package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/softsrv/rowbot/internal/db"
	"github.com/softsrv/rowbot/internal/http/middleware"
)

type profileUserServicer interface {
	DeleteAccount(ctx context.Context, userID uuid.UUID) error
}

// ProfileOAuthServicer is the OAuth capability subset needed by the profile page.
// Exported so router.go can pass *app.OAuthService without a direct dependency.
type ProfileOAuthServicer interface {
	IsDiscordEnabled() bool
	IsConcept2Enabled() bool
	GetDiscordIdentity(ctx context.Context, userID uuid.UUID) (db.OauthIdentity, error)
	GetConcept2Identity(ctx context.Context, userID uuid.UUID) (db.OauthIdentity, error)
}

// discordRegistrationChecker is the subset of app.DiscordService needed by the
// dashboard to know whether the user has registered (via /register) in at
// least one Discord server.
type discordRegistrationChecker interface {
	HasDiscordRegistration(ctx context.Context, userID uuid.UUID) (bool, error)
}

// ProfileHandler groups profile management HTTP handlers.
type ProfileHandler struct {
	users                profileUserServicer
	oauth                ProfileOAuthServicer
	discordRegChecker    discordRegistrationChecker
	discordBotInstallURL string
	renderer             *TemplateRenderer
	secure               bool
}

// NewProfileHandler constructs a ProfileHandler. oauthSvc may be nil when
// OAuth is not configured; the dashboard will hide the Integrations card.
// discordBotInstallURL may be empty when Discord is not configured or the bot
// install feature is disabled. discordRegChecker may be nil, in which case the
// dashboard treats the user as having no server registration.
func NewProfileHandler(userSvc profileUserServicer, oauthSvc ProfileOAuthServicer, discordRegChecker discordRegistrationChecker, discordBotInstallURL string, renderer *TemplateRenderer, secure bool) *ProfileHandler {
	return &ProfileHandler{
		users:                userSvc,
		oauth:                oauthSvc,
		discordRegChecker:    discordRegChecker,
		discordBotInstallURL: discordBotInstallURL,
		renderer:             renderer,
		secure:               secure,
	}
}

// ProfilePage renders the user profile page.
func (h *ProfileHandler) ProfilePage(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	data := map[string]any{"User": user}
	h.renderer.Page(w, http.StatusOK, "profile.html", data)
}

// DashboardPage renders the main dashboard. It is the public, step-by-step
// setup guide and single sign-in entry point for the whole site — it must
// render sensibly for both anonymous and authenticated visitors, since it has
// no auth middleware in front of it.
func (h *ProfileHandler) DashboardPage(w http.ResponseWriter, r *http.Request) {
	user, loggedIn := middleware.UserFromContext(r.Context())

	data := map[string]any{
		"LoggedIn":              loggedIn,
		"DiscordBotInstallURL":  h.discordBotInstallURL,
		"DiscordEnabled":        false,
		"DiscordUsername":       "",
		"HasServerRegistration": false,
		"Concept2Enabled":       false,
		"Concept2Linked":        false,
		"Concept2Username":      "",
		"AllSet":                false,
	}

	// Which integrations are configured at all is server-side config, not
	// user-specific state — computed regardless of login status so an
	// anonymous visitor sees the full 4-step preview (e.g. step 4 shouldn't
	// disappear just because they haven't signed in yet).
	if h.oauth != nil {
		data["DiscordEnabled"] = h.oauth.IsDiscordEnabled()
		data["Concept2Enabled"] = h.oauth.IsConcept2Enabled()
	}

	if loggedIn {
		data["User"] = user

		if h.oauth != nil {
			if h.oauth.IsDiscordEnabled() {
				if identity, err := h.oauth.GetDiscordIdentity(r.Context(), user.ID); err == nil {
					data["DiscordUsername"] = identity.ProviderUsername.String
				}
			}

			if h.oauth.IsConcept2Enabled() {
				if identity, err := h.oauth.GetConcept2Identity(r.Context(), user.ID); err == nil {
					data["Concept2Linked"] = true
					data["Concept2Username"] = identity.ProviderUsername.String
				}
			}
		}

		if h.discordRegChecker != nil {
			hasReg, err := h.discordRegChecker.HasDiscordRegistration(r.Context(), user.ID)
			if err != nil {
				slog.WarnContext(r.Context(), "dashboard: check discord registration", "user_id", user.ID, "error", err)
			} else {
				data["HasServerRegistration"] = hasReg
			}
		}

		data["AllSet"] = data["HasServerRegistration"].(bool) && data["Concept2Linked"].(bool)
	}

	h.renderer.Page(w, http.StatusOK, "dashboard.html", data)
}

// DeleteAccount permanently deletes the authenticated user's account.
func (h *ProfileHandler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := h.users.DeleteAccount(r.Context(), user.ID); err != nil {
		slog.ErrorContext(r.Context(), "delete account", "user_id", user.ID, "error", err)
		h.renderError(w, r, http.StatusInternalServerError, "Failed to delete account. Please try again.")
		return
	}

	clearAuthCookies(w, h.secure)
	w.Header().Set("HX-Redirect", "/dashboard")
	w.WriteHeader(http.StatusOK)
}

func (h *ProfileHandler) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		status = http.StatusOK
	}
	h.renderer.Partial(w, status, "partials/error.html", map[string]any{"Error": msg})
}
