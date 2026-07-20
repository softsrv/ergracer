package http

import (
	"context"
	"io/fs"
	"net/http"
	"time"

	"github.com/softsrv/ergracer/internal/app"
	"github.com/softsrv/ergracer/internal/auth"
	"github.com/softsrv/ergracer/internal/db"
	"github.com/softsrv/ergracer/internal/http/handlers"
	"github.com/softsrv/ergracer/internal/http/middleware"
	"github.com/softsrv/ergracer/web"
)

// RouterConfig holds all dependencies required to build the router.
type RouterConfig struct {
	Queries                   *db.Queries
	Pool                      handlers.DBPinger
	AuthSvc                   *app.AuthService
	UserSvc                   *app.UserService
	OAuthSvc                  *app.OAuthService
	DiscordAuthorizeURL       func(state string) string
	DiscordLinkAuthorizeURL   func(state string) string
	DiscordBotInstallURL      string
	DiscordInteractions       *handlers.DiscordHandler
	Concept2AuthorizeURL      func(state string) string
	Concept2WebhookSecret     string
	RowingSvc                 *app.RowingService
	Renderer                  *handlers.TemplateRenderer
	JWTSecret                 string
	Secure                    bool // true in production
	TrustedProxyCount         int
}

// NewRouter builds and returns the main http.Handler with all routes and middleware.
// ctx controls the lifetime of background goroutines (rate-limiter sweepers); it
// should be cancelled during application shutdown after the HTTP server drains.
func NewRouter(ctx context.Context, cfg RouterConfig) http.Handler {
	mux := http.NewServeMux()

	// ── Handlers ──────────────────────────────────────────────────────────────
	authH := handlers.NewAuthHandler(cfg.AuthSvc, cfg.Renderer, cfg.Secure, cfg.TrustedProxyCount)
	sessH := handlers.NewSessionHandler(cfg.UserSvc, cfg.Renderer, cfg.Secure)

	var profileOAuth handlers.ProfileOAuthServicer
	if cfg.OAuthSvc != nil {
		profileOAuth = cfg.OAuthSvc
	}
	profileH := handlers.NewProfileHandler(cfg.AuthSvc, cfg.UserSvc, profileOAuth, cfg.DiscordBotInstallURL, cfg.Renderer, cfg.Secure)

	// ── Rate limiters ─────────────────────────────────────────────────────────
	// Each limiter spawns a sweep goroutine that exits when ctx is cancelled.
	ipKey := middleware.IPKeyFunc(cfg.TrustedProxyCount)
	loginRL := middleware.NewRateLimiter(ctx, 5, 15*time.Minute, ipKey)
	registerRL := middleware.NewRateLimiter(ctx, 3, time.Hour, ipKey)
	refreshRL := middleware.NewRateLimiter(ctx, 10, time.Minute, middleware.CookieRefreshTokenKeyFunc)
	forgotRL := middleware.NewRateLimiter(ctx, 3, time.Hour, middleware.FormEmailKeyFunc)
	resetRL := middleware.NewRateLimiter(ctx, 5, time.Hour, ipKey)

	authMW := middleware.Authenticate(cfg.Queries, cfg.JWTSecret)
	verifiedMW := func(h http.Handler) http.Handler { return authMW(middleware.RequireEmailVerified(h)) }

	if cfg.OAuthSvc != nil {
		oauthH := handlers.NewOAuthHandler(cfg.OAuthSvc, cfg.DiscordAuthorizeURL, cfg.DiscordLinkAuthorizeURL, cfg.Concept2AuthorizeURL, cfg.Secure, cfg.TrustedProxyCount)
		discordRL := middleware.NewRateLimiter(ctx, 10, 15*time.Minute, ipKey)
		mux.Handle("GET /auth/discord/login", discordRL.Middleware(http.HandlerFunc(oauthH.DiscordLogin)))
		mux.Handle("GET /auth/discord/callback", discordRL.Middleware(http.HandlerFunc(oauthH.DiscordCallback)))
		mux.Handle("GET /auth/discord/link", verifiedMW(http.HandlerFunc(oauthH.DiscordLinkStart)))
		mux.Handle("GET /auth/discord/link/callback", verifiedMW(http.HandlerFunc(oauthH.DiscordLinkCallback)))
		mux.Handle("POST /profile/integrations/discord/unlink", verifiedMW(http.HandlerFunc(oauthH.DiscordUnlink)))

		mux.Handle("GET /auth/concept2/link", verifiedMW(http.HandlerFunc(oauthH.Concept2LinkStart)))
		mux.Handle("GET /auth/concept2/link/callback", verifiedMW(http.HandlerFunc(oauthH.Concept2LinkCallback)))
		mux.Handle("POST /profile/integrations/concept2/unlink", verifiedMW(http.HandlerFunc(oauthH.Concept2Unlink)))
	}

	// ── Static assets ─────────────────────────────────────────────────────────
	staticFS, _ := fs.Sub(web.FS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	// ── Discord interactions (public — protected by Ed25519 signature) ────────
	if cfg.DiscordInteractions != nil {
		mux.HandleFunc("POST /discord/interactions", cfg.DiscordInteractions.Interactions)
	}

	// ── Concept2 webhook (public — protected by HMAC-SHA256 signature) ────────
	webhookH := handlers.NewWebhookHandler(cfg.Concept2WebhookSecret, cfg.RowingSvc)
	mux.Handle("POST /webhooks/concept2", http.HandlerFunc(webhookH.Concept2))

	// ── Public routes ─────────────────────────────────────────────────────────
	mux.HandleFunc("GET /health", handlers.HandleLiveness)
	mux.HandleFunc("GET /ready", handlers.HandleReadiness(cfg.Pool))

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Only send the user to the dashboard if they present a genuinely valid
		// access token; a stale or malformed cookie should land on /login rather
		// than bouncing through a protected route.
		if cookie, err := r.Cookie("access_token"); err == nil && cookie.Value != "" {
			if _, vErr := auth.ValidateAccessToken(cookie.Value, cfg.JWTSecret); vErr == nil {
				http.Redirect(w, r, "/dashboard", http.StatusFound)
				return
			}
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
	mux.HandleFunc("GET /login", authH.LoginPage)
	mux.HandleFunc("GET /register", authH.RegisterPage)
	mux.HandleFunc("GET /forgot-password", authH.ForgotPasswordPage)
	mux.HandleFunc("GET /reset-password", authH.ResetPasswordPage)
	mux.HandleFunc("GET /verify-email", authH.VerifyEmailPage)

	mux.Handle("GET /auth/silent-refresh", refreshRL.Middleware(http.HandlerFunc(authH.SilentRefresh)))
	mux.Handle("POST /auth/login", loginRL.Middleware(http.HandlerFunc(authH.Login)))
	mux.Handle("POST /auth/register", registerRL.Middleware(http.HandlerFunc(authH.Register)))
	mux.Handle("POST /auth/refresh", refreshRL.Middleware(http.HandlerFunc(authH.Refresh)))
	mux.Handle("POST /auth/forgot-password", forgotRL.Middleware(http.HandlerFunc(authH.ForgotPassword)))
	mux.Handle("POST /auth/reset-password", resetRL.Middleware(http.HandlerFunc(authH.ResetPassword)))

	// ── Protected routes ──────────────────────────────────────────────────────
	mux.Handle("POST /auth/logout", authMW(http.HandlerFunc(authH.Logout)))
	mux.Handle("POST /auth/resend-verification", authMW(http.HandlerFunc(authH.ResendVerification)))

	// Email verification: public GET so users can click directly from their inbox.
	mux.HandleFunc("GET /auth/verify-email", authH.VerifyEmail)
	mux.Handle("GET /auth/sessions", verifiedMW(http.HandlerFunc(sessH.ListSessions)))
	mux.Handle("DELETE /auth/sessions/{id}", verifiedMW(http.HandlerFunc(sessH.RevokeSession)))

	mux.Handle("GET /profile", verifiedMW(http.HandlerFunc(profileH.ProfilePage)))
	mux.Handle("POST /profile/change-password", verifiedMW(http.HandlerFunc(profileH.ChangePassword)))
	mux.Handle("POST /profile/set-password", verifiedMW(http.HandlerFunc(profileH.SetPassword)))
	mux.Handle("POST /profile/delete", verifiedMW(http.HandlerFunc(profileH.DeleteAccount)))

	mux.Handle("GET /dashboard", verifiedMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := middleware.UserFromContext(r.Context())
		cfg.Renderer.Page(w, http.StatusOK, "dashboard.html", map[string]any{
			"User": user,
		})
	})))

	// ── Global middleware chain ───────────────────────────────────────────────
	// BodyLimit is innermost so it wraps r.Body before any handler reads it.
	return middleware.RequestID(
		middleware.Logging(
			middleware.SecurityHeaders(cfg.Secure,
				middleware.BodyLimit(middleware.DefaultMaxBodyBytes)(mux),
			),
		),
	)
}
