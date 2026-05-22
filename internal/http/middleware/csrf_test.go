package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/softsrv/starter/internal/http/middleware"
)

func TestCSRFMiddleware(t *testing.T) {
	t.Parallel()

	// secure=false is appropriate for tests running over plain HTTP.
	handler := middleware.CSRF(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("GET sets csrf cookie", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("got %d, want 200", rr.Code)
		}
		found := false
		for _, c := range rr.Result().Cookies() {
			if c.Name == "csrf_token" {
				found = true
			}
		}
		if !found {
			t.Error("expected csrf_token cookie to be set")
		}
	})

	t.Run("POST with valid token passes", func(t *testing.T) {
		t.Parallel()
		token := "validCSRFtoken123"
		form := url.Values{"csrf_token": {token}}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("got %d, want 200", rr.Code)
		}
	})

	t.Run("POST with missing token is forbidden", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "sometoken"})
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("got %d, want 403", rr.Code)
		}
	})

	t.Run("POST with mismatched token is forbidden", func(t *testing.T) {
		t.Parallel()
		form := url.Values{"csrf_token": {"wrong-token"}}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "correct-token"})
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("got %d, want 403", rr.Code)
		}
	})

	t.Run("POST with X-CSRF-Token header passes", func(t *testing.T) {
		t.Parallel()
		token := "htmxCSRFtoken"
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("X-CSRF-Token", token)
		req.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("got %d, want 200", rr.Code)
		}
	})
}
