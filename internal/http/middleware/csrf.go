package middleware

import (
	"log/slog"
	"net/http"

	"github.com/softsrv/starter/internal/auth"
)

const (
	csrfCookieName = "csrf_token"
	csrfFieldName  = "csrf_token"
	csrfHeaderName = "X-CSRF-Token"
)

// CSRF returns middleware that generates and validates CSRF tokens.
// Pass secure=true in production so the CSRF cookie carries the Secure flag.
//
// On safe methods (GET, HEAD, OPTIONS) it ensures a CSRF cookie exists.
// On state-changing methods it validates the submitted token against the cookie.
func CSRF(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				// Ensure a CSRF cookie exists.
				if _, err := r.Cookie(csrfCookieName); err != nil {
					token, genErr := auth.GenerateCSRFToken()
					if genErr != nil {
						slog.ErrorContext(r.Context(), "csrf: generate token", "error", genErr)
					} else {
						http.SetCookie(w, &http.Cookie{
							Name:     csrfCookieName,
							Value:    token,
							Path:     "/",
							HttpOnly: false, // must be readable by JS for HTMX header injection
							Secure:   secure,
							SameSite: http.SameSiteStrictMode,
						})
					}
				}
				next.ServeHTTP(w, r)

			default:
				// Validate on all state-changing methods.
				cookie, err := r.Cookie(csrfCookieName)
				if err != nil {
					slog.WarnContext(r.Context(), "csrf: cookie missing", "method", r.Method, "path", r.URL.Path)
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}

				// Accept token from form field or custom header (HTMX).
				submitted := r.FormValue(csrfFieldName)
				if submitted == "" {
					submitted = r.Header.Get(csrfHeaderName)
				}

				if validateErr := auth.ValidateCSRFToken(submitted, cookie.Value); validateErr != nil {
					slog.WarnContext(r.Context(), "csrf: validation failed", "method", r.Method, "path", r.URL.Path)
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
			}
		})
	}
}

// CSRFTokenFromRequest returns the current CSRF token from the request cookie.
// Templates call this to inject the value into hidden form fields.
func CSRFTokenFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}
