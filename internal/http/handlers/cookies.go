package handlers

import (
	"net/http"
	"time"

	"github.com/softsrv/rowbot/internal/app"
)

// legacyRefreshTokenPath was refresh_token's cookie Path before it moved to
// "/". clearLegacyRefreshTokenCookie expires any cookie a browser might still
// be holding at that old scope — see refreshTokenCandidates for why a
// lingering one is actively dangerous, not just untidy: browsers send the
// more specific Path first (RFC 6265), so it silently shadows the correct
// cookie on every /auth/* request, which is exactly where the refresh
// mechanism lives.
const legacyRefreshTokenPath = "/auth"

func setTokenCookies(w http.ResponseWriter, result app.TokenResult, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    result.AccessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  result.AccessTokenExpiry,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    result.RefreshToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  result.RefreshTokenExpiry,
	})
	clearLegacyRefreshTokenCookie(w, secure)
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
	clearLegacyRefreshTokenCookie(w, secure)
}

// clearLegacyRefreshTokenCookie expires the Path=/auth refresh_token cookie.
// Safe to send unconditionally — browsers that never had one just ignore it.
func clearLegacyRefreshTokenCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     legacyRefreshTokenPath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// refreshTokenCandidates returns every "refresh_token" cookie value present
// on the request, in the order the browser sent them — normally just one,
// but a browser can hold two simultaneously (see legacyRefreshTokenPath)
// until the cleanup above has had a chance to run. Trying every candidate
// rather than just the first (net/http's r.Cookie only ever returns the
// first match) means refresh still works immediately even before that
// cleanup takes effect, instead of failing once and self-healing only on the
// next attempt.
func refreshTokenCandidates(r *http.Request) []string {
	var vals []string
	for _, c := range r.Cookies() {
		if c.Name == "refresh_token" && c.Value != "" {
			vals = append(vals, c.Value)
		}
	}
	return vals
}
