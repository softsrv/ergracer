package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/softsrv/rowbot/internal/app"
	"github.com/softsrv/rowbot/internal/http/handlers"
)

// stubAuthService implements the (unexported) authServicer interface that
// AuthHandler depends on. Each behavior is a function field so individual tests
// can inject exactly the outcome they need.
type stubAuthService struct {
	logoutFn  func(context.Context, string) error
	refreshFn func(context.Context, string, app.DeviceMeta) (app.TokenResult, error)
}

func (s *stubAuthService) Logout(ctx context.Context, raw string) error {
	if s.logoutFn != nil {
		return s.logoutFn(ctx, raw)
	}
	return nil
}

func (s *stubAuthService) Refresh(ctx context.Context, raw string, m app.DeviceMeta) (app.TokenResult, error) {
	if s.refreshFn != nil {
		return s.refreshFn(ctx, raw, m)
	}
	return app.TokenResult{}, nil
}

func okTokens(verified bool) app.TokenResult {
	return app.TokenResult{
		AccessToken:        "access-token-value",
		AccessTokenExpiry:  time.Now().Add(15 * time.Minute),
		RefreshToken:       "refresh-token-value",
		RefreshTokenExpiry: time.Now().Add(720 * time.Hour),
		EmailVerified:      verified,
	}
}

func cookieByName(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestLogout_ClearsCookiesAndRevokes(t *testing.T) {
	t.Parallel()
	var revoked string
	stub := &stubAuthService{
		logoutFn: func(_ context.Context, raw string) error { revoked = raw; return nil },
	}
	h := handlers.NewAuthHandler(stub, newTestRenderer(t), false, 0)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "rt-123"})
	rr := httptest.NewRecorder()
	h.Logout(rr, req)

	if revoked != "rt-123" {
		t.Errorf("Logout called with %q, want rt-123", revoked)
	}
	if rr.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", rr.Code)
	}
	if got := rr.Header().Get("Location"); got != "/dashboard" {
		t.Errorf("redirect = %q, want /dashboard", got)
	}
	if c := cookieByName(rr.Result().Cookies(), "access_token"); c == nil || c.MaxAge >= 0 {
		t.Error("access_token cookie not expired on logout")
	}
}

func TestRefresh_MissingCookieReturns401(t *testing.T) {
	t.Parallel()
	h := handlers.NewAuthHandler(&stubAuthService{}, newTestRenderer(t), false, 0)

	rr := httptest.NewRecorder()
	h.Refresh(rr, httptest.NewRequest(http.MethodPost, "/auth/refresh", nil))

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestRefresh_SuccessSetsCookies(t *testing.T) {
	t.Parallel()
	stub := &stubAuthService{
		refreshFn: func(context.Context, string, app.DeviceMeta) (app.TokenResult, error) {
			return okTokens(true), nil
		},
	}
	h := handlers.NewAuthHandler(stub, newTestRenderer(t), false, 0)

	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "rt-123"})
	rr := httptest.NewRecorder()
	h.Refresh(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if cookieByName(rr.Result().Cookies(), "access_token") == nil {
		t.Error("access_token cookie not refreshed")
	}
}

func TestSilentRefresh_MissingCookieRedirectsToDashboard(t *testing.T) {
	t.Parallel()
	h := handlers.NewAuthHandler(&stubAuthService{}, newTestRenderer(t), false, 0)

	rr := httptest.NewRecorder()
	h.SilentRefresh(rr, httptest.NewRequest(http.MethodGet, "/auth/silent-refresh", nil))

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", rr.Code)
	}
	if got := rr.Header().Get("Location"); got != "/dashboard" {
		t.Errorf("redirect = %q, want /dashboard", got)
	}
}

func TestSilentRefresh_FailedRefreshRedirectsToDashboard(t *testing.T) {
	t.Parallel()
	stub := &stubAuthService{
		refreshFn: func(context.Context, string, app.DeviceMeta) (app.TokenResult, error) {
			return app.TokenResult{}, app.ErrTokenNotFound
		},
	}
	h := handlers.NewAuthHandler(stub, newTestRenderer(t), false, 0)

	req := httptest.NewRequest(http.MethodGet, "/auth/silent-refresh", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "rt-123"})
	rr := httptest.NewRecorder()
	h.SilentRefresh(rr, req)

	if got := rr.Header().Get("Location"); got != "/dashboard" {
		t.Errorf("redirect = %q, want /dashboard", got)
	}
}

// TestSilentRefresh_FallsBackWhenFirstCookieIsStale reproduces a browser
// still holding two "refresh_token" cookies at once — a stale one left over
// from before the cookie's Path changed from /auth to /, alongside the
// current one. RFC 6265 sends the more specific path first, so this is the
// cookie order net/http actually hands the handler; the fix must succeed
// using the second (current) value rather than failing outright on the
// first (stale) one.
func TestSilentRefresh_FallsBackWhenFirstCookieIsStale(t *testing.T) {
	t.Parallel()
	var triedValues []string
	stub := &stubAuthService{
		refreshFn: func(_ context.Context, raw string, _ app.DeviceMeta) (app.TokenResult, error) {
			triedValues = append(triedValues, raw)
			if raw == "stale-legacy-value" {
				return app.TokenResult{}, app.ErrTokenNotFound
			}
			return okTokens(true), nil
		},
	}
	h := handlers.NewAuthHandler(stub, newTestRenderer(t), false, 0)

	req := httptest.NewRequest(http.MethodGet, "/auth/silent-refresh?next=/profile", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "stale-legacy-value"})
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "current-value"})
	rr := httptest.NewRecorder()
	h.SilentRefresh(rr, req)

	if got := rr.Header().Get("Location"); got != "/profile" {
		t.Errorf("redirect = %q, want /profile (i.e. it succeeded)", got)
	}
	if cookieByName(rr.Result().Cookies(), "access_token") == nil {
		t.Error("access_token cookie not set — refresh did not actually succeed")
	}
	want := []string{"stale-legacy-value", "current-value"}
	if len(triedValues) != len(want) || triedValues[0] != want[0] || triedValues[1] != want[1] {
		t.Errorf("tried values = %v, want %v (both, in order)", triedValues, want)
	}
}

func TestSilentRefresh_SuccessRedirectsToNext(t *testing.T) {
	t.Parallel()
	stub := &stubAuthService{
		refreshFn: func(context.Context, string, app.DeviceMeta) (app.TokenResult, error) {
			return okTokens(true), nil
		},
	}
	h := handlers.NewAuthHandler(stub, newTestRenderer(t), false, 0)

	req := httptest.NewRequest(http.MethodGet, "/auth/silent-refresh?next=/profile", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "rt-123"})
	rr := httptest.NewRecorder()
	h.SilentRefresh(rr, req)

	if got := rr.Header().Get("Location"); got != "/profile" {
		t.Errorf("redirect = %q, want /profile", got)
	}
	if cookieByName(rr.Result().Cookies(), "access_token") == nil {
		t.Error("access_token cookie not set")
	}
}
