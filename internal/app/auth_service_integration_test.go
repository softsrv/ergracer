//go:build integration

package app_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/softsrv/rowbot/internal/app"
	"github.com/softsrv/rowbot/internal/auth"
	"github.com/softsrv/rowbot/internal/db"
)

// testDB connects to the database pointed to by TEST_DATABASE_URL.
// Run integration tests with: TEST_DATABASE_URL=... go test -tags integration ./internal/app/...
func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testAuthService(t *testing.T, pool *pgxpool.Pool) *app.AuthService {
	t.Helper()
	q := db.New(pool)
	return app.NewAuthService(q, pool, app.AuthServiceConfig{
		JWTSecret:      "integration-test-secret-32-bytes!!!",
		AccessExpiry:   15 * time.Minute,
		RefreshExpiry:  720 * time.Hour,
		BCryptCost:     12,
		PasswordMinLen: 8,
		AppBaseURL:     "http://localhost:8080",
		AppName:        "TestApp",
	})
}

// testUser creates a user directly via the OAuth-style no-password path
// (mirroring how every real account is now provisioned — Discord OAuth is the
// sole account-creation path) so Refresh/Logout can be exercised without
// AuthService.Register/Login, which no longer exist.
func testUser(t *testing.T, pool *pgxpool.Pool, email string) db.User {
	t.Helper()
	q := db.New(pool)
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7: %v", err)
	}
	user, err := q.CreateUserNoPassword(context.Background(), db.CreateUserNoPasswordParams{
		ID:    id,
		Email: email,
	})
	if err != nil {
		t.Fatalf("CreateUserNoPassword: %v", err)
	}
	return user
}

func TestIntegration_Refresh(t *testing.T) {
	pool := testDB(t)
	svc := testAuthService(t, pool)
	ctx := context.Background()

	user := testUser(t, pool, "refresh@example.com")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID)
	})

	result, err := svc.Refresh(ctx, mustIssueRefreshToken(t, pool, user.ID), app.DeviceMeta{})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Refresh returns a new access token but the same refresh token.
	result2, err := svc.Refresh(ctx, result.RefreshToken, app.DeviceMeta{})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result2.RefreshToken != result.RefreshToken {
		t.Error("refresh token must not change (no rotation)")
	}
	if result2.AccessToken == result.AccessToken {
		t.Error("access token must be newly issued on each refresh")
	}

	// Revoked token must be rejected.
	if err := svc.Logout(ctx, result.RefreshToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	_, err = svc.Refresh(ctx, result.RefreshToken, app.DeviceMeta{})
	if err != app.ErrTokenRevoked {
		t.Errorf("expected ErrTokenRevoked after logout, got %v", err)
	}
}

// mustIssueRefreshToken inserts a refresh token row directly and returns its
// raw (unhashed) value, letting tests exercise Refresh/Logout without going
// through a full OAuth login.
func mustIssueRefreshToken(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) string {
	t.Helper()
	q := db.New(pool)
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7: %v", err)
	}
	raw, hashed, err := auth.GenerateRefreshToken()
	if err != nil {
		t.Fatalf("generate refresh token: %v", err)
	}
	_, err = q.InsertRefreshToken(context.Background(), db.InsertRefreshTokenParams{
		ID:        id,
		UserID:    userID,
		TokenHash: hashed,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(720 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("InsertRefreshToken: %v", err)
	}
	return raw
}
