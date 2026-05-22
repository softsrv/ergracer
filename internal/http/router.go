package http

import (
	"context"
	"net/http"
	"time"

	"github.com/softsrv/starter/internal/app"
	"github.com/softsrv/starter/internal/db"
	"github.com/softsrv/starter/internal/http/handlers"
	"github.com/softsrv/starter/internal/http/middleware"
)

// RouterConfig holds all dependencies required to build the router.
type RouterConfig struct {
	Queries   *db.Queries
	Pool      handlers.DBPinger
	AuthSvc   *app.AuthService
	UserSvc   *app.UserService
	Renderer  *handlers.TemplateRenderer
	JWTSecret string
	Secure    bool // true in production
}

// NewRouter builds and returns the main http.Handler with all routes and middleware.
// ctx controls the lifetime of background goroutines (rate-limiter sweepers); it
// should be cancelled during application shutdown after the HTTP server drains.
func NewRouter(ctx context.Context, cfg RouterConfig) http.Handler {
	mux := http.NewServeMux()

	// ── Handlers ──────────────────────────────────────────────────────────────
	authH := handlers.NewAuthHandler(cfg.AuthSvc, cfg.Renderer, cfg.Secure)
	sessH := handlers.NewSessionHandler(cfg.UserSvc, cfg.Renderer)

	// ── Rate limiters ─────────────────────────────────────────────────────────
	// Each limiter spawns a sweep goroutine that exits when ctx is cancelled.
	loginRL    := middleware.NewRateLimiter(ctx, 5,  15*time.Minute, middleware.IPKeyFunc)
	registerRL := middleware.NewRateLimiter(ctx, 3,  time.Hour,      middleware.IPKeyFunc)
	refreshRL  := middleware.NewRateLimiter(ctx, 10, time.Minute,    middleware.CookieRefreshTokenKeyFunc)
	forgotRL   := middleware.NewRateLimiter(ctx, 3,  time.Hour,      middleware.FormEmailKeyFunc)
	resetRL    := middleware.NewRateLimiter(ctx, 5,  time.Hour,      middleware.IPKeyFunc)

	authMW := middleware.Authenticate(cfg.Queries, cfg.JWTSecret)

	// ── Static assets ─────────────────────────────────────────────────────────
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// ── Public routes ─────────────────────────────────────────────────────────
	mux.HandleFunc("GET /health",  handlers.HandleLiveness)
	mux.HandleFunc("GET /ready",   handlers.HandleReadiness(cfg.Pool))
	mux.HandleFunc("GET /metrics", handlers.HandleMetrics)

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login", http.StatusFound)
	})
	mux.HandleFunc("GET /login",           authH.LoginPage)
	mux.HandleFunc("GET /register",        authH.RegisterPage)
	mux.HandleFunc("GET /forgot-password", authH.ForgotPasswordPage)
	mux.HandleFunc("GET /reset-password",  authH.ResetPasswordPage)
	mux.HandleFunc("GET /verify-email",    authH.VerifyEmailPage)

	mux.Handle("POST /auth/login",           loginRL.Middleware(http.HandlerFunc(authH.Login)))
	mux.Handle("POST /auth/register",        registerRL.Middleware(http.HandlerFunc(authH.Register)))
	mux.Handle("POST /auth/refresh",         refreshRL.Middleware(http.HandlerFunc(authH.Refresh)))
	mux.Handle("POST /auth/forgot-password", forgotRL.Middleware(http.HandlerFunc(authH.ForgotPassword)))
	mux.Handle("POST /auth/reset-password",  resetRL.Middleware(http.HandlerFunc(authH.ResetPassword)))

	// ── Protected routes ──────────────────────────────────────────────────────
	mux.Handle("POST /auth/logout",               authMW(http.HandlerFunc(authH.Logout)))
	mux.Handle("POST /auth/verify-email",         authMW(http.HandlerFunc(authH.VerifyEmail)))
	mux.Handle("POST /auth/resend-verification",  authMW(http.HandlerFunc(authH.ResendVerification)))
	mux.Handle("GET /auth/sessions",         authMW(http.HandlerFunc(sessH.ListSessions)))
	mux.Handle("DELETE /auth/sessions/{id}", authMW(http.HandlerFunc(sessH.RevokeSession)))

	mux.Handle("GET /dashboard", authMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := middleware.UserFromContext(r.Context())
		cfg.Renderer.Page(w, http.StatusOK, "dashboard.html", map[string]any{
			"User":      user,
			"CSRFToken": middleware.CSRFTokenFromRequest(r),
		})
	})))

	// ── Global middleware chain ───────────────────────────────────────────────
	// CSRF is passed cfg.Secure so the CSRF cookie carries the Secure flag in production.
	return middleware.RequestID(
		middleware.Logging(
			middleware.CSRF(cfg.Secure)(mux),
		),
	)
}
