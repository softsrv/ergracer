package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/softsrv/rowbot/internal/app"
)

// findCookie returns the first cookie matching name and path from cookies.
func findCookie(cookies []*http.Cookie, name, path string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name && c.Path == path {
			return c
		}
	}
	return nil
}

// TestSetTokenCookiesPath guards against regressing refresh_token back to a
// narrower Path than access_token. middleware.Authenticate's silent-refresh
// branch reads r.Cookie("refresh_token") on arbitrary protected routes (e.g.
// /dashboard) — if the browser never attaches that cookie there because its
// Path is scoped to /auth, the check always fails and users get a hard
// logout on every access-token expiry regardless of the refresh token's
// validity. Both real cookies must share Path "/" for that check to work.
// setTokenCookies also proactively clears any lingering Path=/auth
// refresh_token cookie from before that Path changed — see
// clearLegacyRefreshTokenCookie for why a stray one is actively dangerous,
// not just untidy.
func TestSetTokenCookiesPath(t *testing.T) {
	rec := httptest.NewRecorder()
	setTokenCookies(rec, app.TokenResult{
		AccessToken:        "at",
		AccessTokenExpiry:  time.Now().Add(time.Hour),
		RefreshToken:       "rt",
		RefreshTokenExpiry: time.Now().Add(24 * time.Hour),
	}, true)

	cookies := rec.Result().Cookies()

	if c := findCookie(cookies, "access_token", "/"); c == nil {
		t.Error("expected access_token cookie at Path=/")
	} else if c.Value != "at" {
		t.Errorf("access_token value = %q, want %q", c.Value, "at")
	}

	if c := findCookie(cookies, "refresh_token", "/"); c == nil {
		t.Error("expected refresh_token cookie at Path=/")
	} else if c.Value != "rt" {
		t.Errorf("refresh_token value = %q, want %q", c.Value, "rt")
	}

	if c := findCookie(cookies, "refresh_token", "/auth"); c == nil {
		t.Error("expected a legacy-clearing refresh_token cookie at Path=/auth")
	} else if c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("legacy refresh_token cookie not cleared: value=%q maxAge=%d", c.Value, c.MaxAge)
	}
}

func TestClearAuthCookiesPath(t *testing.T) {
	rec := httptest.NewRecorder()
	clearAuthCookies(rec, true)

	cookies := rec.Result().Cookies()

	if c := findCookie(cookies, "access_token", "/"); c == nil {
		t.Error("expected access_token cookie at Path=/")
	} else if c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("access_token cookie not cleared: value=%q maxAge=%d", c.Value, c.MaxAge)
	}

	if c := findCookie(cookies, "refresh_token", "/"); c == nil {
		t.Error("expected refresh_token cookie at Path=/")
	} else if c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("refresh_token cookie not cleared: value=%q maxAge=%d", c.Value, c.MaxAge)
	}

	if c := findCookie(cookies, "refresh_token", "/auth"); c == nil {
		t.Error("expected a legacy-clearing refresh_token cookie at Path=/auth")
	} else if c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("legacy refresh_token cookie not cleared: value=%q maxAge=%d", c.Value, c.MaxAge)
	}
}

// TestRefreshTokenCandidates confirms every "refresh_token" cookie on a
// request is collected, not just the first — the whole point being to
// survive a browser that's (still) holding both an old Path=/auth cookie and
// the current Path=/ one simultaneously.
func TestRefreshTokenCandidates(t *testing.T) {
	req := httptest.NewRequest("GET", "/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "stale-legacy-value"})
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "current-value"})
	req.AddCookie(&http.Cookie{Name: "access_token", Value: "irrelevant"})

	got := refreshTokenCandidates(req)
	want := []string{"stale-legacy-value", "current-value"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
