package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/softsrv/rowbot/internal/auth"
	"github.com/softsrv/rowbot/internal/db"
)

type userContextKey struct{}

// UserFetcher is satisfied by *db.Queries.
type UserFetcher interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (db.User, error)
}

// Authenticate validates the JWT from either the cookie or Authorization header,
// loads the user, and attaches it to the request context.
func Authenticate(queries UserFetcher, jwtSecret string, secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := extractToken(r)

			var claims *auth.Claims
			if tokenStr != "" {
				var err error
				claims, err = auth.ValidateAccessToken(tokenStr, jwtSecret)
				if err != nil && !errors.Is(err, auth.ErrTokenExpired) {
					// Present but invalid for some other reason (bad signature,
					// malformed, wrong issuer) — never worth a refresh attempt.
					respondUnauthorized(w, r)
					return
				}
			}

			// claims is nil here for two different reasons, treated identically:
			// no access_token cookie was sent at all, or one was sent but
			// ValidateAccessToken rejected it as expired. In practice the first
			// case is the common one, not the second — setTokenCookies sets the
			// cookie's own Expires to the JWT's exp, so once that time passes
			// most browsers simply stop sending the cookie rather than keep
			// sending an expired one. Both cases get the same chance to recover
			// via the refresh token before giving up.
			if claims == nil {
				if r.Header.Get("HX-Request") == "" {
					// For full-page navigations, attempt a silent refresh instead of
					// sending the user to /login — the refresh token may still be valid.
					if _, cookieErr := r.Cookie("refresh_token"); cookieErr == nil {
						target := "/auth/silent-refresh?next=" + url.QueryEscape(r.URL.RequestURI())
						http.Redirect(w, r, target, http.StatusFound)
						return
					}
				} else {
					// For HTMX requests, fire the token-expired event and stop —
					// app.js's listener attempts a silent refresh and replays the
					// element that triggered this request. This must NOT also set
					// HX-Redirect (what respondUnauthorized below does): htmx
					// processes both headers from the same response in one pass,
					// firing the trigger and then immediately, synchronously,
					// following the redirect — which always wins the race against
					// that async refresh and sends the user to "/" regardless of
					// whether the refresh would have succeeded.
					w.Header().Set("HX-Trigger", "token-expired")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				respondUnauthorized(w, r)
				return
			}

			userID, err := uuid.Parse(claims.Subject)
			if err != nil {
				slog.WarnContext(r.Context(), "auth: invalid user id in token claims", "subject", claims.Subject, "error", err)
				respondUnauthorized(w, r)
				return
			}

			user, err := queries.GetUserByID(r.Context(), userID)
			if err != nil {
				slog.WarnContext(r.Context(), "auth: get user by id", "user_id", userID, "error", err)
				if errors.Is(err, pgx.ErrNoRows) {
					// The JWT is cryptographically valid and unexpired, but
					// the user row it points at is gone (deleted account, DB
					// reset). "/" only checks token validity, not user
					// existence, so an uncleared cookie here sends the
					// browser straight back to a protected route that just
					// bounces it back to "/" again — an infinite redirect
					// loop. Clearing it breaks that loop on the first hop.
					clearAuthCookies(w, secure)
				}
				respondUnauthorized(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey{}, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserFromContext retrieves the authenticated user from context.
func UserFromContext(ctx context.Context) (db.User, bool) {
	u, ok := ctx.Value(userContextKey{}).(db.User)
	return u, ok
}

// extractToken reads the JWT from the access_token cookie or Authorization header.
func extractToken(r *http.Request) string {
	if cookie, err := r.Cookie("access_token"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		return strings.TrimPrefix(hdr, "Bearer ")
	}
	return ""
}

// clearAuthCookies expires both auth cookies on the response.
func clearAuthCookies(w http.ResponseWriter, secure bool) {
	epoch := time.Unix(0, 0)
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  epoch,
		MaxAge:   -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  epoch,
		MaxAge:   -1,
	})
}

func respondUnauthorized(w http.ResponseWriter, r *http.Request) {
	// /login no longer exists, and /dashboard itself now requires a session —
	// so the landing page (with its Discord OAuth "Get Started" link) is the
	// sole sign-in entry point an unauthenticated visitor can be sent to.
	// For HTMX requests, redirect via header so the partial swap works.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
