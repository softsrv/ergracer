package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/softsrv/rowbot/internal/auth"
	"github.com/softsrv/rowbot/internal/db"
	"github.com/softsrv/rowbot/internal/http/middleware"
)

const authTestSecret = "auth-test-secret-that-is-at-least-32-bytes!!"

// stubFetcher implements middleware.UserFetcher for testing.
type stubFetcher struct {
	user db.User
	err  error
}

func (s *stubFetcher) GetUserByID(_ context.Context, _ uuid.UUID) (db.User, error) {
	return s.user, s.err
}

func makeTestToken(t *testing.T, userID uuid.UUID, expiry time.Duration) string {
	t.Helper()
	tp, err := auth.IssueAccessToken(userID, "test@example.com", authTestSecret, expiry)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return tp.AccessToken
}

func TestAuthMiddleware(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	validUser := db.User{ID: userID, Email: "test@example.com"}

	protected := middleware.Authenticate(&stubFetcher{user: validUser}, authTestSecret)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, ok := middleware.UserFromContext(r.Context())
			if !ok {
				t.Error("user not found in context")
			}
			w.WriteHeader(http.StatusOK)
		}),
	)

	t.Run("valid cookie passes through", func(t *testing.T) {
		t.Parallel()
		token := makeTestToken(t, userID, 15*time.Minute)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "access_token", Value: token})
		rr := httptest.NewRecorder()
		protected.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("got %d, want 200", rr.Code)
		}
	})

	t.Run("valid bearer header passes through", func(t *testing.T) {
		t.Parallel()
		token := makeTestToken(t, userID, 15*time.Minute)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		protected.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("got %d, want 200", rr.Code)
		}
	})

	t.Run("missing token redirects", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		protected.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("got %d, want 303", rr.Code)
		}
	})

	// The access_token cookie's own Expires matches the JWT's exp, so once
	// that time passes most browsers stop sending it outright rather than
	// keep sending an expired one — meaning "no access_token cookie at all,
	// but a still-valid refresh_token" is the common real-world shape of an
	// expired session, not "access_token present but ValidateAccessToken
	// rejects it as expired". Both must get the same recovery attempt.
	t.Run("missing token with refresh cookie redirects to silent-refresh", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "rt-123"})
		rr := httptest.NewRecorder()
		protected.ServeHTTP(rr, req)
		if rr.Code != http.StatusFound {
			t.Errorf("got %d, want 302", rr.Code)
		}
		if got := rr.Header().Get("Location"); got != "/auth/silent-refresh?next=%2Fdashboard" {
			t.Errorf("redirect = %q, want silent-refresh with next=/dashboard", got)
		}
	})

	t.Run("missing token with refresh cookie sets HX-Trigger for HTMX requests", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "rt-123"})
		req.Header.Set("HX-Request", "true")
		rr := httptest.NewRecorder()
		protected.ServeHTTP(rr, req)
		if rr.Header().Get("HX-Trigger") != "token-expired" {
			t.Errorf("expected HX-Trigger: token-expired, got %q", rr.Header().Get("HX-Trigger"))
		}
		if got := rr.Header().Get("HX-Redirect"); got != "" {
			t.Errorf("expected no HX-Redirect alongside HX-Trigger: token-expired, got %q", got)
		}
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", rr.Code)
		}
	})

	t.Run("expired token sets HX-Trigger for HTMX requests", func(t *testing.T) {
		t.Parallel()
		token := makeTestToken(t, userID, -time.Second)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "access_token", Value: token})
		req.Header.Set("HX-Request", "true")
		rr := httptest.NewRecorder()
		protected.ServeHTTP(rr, req)
		if rr.Header().Get("HX-Trigger") != "token-expired" {
			t.Errorf("expected HX-Trigger: token-expired, got %q", rr.Header().Get("HX-Trigger"))
		}
		// Must NOT also set HX-Redirect: htmx processes both headers from the
		// same response in one pass, firing the trigger and then immediately
		// following the redirect — which would always beat app.js's async
		// silent-refresh attempt and send the user to "/" regardless of
		// whether the refresh would have succeeded.
		if got := rr.Header().Get("HX-Redirect"); got != "" {
			t.Errorf("expected no HX-Redirect alongside HX-Trigger: token-expired, got %q", got)
		}
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", rr.Code)
		}
	})
}
