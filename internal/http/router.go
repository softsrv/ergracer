package http

import (
	"context"
	"io/fs"
	"net/http"
	"time"

	"github.com/softsrv/rowbot/internal/app"
	"github.com/softsrv/rowbot/internal/auth"
	"github.com/softsrv/rowbot/internal/db"
	"github.com/softsrv/rowbot/internal/http/handlers"
	"github.com/softsrv/rowbot/internal/http/middleware"
	"github.com/softsrv/rowbot/web"
)

// RouterConfig holds all dependencies required to build the router.
type RouterConfig struct {
	Queries                   *db.Queries
	Pool                      handlers.DBPinger
	AuthSvc                   *app.AuthService
	UserSvc                   *app.UserService
	OAuthSvc                  *app.OAuthService
	DiscordAuthorizeURL       func(state string) string
	DiscordSilentAuthorizeURL func(state string) string
	DiscordLinkAuthorizeURL   func(state string) string
	DiscordBotInstallURL      string
	DiscordInteractions       *handlers.DiscordHandler
	DiscordSvc                *app.DiscordService
	DiscordBotToken           string
	Concept2AuthorizeURL      func(state string) string
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
	profileH := handlers.NewProfileHandler(cfg.UserSvc, profileOAuth, cfg.DiscordSvc, cfg.DiscordBotInstallURL, cfg.Renderer, cfg.Secure)

	// ── Rate limiters ─────────────────────────────────────────────────────────
	// Each limiter spawns a sweep goroutine that exits when ctx is cancelled.
	ipKey := middleware.IPKeyFunc(cfg.TrustedProxyCount)
	refreshRL := middleware.NewRateLimiter(ctx, 10, time.Minute, middleware.CookieRefreshTokenKeyFunc)

	authMW := middleware.Authenticate(cfg.Queries, cfg.JWTSecret)
	verifiedMW := func(h http.Handler) http.Handler { return authMW(middleware.RequireEmailVerified(h)) }

	if cfg.OAuthSvc != nil {
		oauthH := handlers.NewOAuthHandler(cfg.OAuthSvc, cfg.DiscordAuthorizeURL, cfg.DiscordSilentAuthorizeURL, cfg.DiscordLinkAuthorizeURL, cfg.Concept2AuthorizeURL, cfg.DiscordSvc, cfg.DiscordBotToken, nil, cfg.Secure, cfg.TrustedProxyCount)
		discordRL := middleware.NewRateLimiter(ctx, 10, 15*time.Minute, ipKey)
		mux.Handle("GET /auth/discord/login", discordRL.Middleware(http.HandlerFunc(oauthH.DiscordLogin)))
		mux.Handle("GET /auth/discord/callback", discordRL.Middleware(http.HandlerFunc(oauthH.DiscordCallback)))
		mux.Handle("GET /auth/discord/link", verifiedMW(http.HandlerFunc(oauthH.DiscordLinkStart)))
		mux.Handle("GET /auth/discord/link/callback", verifiedMW(http.HandlerFunc(oauthH.DiscordLinkCallback)))
		mux.Handle("GET /auth/discord/bot-install/callback", verifiedMW(http.HandlerFunc(oauthH.DiscordBotInstallCallback)))

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

	// ── Concept2 webhook (public — trust is structural, not cryptographic; ────
	// Concept2 does not sign deliveries, so the handler only validates payload
	// shape and re-fetches real data via our own OAuth token; see webhooks.go)
	webhookH := handlers.NewWebhookHandler(cfg.RowingSvc)
	mux.Handle("POST /webhooks/concept2", http.HandlerFunc(webhookH.Concept2))

	// ── Public routes ─────────────────────────────────────────────────────────
	mux.HandleFunc("GET /health", handlers.HandleLiveness)
	mux.HandleFunc("GET /ready", handlers.HandleReadiness(cfg.Pool))

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Send an already-authenticated visitor straight to the dashboard;
		// only send them there for a genuinely valid access token — a stale
		// or malformed cookie should land on the landing page rather than
		// bouncing through a protected route. Anonymous visitors see the
		// marketing landing page instead of being redirected anywhere.
		if cookie, err := r.Cookie("access_token"); err == nil && cookie.Value != "" {
			if _, vErr := auth.ValidateAccessToken(cookie.Value, cfg.JWTSecret); vErr == nil {
				http.Redirect(w, r, "/dashboard", http.StatusFound)
				return
			}
		}
		cfg.Renderer.Page(w, http.StatusOK, "landing.html", nil)
	})

	mux.Handle("GET /auth/silent-refresh", refreshRL.Middleware(http.HandlerFunc(authH.SilentRefresh)))
	mux.Handle("POST /auth/refresh", refreshRL.Middleware(http.HandlerFunc(authH.Refresh)))

	// ── Protected routes ──────────────────────────────────────────────────────
	mux.Handle("POST /auth/logout", authMW(http.HandlerFunc(authH.Logout)))

	mux.Handle("GET /auth/sessions", verifiedMW(http.HandlerFunc(sessH.ListSessions)))
	mux.Handle("DELETE /auth/sessions/{id}", verifiedMW(http.HandlerFunc(sessH.RevokeSession)))

	mux.Handle("GET /profile", verifiedMW(http.HandlerFunc(profileH.ProfilePage)))
	mux.Handle("POST /profile/delete", verifiedMW(http.HandlerFunc(profileH.DeleteAccount)))

	// Dashboard is the public sign-in/setup entry point for the whole site —
	// it must render correctly for both anonymous and authenticated visitors,
	// so it uses OptionalAuthenticate (populates the user context when a valid
	// session exists, but never rejects/redirects when one doesn't) rather
	// than the hard-reject authMW/verifiedMW used by protected routes.
	optionalAuthMW := middleware.OptionalAuthenticate(cfg.Queries, cfg.JWTSecret)
	mux.Handle("GET /dashboard", optionalAuthMW(http.HandlerFunc(profileH.DashboardPage)))

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
