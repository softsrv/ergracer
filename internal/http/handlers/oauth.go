package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"html"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/softsrv/rowbot/internal/app"
	"github.com/softsrv/rowbot/internal/discord"
	"github.com/softsrv/rowbot/internal/http/middleware"
)

const oauthStateCookie = "oauth_state"
const oauthLoginModeCookie = "oauth_login_mode"
const oauthLinkStateCookie = "oauth_link_state"
const oauthConcept2LinkStateCookie = "oauth_concept2_link_state"

// oauthConcept2LinkNextCookie remembers which page a Concept2 link attempt
// started from ("profile" or "dashboard"), so Concept2LinkCallback can send
// the user back there — success or failure — rather than a fixed page that
// may not even show Concept2 status. Only ever holds one of those two fixed
// keywords, never an arbitrary path, so it can't become an open redirect.
const oauthConcept2LinkNextCookie = "oauth_concept2_link_next"

// oauthLoginModeSilent/oauthLoginModeConsent are the values stored in
// oauthLoginModeCookie, distinguishing a prompt=none attempt from its
// full-consent fallback so DiscordCallback knows whether to retry.
const (
	oauthLoginModeSilent  = "silent"
	oauthLoginModeConsent = "consent"
)

type oauthServicer interface {
	HandleDiscordCallback(ctx context.Context, code string, meta app.DeviceMeta) (app.TokenResult, error)
	LinkDiscord(ctx context.Context, userID uuid.UUID, code string) error
	LinkConcept2(ctx context.Context, userID uuid.UUID, code string) error
	UnlinkConcept2(ctx context.Context, userID uuid.UUID) error
}

// guildRecorder is the subset of app.DiscordService used by
// DiscordBotInstallCallback to record which guild the bot was just
// installed into.
type guildRecorder interface {
	RecordGuildSeen(ctx context.Context, guildID, guildName string) error
}

// setupProgressServicer is the subset of app.UserService needed by
// DiscordBotInstallCallback to advance a mid-wizard user past step 1 once
// the Discord bot-install round-trip actually completes.
type setupProgressServicer interface {
	SetSetupProgress(ctx context.Context, userID uuid.UUID, progress int32) error
}

// OAuthHandler handles OAuth2 login and linking flows.
type OAuthHandler struct {
	oauth                     oauthServicer
	discordAuthorizeURL       func(state string) string
	discordSilentAuthorizeURL func(state string) string
	discordLinkAuthorizeURL   func(state string) string
	concept2AuthorizeURL      func(state string) string
	guildRecorder             guildRecorder
	botToken                  string
	httpClient                *http.Client
	users                     setupProgressServicer
	secure                    bool
	trustedProxyCount         int
}

// NewOAuthHandler constructs an OAuthHandler. When httpClient is nil,
// http.DefaultClient is used.
func NewOAuthHandler(
	oauthSvc oauthServicer,
	discordAuthorizeURL func(state string) string,
	discordSilentAuthorizeURL func(state string) string,
	discordLinkAuthorizeURL func(state string) string,
	concept2AuthorizeURL func(state string) string,
	guildRecorder guildRecorder,
	botToken string,
	httpClient *http.Client,
	users setupProgressServicer,
	secure bool,
	trustedProxyCount int,
) *OAuthHandler {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &OAuthHandler{
		oauth:                     oauthSvc,
		discordAuthorizeURL:       discordAuthorizeURL,
		discordSilentAuthorizeURL: discordSilentAuthorizeURL,
		discordLinkAuthorizeURL:   discordLinkAuthorizeURL,
		concept2AuthorizeURL:      concept2AuthorizeURL,
		guildRecorder:             guildRecorder,
		botToken:                  botToken,
		httpClient:                httpClient,
		users:                     users,
		secure:                    secure,
		trustedProxyCount:         trustedProxyCount,
	}
}

// ── Discord ───────────────────────────────────────────────────────────────────

