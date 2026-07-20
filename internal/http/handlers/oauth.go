package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"html"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/softsrv/ergracer/internal/app"
	"github.com/softsrv/ergracer/internal/http/middleware"
)

const oauthStateCookie = "oauth_state"
const oauthLinkStateCookie = "oauth_link_state"
const oauthConcept2LinkStateCookie = "oauth_concept2_link_state"

type oauthServicer interface {
	HandleDiscordCallback(ctx context.Context, code string, meta app.DeviceMeta) (app.TokenResult, error)
	LinkDiscord(ctx context.Context, userID uuid.UUID, code string) error
	UnlinkDiscord(ctx context.Context, userID uuid.UUID) error
	LinkConcept2(ctx context.Context, userID uuid.UUID, code string) error
	UnlinkConcept2(ctx context.Context, userID uuid.UUID) error
}

// OAuthHandler handles OAuth2 login and linking flows.
type OAuthHandler struct {
	oauth                    oauthServicer
	discordAuthorizeURL      func(state string) string
	discordLinkAuthorizeURL  func(state string) string
	concept2AuthorizeURL     func(state string) string
	secure                   bool
	trustedProxyCount        int
}

// NewOAuthHandler constructs an OAuthHandler.
func NewOAuthHandler(
	oauthSvc oauthServicer,
	discordAuthorizeURL func(state string) string,
	discordLinkAuthorizeURL func(state string) string,
	concept2AuthorizeURL func(state string) string,
	secure bool,
	trustedProxyCount int,
) *OAuthHandler {
	return &OAuthHandler{
		oauth:                   oauthSvc,
		discordAuthorizeURL:     discordAuthorizeURL,
		discordLinkAuthorizeURL: discordLinkAuthorizeURL,
		concept2AuthorizeURL:    concept2AuthorizeURL,
		secure:                  secure,
		trustedProxyCount:       trustedProxyCount,
	}
}

// ── Discord ───────────────────────────────────────────────────────────────────

// DiscordLogin generates an OAuth state, stores it in a cookie, and redirects
// to Discord's authorization endpoint.
func (h *OAuthHandler) DiscordLogin(w http.ResponseWriter, r *http.Request) {
	state, err := generateState()
	if err != nil {
		slog.ErrorContext(r.Context(), "discord login: generate state", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})

	http.Redirect(w, r, h.discordAuthorizeURL(state), http.StatusFound)
}

// DiscordLinkStart initiates the OAuth flow for linking Discord to an existing
// account. Uses a separate state cookie and redirects to the link-specific
// callback route /auth/discord/link/callback.
func (h *OAuthHandler) DiscordLinkStart(w http.ResponseWriter, r *http.Request) {
	state, err := generateState()
	if err != nil {
		slog.ErrorContext(r.Context(), "discord link start: generate state", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oauthLinkStateCookie,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})

	http.Redirect(w, r, h.discordLinkAuthorizeURL(state), http.StatusFound)
}

// DiscordCallback handles the OAuth2 callback for the Discord login flow only.
func (h *OAuthHandler) DiscordCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	loginCookie, loginErr := r.Cookie(oauthStateCookie)
	if loginErr != nil || loginCookie.Value == "" || loginCookie.Value != state {
		slog.WarnContext(r.Context(), "discord callback: state mismatch")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	clearStateCookie(w, h.secure)
	h.completeDiscordLogin(w, r, code)
}

// DiscordLinkCallback handles the OAuth2 callback for the Discord account-link
// flow. The route is behind verifiedMW so middleware.UserFromContext is populated.
func (h *OAuthHandler) DiscordLinkCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	linkCookie, linkErr := r.Cookie(oauthLinkStateCookie)
	if linkErr != nil || linkCookie.Value == "" || linkCookie.Value != state {
		slog.WarnContext(r.Context(), "discord link callback: state mismatch")
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}
	clearLinkStateCookie(w, h.secure)

	if code == "" {
		slog.WarnContext(r.Context(), "discord link callback: missing code")
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}

	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	if err := h.oauth.LinkDiscord(r.Context(), user.ID, code); err != nil {
		slog.WarnContext(r.Context(), "discord link callback: link", "user_id", user.ID, "error", err)
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}

	http.Redirect(w, r, "/profile", http.StatusFound)
}

func (h *OAuthHandler) completeDiscordLogin(w http.ResponseWriter, r *http.Request, code string) {
	if code == "" {
		slog.WarnContext(r.Context(), "discord login callback: missing code")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	meta := deviceMetaFromRequest(r, h.trustedProxyCount)
	result, err := h.oauth.HandleDiscordCallback(r.Context(), code, meta)
	if err != nil {
		slog.WarnContext(r.Context(), "discord login callback: handle", "error", err)
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	setTokenCookies(w, result, h.secure)
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// DiscordUnlink removes the Discord identity link from the authenticated user's account.
func (h *OAuthHandler) DiscordUnlink(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := h.oauth.UnlinkDiscord(r.Context(), user.ID); err != nil {
		slog.WarnContext(r.Context(), "discord unlink", "user_id", user.ID, "error", err)
		if r.Header.Get("HX-Request") == "true" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<div class="alert alert-error text-sm">` + html.EscapeString(err.Error()) + `</div>`)) //nolint:errcheck
			return
		}
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/profile")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/profile", http.StatusFound)
}

// ── Concept2 ──────────────────────────────────────────────────────────────────

// Concept2LinkStart initiates the OAuth flow for linking a Concept2 Logbook
// account to the authenticated user's profile.
func (h *OAuthHandler) Concept2LinkStart(w http.ResponseWriter, r *http.Request) {
	state, err := generateState()
	if err != nil {
		slog.ErrorContext(r.Context(), "concept2 link start: generate state", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oauthConcept2LinkStateCookie,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})

	authorizeURL := h.concept2AuthorizeURL(state)
	slog.DebugContext(r.Context(), "concept2 link: redirecting to authorize", "url", authorizeURL)
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// Concept2LinkCallback completes the Concept2 OAuth linking flow. This route
// is protected by verifiedMW so middleware.UserFromContext is populated.
func (h *OAuthHandler) Concept2LinkCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	cookie, err := r.Cookie(oauthConcept2LinkStateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != state {
		slog.WarnContext(r.Context(), "concept2 link callback: state mismatch")
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}
	clearConcept2LinkStateCookie(w, h.secure)

	if code == "" {
		slog.WarnContext(r.Context(), "concept2 link callback: missing code")
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}

	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	if err := h.oauth.LinkConcept2(r.Context(), user.ID, code); err != nil {
		slog.WarnContext(r.Context(), "concept2 link callback: link", "user_id", user.ID, "error", err)
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}

	http.Redirect(w, r, "/profile", http.StatusFound)
}

// Concept2Unlink removes the Concept2 identity link from the authenticated user's account.
func (h *OAuthHandler) Concept2Unlink(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := h.oauth.UnlinkConcept2(r.Context(), user.ID); err != nil {
		slog.WarnContext(r.Context(), "concept2 unlink", "user_id", user.ID, "error", err)
		if r.Header.Get("HX-Request") == "true" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<div class="alert alert-error text-sm">` + html.EscapeString(err.Error()) + `</div>`)) //nolint:errcheck
			return
		}
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/profile")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/profile", http.StatusFound)
}

// ── Cookie helpers ────────────────────────────────────────────────────────────

func clearStateCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func clearLinkStateCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthLinkStateCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func clearConcept2LinkStateCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthConcept2LinkStateCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
