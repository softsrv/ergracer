package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/softsrv/ergracer/internal/app"
	"github.com/softsrv/ergracer/internal/db"
	"github.com/softsrv/ergracer/internal/http/middleware"
)

type profileAuthServicer interface {
	ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string) error
	SetPassword(ctx context.Context, userID uuid.UUID, newPassword string) error
}

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

// ProfileHandler groups profile management HTTP handlers.
type ProfileHandler struct {
	auth                 profileAuthServicer
	users                profileUserServicer
	oauth                ProfileOAuthServicer
	discordBotInstallURL string
	renderer             *TemplateRenderer
	secure               bool
}

// NewProfileHandler constructs a ProfileHandler. oauthSvc may be nil when
// OAuth is not configured; the profile page will hide the Integrations card.
// discordBotInstallURL may be empty when Discord is not configured or the bot
// install feature is disabled.
func NewProfileHandler(authSvc profileAuthServicer, userSvc profileUserServicer, oauthSvc ProfileOAuthServicer, discordBotInstallURL string, renderer *TemplateRenderer, secure bool) *ProfileHandler {
	return &ProfileHandler{auth: authSvc, users: userSvc, oauth: oauthSvc, discordBotInstallURL: discordBotInstallURL, renderer: renderer, secure: secure}
}

// ProfilePage renders the user profile page.
func (h *ProfileHandler) ProfilePage(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	data := map[string]any{
		"User":                 user,
		"DiscordLinked":        false,
		"DiscordUsername":      "",
		"DiscordEnabled":       false,
		"DiscordBotInstallURL": h.discordBotInstallURL,
		"Concept2Linked":       false,
		"Concept2Username":     "",
		"Concept2Enabled":      false,
	}

	if h.oauth != nil {
		data["DiscordEnabled"] = h.oauth.IsDiscordEnabled()
		data["Concept2Enabled"] = h.oauth.IsConcept2Enabled()

		if h.oauth.IsDiscordEnabled() {
			if identity, err := h.oauth.GetDiscordIdentity(r.Context(), user.ID); err == nil {
				data["DiscordLinked"] = true
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

	h.renderer.Page(w, http.StatusOK, "profile.html", data)
}

// ChangePassword handles password change form submission.
func (h *ProfileHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "Invalid form data")
		return
	}

	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	confirmPassword := r.FormValue("confirm_password")

	if newPassword != confirmPassword {
		h.renderError(w, r, http.StatusUnprocessableEntity, "New passwords do not match")
		return
	}

	if err := h.auth.ChangePassword(r.Context(), user.ID, currentPassword, newPassword); err != nil {
		slog.WarnContext(r.Context(), "change password failed", "user_id", user.ID, "error", err)
		msg := err.Error()
		if errors.Is(err, app.ErrInvalidCredentials) {
			msg = "Current password is incorrect"
		}
		h.renderError(w, r, http.StatusUnprocessableEntity, msg)
		return
	}

	h.renderer.Partial(w, http.StatusOK, "partials/flash.html", map[string]any{
		"Message": "Password changed successfully.",
	})
}

// SetPassword sets a password for a user who has none (e.g. signed up via OAuth).
func (h *ProfileHandler) SetPassword(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "Invalid form data")
		return
	}

	newPassword := r.FormValue("password")
	confirmPassword := r.FormValue("confirm_password")

	if newPassword != confirmPassword {
		h.renderError(w, r, http.StatusUnprocessableEntity, "Passwords do not match")
		return
	}

	if err := h.auth.SetPassword(r.Context(), user.ID, newPassword); err != nil {
		slog.WarnContext(r.Context(), "set password failed", "user_id", user.ID, "error", err)
		h.renderError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}

	h.renderer.Partial(w, http.StatusOK, "partials/flash.html", map[string]any{
		"Message": "Password set successfully.",
	})
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
	w.Header().Set("HX-Redirect", "/login")
	w.WriteHeader(http.StatusOK)
}

func (h *ProfileHandler) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		status = http.StatusOK
	}
	h.renderer.Partial(w, status, "partials/error.html", map[string]any{"Error": msg})
}
