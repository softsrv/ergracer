package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/softsrv/starter/internal/app"
	"github.com/softsrv/starter/internal/db"
	"github.com/softsrv/starter/internal/http/middleware"
)

// authServicer defines the subset of app.AuthService that AuthHandler requires.
// Accepting an interface (rather than the concrete type) makes the handler
// independently testable without a real database or SMTP server.
type authServicer interface {
	Register(ctx context.Context, email, password string) (db.User, error)
	Login(ctx context.Context, email, password string, meta app.DeviceMeta) (app.TokenResult, error)
	Logout(ctx context.Context, rawRefreshToken string) error
	Refresh(ctx context.Context, rawRefreshToken string, meta app.DeviceMeta) (app.TokenResult, error)
	RequestPasswordReset(ctx context.Context, rawEmail string) error
	CompletePasswordReset(ctx context.Context, rawToken, newPassword string) error
	VerifyEmail(ctx context.Context, userID uuid.UUID, code string) error
	ResendVerification(ctx context.Context, userID uuid.UUID) error
}

// AuthHandler groups all authentication HTTP handlers.
type AuthHandler struct {
	auth     authServicer
	renderer *TemplateRenderer
	secure   bool // true in production (Secure cookie flag)
}

// NewAuthHandler constructs an AuthHandler.
func NewAuthHandler(authSvc authServicer, renderer *TemplateRenderer, secure bool) *AuthHandler {
	return &AuthHandler{auth: authSvc, renderer: renderer, secure: secure}
}

// ── Pages ─────────────────────────────────────────────────────────────────────

func (h *AuthHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	h.renderer.Page(w, http.StatusOK, "auth/login.html", map[string]any{
		"CSRFToken": middleware.CSRFTokenFromRequest(r),
	})
}

func (h *AuthHandler) RegisterPage(w http.ResponseWriter, r *http.Request) {
	h.renderer.Page(w, http.StatusOK, "auth/register.html", map[string]any{
		"CSRFToken": middleware.CSRFTokenFromRequest(r),
	})
}

func (h *AuthHandler) ForgotPasswordPage(w http.ResponseWriter, r *http.Request) {
	h.renderer.Page(w, http.StatusOK, "auth/forgot-password.html", map[string]any{
		"CSRFToken": middleware.CSRFTokenFromRequest(r),
	})
}

func (h *AuthHandler) ResetPasswordPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	h.renderer.Page(w, http.StatusOK, "auth/reset-password.html", map[string]any{
		"CSRFToken": middleware.CSRFTokenFromRequest(r),
		"Token":     token,
	})
}

func (h *AuthHandler) VerifyEmailPage(w http.ResponseWriter, r *http.Request) {
	h.renderer.Page(w, http.StatusOK, "auth/verify-email.html", map[string]any{
		"CSRFToken": middleware.CSRFTokenFromRequest(r),
	})
}

// ── Actions ───────────────────────────────────────────────────────────────────

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		slog.WarnContext(r.Context(), "register: parse form", "error", err)
		h.renderError(w, r, http.StatusBadRequest, "Invalid form data")
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")

	if _, err := h.auth.Register(r.Context(), email, password); err != nil {
		slog.WarnContext(r.Context(), "register failed", "error", err)
		h.renderError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// Issue tokens immediately after registration so the user is authenticated
	// when they reach /verify-email. POST /auth/verify-email is protected by
	// authMW and will reject unauthenticated requests.
	meta := deviceMeta(r)
	result, err := h.auth.Login(r.Context(), email, password, meta)
	if err != nil {
		// Registration succeeded — log the auto-login failure and redirect anyway.
		// The user will be prompted to log in on the verify-email page.
		slog.Error("auto-login after register", "error", err)
		htmxRedirect(w, "/verify-email")
		return
	}

	h.setTokenCookies(w, result)
	htmxRedirect(w, "/verify-email")
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		slog.WarnContext(r.Context(), "login: parse form", "error", err)
		h.renderError(w, r, http.StatusBadRequest, "Invalid form data")
		return
	}

	meta := deviceMeta(r)
	result, err := h.auth.Login(r.Context(), r.FormValue("email"), r.FormValue("password"), meta)
	if err != nil {
		slog.WarnContext(r.Context(), "login failed", "error", err)
		status := http.StatusUnauthorized
		if errors.Is(err, app.ErrAccountLocked) {
			status = http.StatusLocked
		}
		h.renderError(w, r, status, err.Error())
		return
	}

	h.setTokenCookies(w, result)
	htmxRedirect(w, "/dashboard")
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("refresh_token"); err == nil {
		if logoutErr := h.auth.Logout(r.Context(), cookie.Value); logoutErr != nil {
			slog.WarnContext(r.Context(), "logout: revoke refresh token", "error", logoutErr)
		}
	}
	h.clearTokenCookies(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("refresh_token")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	meta := deviceMeta(r)
	result, err := h.auth.Refresh(r.Context(), cookie.Value, meta)
	if err != nil {
		slog.WarnContext(r.Context(), "token refresh failed", "error", err)
		h.clearTokenCookies(w)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	h.setTokenCookies(w, result)
	w.WriteHeader(http.StatusOK)
}

func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		slog.WarnContext(r.Context(), "forgot password: parse form", "error", err)
		h.renderError(w, r, http.StatusBadRequest, "Invalid form data")
		return
	}

	// Error is intentionally not returned to the caller (prevents email enumeration),
	// but we log it so server-side failures don't go unnoticed.
	if err := h.auth.RequestPasswordReset(r.Context(), r.FormValue("email")); err != nil {
		slog.ErrorContext(r.Context(), "request password reset", "error", err)
	}

	h.renderer.Partial(w, http.StatusOK, "partials/flash.html", map[string]any{
		"Message": "If that email is registered, a reset link has been sent.",
	})
}

