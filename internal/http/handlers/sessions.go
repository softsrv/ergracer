package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/softsrv/starter/internal/db"
	"github.com/softsrv/starter/internal/http/middleware"
)

// userServicer defines the subset of app.UserService that SessionHandler requires.
// Accepting an interface makes the handler independently testable.
type userServicer interface {
	ListSessions(ctx context.Context, userID uuid.UUID) ([]db.RefreshToken, error)
	RevokeSession(ctx context.Context, userID, tokenID uuid.UUID) error
}

// SessionHandler groups session management HTTP handlers.
type SessionHandler struct {
	users    userServicer
	renderer *TemplateRenderer
}

// NewSessionHandler constructs a SessionHandler.
func NewSessionHandler(userSvc userServicer, renderer *TemplateRenderer) *SessionHandler {
	return &SessionHandler{users: userSvc, renderer: renderer}
}

// ListSessions renders the active sessions for the authenticated user.
func (h *SessionHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	sessions, err := h.users.ListSessions(r.Context(), user.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "list sessions", "user_id", user.ID, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	h.renderer.Partial(w, http.StatusOK, "partials/session-list.html", map[string]any{
		"Sessions": sessions,
		"UserID":   user.ID,
	})
}

// RevokeSession revokes a specific refresh token, then re-renders the session list.
func (h *SessionHandler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	tokenIDStr := r.PathValue("id")
	tokenID, err := uuid.Parse(tokenIDStr)
	if err != nil {
		slog.WarnContext(r.Context(), "revoke session: invalid token id", "token_id", tokenIDStr, "error", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if err := h.users.RevokeSession(r.Context(), user.ID, tokenID); err != nil {
		slog.ErrorContext(r.Context(), "revoke session", "user_id", user.ID, "token_id", tokenID, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Return updated session list fragment for HTMX swap.
	sessions, err := h.users.ListSessions(r.Context(), user.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "list sessions after revoke", "user_id", user.ID, "error", err)
	}
	h.renderer.Partial(w, http.StatusOK, "partials/session-list.html", map[string]any{
		"Sessions": sessions,
		"UserID":   user.ID,
	})
}