// DiscordLogin generates an OAuth state, stores it in a cookie, and redirects
// to Discord's authorization endpoint. It first tries a silent (prompt=none)
// request so a user who has already authorized this app isn't shown the
// consent screen again; DiscordCallback falls back to a full-consent request
// if Discord reports the silent attempt failed (e.g. this is a first-time
// authorization).
func (h *OAuthHandler) DiscordLogin(w http.ResponseWriter, r *http.Request) {
	h.startDiscordLogin(w, r, true)
}

// startDiscordLogin generates a fresh state, records whether this attempt is
// silent or full-consent (so DiscordCallback knows whether a failure is
// retryable), and redirects to the corresponding Discord authorize URL.
func (h *OAuthHandler) startDiscordLogin(w http.ResponseWriter, r *http.Request, silent bool) {
	state, err := generateState()
	if err != nil {
		slog.ErrorContext(r.Context(), "discord login: generate state", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	mode := oauthLoginModeConsent
	authorizeURL := h.discordAuthorizeURL
	if silent {
		mode = oauthLoginModeSilent
		authorizeURL = h.discordSilentAuthorizeURL
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
	http.SetCookie(w, &http.Cookie{
		Name:     oauthLoginModeCookie,
		Value:    mode,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})

	http.Redirect(w, r, authorizeURL(state), http.StatusFound)
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
	oauthErr := r.URL.Query().Get("error")

	loginCookie, loginErr := r.Cookie(oauthStateCookie)
	if loginErr != nil || loginCookie.Value == "" || loginCookie.Value != state {
		slog.WarnContext(r.Context(), "discord callback: state mismatch")
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	wasSilent := false
	if modeCookie, modeErr := r.Cookie(oauthLoginModeCookie); modeErr == nil {
		wasSilent = modeCookie.Value == oauthLoginModeSilent
	}

	clearStateCookie(w, h.secure)
	clearLoginModeCookie(w, h.secure)

	if oauthErr != "" {
		if wasSilent {
			// prompt=none can't show any UI, so a user who hasn't authorized
			// yet (or revoked access) comes back as an error rather than a
			// consent screen. Retry once with the normal, full-consent flow.
			slog.DebugContext(r.Context(), "discord callback: silent auth failed, retrying with consent", "error", oauthErr)
			h.startDiscordLogin(w, r, false)
			return
		}
		slog.WarnContext(r.Context(), "discord callback: authorization failed", "error", oauthErr)
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	h.completeDiscordLogin(w, r, code)
}

// DiscordLinkCallback handles the OAuth2 callback for the Discord account-link
// flow. The route is behind authMW so middleware.UserFromContext is populated.
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
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	if err := h.oauth.LinkDiscord(r.Context(), user.ID, code); err != nil {
		slog.WarnContext(r.Context(), "discord link callback: link", "user_id", user.ID, "error", err)
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}

	http.Redirect(w, r, "/profile", http.StatusFound)
}

// DiscordBotInstallCallback handles the redirect Discord sends after an
// admin installs the bot into a server via the "extended bot
// authorization" flow (BotInstallURL). It reads the guild_id Discord
// includes on this redirect and records it — see DiscordService.RecordGuildSeen.
// Protected by authMW; no state-cookie CSRF check is needed here since
// nothing sensitive is being granted to the visitor (unlike the login/link
// flows, this doesn't create a session or link an identity, just records
// which server got the bot added — worst case of a forged guild_id is a
// bogus row in discord_guilds, not an account compromise).
// DiscordBotInstallCallback is reached from a same-tab navigation to
// Discord's install flow — both the setup wizard's step 1 "Yes" link and the
// profile page's "Add a server" modal link to it directly rather than
// opening a new tab, so Discord always lands the browser back here in the
// same tab the user started from. If the authenticated user is still on
// wizard step 1 at that point, this advances them to step 2; either way it
// redirects to /dashboard once done, the same same-tab round-trip the
// Discord login flow already uses.
func (h *OAuthHandler) DiscordBotInstallCallback(w http.ResponseWriter, r *http.Request) {
	guildID := r.URL.Query().Get("guild_id")
	if guildID == "" {
		slog.WarnContext(r.Context(), "discord bot install callback: missing guild_id")
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	guildName := "unknown server"
	if h.botToken != "" {
		guildCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if name, err := discord.GetGuildName(guildCtx, h.httpClient, h.botToken, guildID); err == nil {
			guildName = name
		} else {
			slog.WarnContext(r.Context(), "discord bot install callback: get guild name", "guild_id", guildID, "error", err)
		}
	}

	if h.guildRecorder != nil {
		if err := h.guildRecorder.RecordGuildSeen(r.Context(), guildID, guildName); err != nil {
			slog.WarnContext(r.Context(), "discord bot install callback: record guild seen", "guild_id", guildID, "error", err)
		}
	}

	if user, ok := middleware.UserFromContext(r.Context()); ok && user.SetupProgress == setupStepFirst && h.users != nil {
		if err := h.users.SetSetupProgress(r.Context(), user.ID, setupStepFirst+1); err != nil {
			slog.WarnContext(r.Context(), "discord bot install callback: advance setup progress", "user_id", user.ID, "error", err)
		}
	}

	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (h *OAuthHandler) completeDiscordLogin(w http.ResponseWriter, r *http.Request, code string) {
	if code == "" {
		slog.WarnContext(r.Context(), "discord login callback: missing code")
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	meta := deviceMetaFromRequest(r, h.trustedProxyCount)
	result, err := h.oauth.HandleDiscordCallback(r.Context(), code, meta)
	if err != nil {
		slog.WarnContext(r.Context(), "discord login callback: handle", "error", err)
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	setTokenCookies(w, result, h.secure)
	http.Redirect(w, r, "/dashboard", http.StatusFound)
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

	next := "dashboard"
	if r.URL.Query().Get("next") == "profile" {
		next = "profile"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthConcept2LinkNextCookie,
		Value:    next,
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
// is protected by authMW so middleware.UserFromContext is populated.
func (h *OAuthHandler) Concept2LinkCallback(w http.ResponseWriter, r *http.Request) {
	// Read (and clear) where this attempt started before anything else, so
	// every exit path below — success or failure — lands the user back
	// there, whether that's the profile page's Connections card or the
	// onboarding wizard's step 4.
	redirectTarget := concept2LinkRedirectTarget(w, r, h.secure)

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	cookie, err := r.Cookie(oauthConcept2LinkStateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != state {
		slog.WarnContext(r.Context(), "concept2 link callback: state mismatch")
		http.Redirect(w, r, redirectTarget, http.StatusFound)
		return
	}
	clearConcept2LinkStateCookie(w, h.secure)

	if code == "" {
		slog.WarnContext(r.Context(), "concept2 link callback: missing code")
		http.Redirect(w, r, redirectTarget, http.StatusFound)
		return
	}

	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, redirectTarget, http.StatusFound)
		return
	}

	if err := h.oauth.LinkConcept2(r.Context(), user.ID, code); err != nil {
		slog.WarnContext(r.Context(), "concept2 link callback: link", "user_id", user.ID, "error", err)
		http.Redirect(w, r, redirectTarget, http.StatusFound)
		return
	}

	http.Redirect(w, r, redirectTarget, http.StatusFound)
}

// concept2LinkRedirectTarget reads and clears the oauthConcept2LinkNextCookie
// set by Concept2LinkStart, returning "/dashboard" or "/profile". Defaults to
// "/dashboard" if the cookie is missing (e.g. a stale or direct hit on this
// callback).
func concept2LinkRedirectTarget(w http.ResponseWriter, r *http.Request, secure bool) string {
	target := "/dashboard"
	if cookie, err := r.Cookie(oauthConcept2LinkNextCookie); err == nil && cookie.Value == "profile" {
		target = "/profile"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthConcept2LinkNextCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	return target
}

// Concept2Unlink removes the Concept2 identity link from the authenticated
// user's account. Only ever reached from the profile page's Connections
// card, so it redirects back there rather than /dashboard (which no longer
// shows any Concept2 status outside the onboarding wizard).
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

func clearLoginModeCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthLoginModeCookie,
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