func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		slog.WarnContext(r.Context(), "reset password: parse form", "error", err)
		h.renderError(w, r, http.StatusBadRequest, "Invalid form data")
		return
	}

	token := r.FormValue("token")
	newPassword := r.FormValue("password")

	if err := h.auth.CompletePasswordReset(r.Context(), token, newPassword); err != nil {
		slog.WarnContext(r.Context(), "reset password failed", "error", err)
		status := http.StatusUnprocessableEntity
		if errors.Is(err, app.ErrTokenNotFound) || errors.Is(err, app.ErrTokenExpired) || errors.Is(err, app.ErrTokenUsed) {
			status = http.StatusBadRequest
		}
		h.renderError(w, r, status, err.Error())
		return
	}

	htmxRedirect(w, "/login")
}

func (h *AuthHandler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		slog.WarnContext(r.Context(), "verify email: parse form", "error", err)
		h.renderError(w, r, http.StatusBadRequest, "Invalid form data")
		return
	}

	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if err := h.auth.VerifyEmail(r.Context(), user.ID, r.FormValue("code")); err != nil {
		slog.WarnContext(r.Context(), "verify email failed", "user_id", user.ID, "error", err)
		h.renderError(w, r, http.StatusUnprocessableEntity, "Invalid or expired code")
		return
	}

	htmxRedirect(w, "/dashboard")
}

// ResendVerification issues a fresh verification code to the authenticated user.
func (h *AuthHandler) ResendVerification(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if err := h.auth.ResendVerification(r.Context(), user.ID); err != nil {
		if errors.Is(err, app.ErrEmailAlreadyVerified) {
			htmxRedirect(w, "/dashboard")
			return
		}
		slog.WarnContext(r.Context(), "resend verification failed", "user_id", user.ID, "error", err)
		h.renderError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}

	h.renderer.Partial(w, http.StatusOK, "partials/flash.html", map[string]any{
		"Message": "A new verification code has been sent to your email.",
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (h *AuthHandler) setTokenCookies(w http.ResponseWriter, result app.TokenResult) {
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    result.AccessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  result.AccessTokenExpiry,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    result.RefreshToken,
		Path:     "/auth/refresh",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  result.RefreshTokenExpiry,
	})
}

func (h *AuthHandler) clearTokenCookies(w http.ResponseWriter) {
	epoch := time.Unix(0, 0)
	// access_token was set on Path "/".
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  epoch,
		MaxAge:   -1,
	})
	// refresh_token was set on the narrower Path "/auth/refresh"; clearing must
	// use that same path, otherwise the browser treats them as different cookies
	// and the session cookie is never actually removed.
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/auth/refresh",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  epoch,
		MaxAge:   -1,
	})
}

func (h *AuthHandler) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	// HTMX 2.0 only processes 2xx responses for DOM swaps by default.
	// Downgrade to 200 for HTMX requests so the error partial is actually
	// swapped into the target element (e.g. #form-error).
	if r.Header.Get("HX-Request") == "true" {
		status = http.StatusOK
	}
	h.renderer.Partial(w, status, "partials/error.html", map[string]any{"Error": msg})
}

func htmxRedirect(w http.ResponseWriter, path string) {
	w.Header().Set("HX-Redirect", path)
	w.WriteHeader(http.StatusOK)
}

// deviceMeta extracts client IP and user-agent metadata from the request.
// When an X-Forwarded-For header is present it uses only the leftmost IP to
// avoid accepting spoofed values appended by the client.
func deviceMeta(r *http.Request) app.DeviceMeta {
	var addr *netip.Addr

	var ipStr string
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// X-Forwarded-For may be "client, proxy1, proxy2" — take the leftmost.
		first, _, _ := strings.Cut(fwd, ",")
		ipStr = strings.TrimSpace(first)
	} else {
		// RemoteAddr is "host:port"; strip the port.
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			ipStr = host
		} else {
			ipStr = r.RemoteAddr
		}
	}

	if parsed, err := netip.ParseAddr(ipStr); err == nil {
		addr = &parsed
	}

	return app.DeviceMeta{
		DeviceName: "", // could parse UA for friendly name
		IPAddress:  addr,
		UserAgent:  r.UserAgent(),
	}
}
