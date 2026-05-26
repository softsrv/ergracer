package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/softsrv/starter/internal/auth"
	"github.com/softsrv/starter/internal/db"
	"github.com/softsrv/starter/internal/email"
	"github.com/softsrv/starter/internal/users"
)

const (
	maxFailedLoginAttempts = 10
	lockDuration           = time.Hour
)

// DeviceMeta holds metadata captured from the HTTP request.
type DeviceMeta struct {
	DeviceName string
	IPAddress  *netip.Addr
	UserAgent  string
}

// TokenResult is returned after a successful login or token refresh.
type TokenResult struct {
	AccessToken        string
	AccessTokenExpiry  time.Time
	RefreshToken       string
	RefreshTokenExpiry time.Time
}

// pgxBeginner is satisfied by *pgxpool.Pool.
type pgxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// AuthServiceConfig holds the configuration for AuthService.
// Using a config struct instead of positional parameters makes call sites
// self-documenting and prevents accidental argument transposition.
type AuthServiceConfig struct {
	JWTSecret      string
	AccessExpiry   time.Duration
	RefreshExpiry  time.Duration
	BCryptCost     int
	PasswordMinLen int
	AppBaseURL     string
	AppName        string
}

// AuthService handles all authentication business logic.
type AuthService struct {
	q      *db.Queries
	pool   pgxBeginner
	mailer email.Mailer
	cfg    AuthServiceConfig

	// wg tracks background email goroutines so Shutdown can drain them cleanly.
	wg sync.WaitGroup
}

// NewAuthService constructs an AuthService with all dependencies injected.
func NewAuthService(q *db.Queries, pool pgxBeginner, mailer email.Mailer, cfg AuthServiceConfig) *AuthService {
	return &AuthService{
		q:      q,
		pool:   pool,
		mailer: mailer,
		cfg:    cfg,
	}
}

// Shutdown blocks until all in-flight background email goroutines have finished.
// Call this during application shutdown, after the HTTP server has stopped
// accepting new requests, to avoid cutting off in-progress email deliveries.
func (s *AuthService) Shutdown() {
	s.wg.Wait()
}

// goSend runs fn in a tracked background goroutine. The WaitGroup is incremented
// before launching so that Shutdown() can drain all outstanding sends.
func (s *AuthService) goSend(fn func()) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		fn()
	}()
}

// Register creates a new user account and sends an email verification code.
func (s *AuthService) Register(ctx context.Context, rawEmail, password string) (db.User, error) {
	normalizedEmail := users.NormalizeEmail(rawEmail)
	if err := users.ValidateEmail(normalizedEmail); err != nil {
		return db.User{}, err
	}
	if err := users.ValidatePassword(password, s.cfg.PasswordMinLen); err != nil {
		return db.User{}, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cfg.BCryptCost)
	if err != nil {
		return db.User{}, fmt.Errorf("hash password: %w", err)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return db.User{}, fmt.Errorf("generate user id: %w", err)
	}

	user, err := s.q.CreateUser(ctx, db.CreateUserParams{
		ID:           id,
		Email:        normalizedEmail,
		PasswordHash: string(hash),
	})
	if err != nil {
		return db.User{}, fmt.Errorf("create user: %w", err)
	}

	s.goSend(func() {
		if sendErr := s.sendVerificationCode(context.Background(), user); sendErr != nil {
			slog.Error("send verification code", "error", sendErr, "user_id", user.ID)
		}
	})

	return user, nil
}

// Login authenticates a user and returns a token pair.
func (s *AuthService) Login(ctx context.Context, rawEmail, password string, meta DeviceMeta) (TokenResult, error) {
	normalizedEmail := users.NormalizeEmail(rawEmail)

	user, err := s.q.GetUserByEmail(ctx, normalizedEmail)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TokenResult{}, ErrInvalidCredentials
		}
		return TokenResult{}, fmt.Errorf("get user: %w", err)
	}

	// Check account lock before anything else.
	if user.LockedUntil.Valid && user.LockedUntil.Time.After(time.Now()) {
		return TokenResult{}, ErrAccountLocked
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		if incrErr := s.q.IncrementFailedLoginAttempts(ctx, user.ID); incrErr != nil {
			slog.Error("increment failed login attempts", "user_id", user.ID, "error", incrErr)
		}
		if user.FailedLoginAttempts+1 >= maxFailedLoginAttempts {
			lockedUntil := time.Now().Add(lockDuration)
			if lockErr := s.q.LockAccount(ctx, db.LockAccountParams{
				ID:          user.ID,
				LockedUntil: pgtype.Timestamptz{Time: lockedUntil, Valid: true},
			}); lockErr != nil {
				slog.Error("lock account", "user_id", user.ID, "error", lockErr)
			}
		}
		return TokenResult{}, ErrInvalidCredentials
	}

	if err := s.q.ResetLoginAttempts(ctx, user.ID); err != nil {
		slog.Error("reset login attempts", "error", err, "user_id", user.ID)
	}

	return s.issueTokenPair(ctx, user.ID, user.Email, uuid.Nil, nil, meta)
}

// Logout revokes the given raw refresh token.
func (s *AuthService) Logout(ctx context.Context, rawRefreshToken string) error {
	hash := auth.HashToken(rawRefreshToken)
	rt, err := s.q.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("get refresh token: %w", err)
	}
	return s.q.RevokeRefreshToken(ctx, rt.ID)
}

// Refresh validates a raw refresh token, rotates it, and returns a new token pair.
func (s *AuthService) Refresh(ctx context.Context, rawRefreshToken string, meta DeviceMeta) (TokenResult, error) {
	hash := auth.HashToken(rawRefreshToken)

	rt, err := s.q.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TokenResult{}, ErrTokenNotFound
		}
		return TokenResult{}, fmt.Errorf("get refresh token: %w", err)
	}

	// Theft detection: already-revoked token reuse means family is compromised.
	if rt.RevokedAt.Valid {
		slog.Warn("refresh token reuse detected — revoking family",
			"token_id", rt.ID, "token_family", rt.TokenFamily)
		if revokeErr := s.q.RevokeTokenFamily(ctx, rt.TokenFamily); revokeErr != nil {
			slog.Error("revoke token family", "token_family", rt.TokenFamily, "error", revokeErr)
		}
		return TokenResult{}, ErrTokenRevoked
	}

	if rt.ExpiresAt.Time.Before(time.Now()) {
		return TokenResult{}, ErrTokenExpired
	}

	user, err := s.q.GetUserByID(ctx, rt.UserID)
	if err != nil {
		return TokenResult{}, fmt.Errorf("get user: %w", err)
	}

	prevID := rt.ID
	return s.issueTokenPair(ctx, user.ID, user.Email, rt.TokenFamily, &prevID, meta)
}

// RequestPasswordReset sends a reset email if the address exists.
// Always returns nil to prevent email enumeration.
func (s *AuthService) RequestPasswordReset(ctx context.Context, rawEmail string) error {
	normalizedEmail := users.NormalizeEmail(rawEmail)

	user, err := s.q.GetUserByEmail(ctx, normalizedEmail)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("get user: %w", err)
	}

	count, err := s.q.CountRecentPasswordResetsByEmail(ctx, normalizedEmail)
	if err != nil {
		return fmt.Errorf("count resets: %w", err)
	}
	if count >= 3 {
		return nil
	}

	rawToken, hashedToken, err := auth.GenerateResetToken()
	if err != nil {
		return fmt.Errorf("generate reset token: %w", err)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate token id: %w", err)
	}
	_, err = s.q.InsertPasswordResetToken(ctx, db.InsertPasswordResetTokenParams{
		ID:        id,
		UserID:    user.ID,
		TokenHash: hashedToken,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("insert reset token: %w", err)
	}

	resetURL := fmt.Sprintf("%s/reset-password?token=%s", s.cfg.AppBaseURL, rawToken)
	subj, html, text := email.PasswordResetEmail(s.cfg.AppName, resetURL)
	s.goSend(func() {
		if sendErr := s.mailer.Send(normalizedEmail, subj, html, text); sendErr != nil {
			slog.Error("send reset email", "error", sendErr, "user_id", user.ID)
		}
	})

	return nil
}

// CompletePasswordReset validates the token and updates the user's password.
func (s *AuthService) CompletePasswordReset(ctx context.Context, rawToken, newPassword string) error {
	if err := users.ValidatePassword(newPassword, s.cfg.PasswordMinLen); err != nil {
		return err
	}

	hashedToken := auth.HashToken(rawToken)
	prt, err := s.q.GetPasswordResetTokenByHash(ctx, hashedToken)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTokenNotFound
		}
		return fmt.Errorf("get reset token: %w", err)
	}
	if prt.UsedAt.Valid {
		return ErrTokenUsed
	}
	if prt.ExpiresAt.Time.Before(time.Now()) {
		return ErrTokenExpired
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.cfg.BCryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	qtx := s.q.WithTx(tx)
	if err := qtx.UpdatePasswordHash(ctx, db.UpdatePasswordHashParams{
		ID:           prt.UserID,
		PasswordHash: string(newHash),
	}); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if err := qtx.MarkPasswordResetTokenUsed(ctx, prt.ID); err != nil {
		return fmt.Errorf("mark token used: %w", err)
	}
	if err := qtx.RevokeAllUserRefreshTokens(ctx, prt.UserID); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	user, userErr := s.q.GetUserByID(ctx, prt.UserID)
	if userErr != nil {
		slog.Error("get user after password reset", "user_id", prt.UserID, "error", userErr)
	} else {
		subj, html, text := email.PasswordChangedEmail(s.cfg.AppName)
		s.goSend(func() {
			if sendErr := s.mailer.Send(user.Email, subj, html, text); sendErr != nil {
				slog.Error("send password changed email", "error", sendErr, "user_id", user.ID)
			}
		})
	}

	return nil
}

// VerifyEmail validates a 6-digit code for the given user.
func (s *AuthService) VerifyEmail(ctx context.Context, userID uuid.UUID, code string) error {
	record, err := s.q.GetLatestUnusedVerificationCode(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTokenNotFound
		}
		return fmt.Errorf("get verification code: %w", err)
	}

	// Constant-time comparison of the submitted code against the stored plaintext code.
	if len(code) != len(record.Code) {
		return ErrTokenInvalid
	}
	var diff byte
	for i := range code {
		diff |= code[i] ^ record.Code[i]
	}
	if diff != 0 {
		return ErrTokenInvalid
	}

	if err := s.q.MarkVerificationCodeUsed(ctx, record.ID); err != nil {
		return fmt.Errorf("mark code used: %w", err)
	}
	return s.q.SetEmailVerified(ctx, userID)
}

// ResendVerification issues a fresh verification code if rate limit not exceeded.
func (s *AuthService) ResendVerification(ctx context.Context, userID uuid.UUID) error {
	user, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	if user.EmailVerified {
		return ErrEmailAlreadyVerified
	}

	count, err := s.q.CountRecentVerificationCodesByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("count codes: %w", err)
	}
	if count >= 3 {
		return ErrRateLimited
	}

	return s.sendVerificationCode(ctx, user)
}

// ── private helpers ───────────────────────────────────────────────────────────

func (s *AuthService) issueTokenPair(
	ctx context.Context,
	userID uuid.UUID,
	userEmail string,
	existingFamily uuid.UUID,
	previousTokenID *uuid.UUID,
	meta DeviceMeta,
) (TokenResult, error) {
	tp, err := auth.IssueAccessToken(userID, userEmail, s.cfg.JWTSecret, s.cfg.AccessExpiry)
	if err != nil {
		return TokenResult{}, fmt.Errorf("issue access token: %w", err)
	}

	rawRefresh, hashedRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		return TokenResult{}, fmt.Errorf("generate refresh token: %w", err)
	}

	tokenFamily := existingFamily
	if tokenFamily == uuid.Nil {
		tokenFamily, err = uuid.NewV7()
		if err != nil {
			return TokenResult{}, fmt.Errorf("generate token family: %w", err)
		}
	}

	newID, err := uuid.NewV7()
	if err != nil {
		return TokenResult{}, fmt.Errorf("generate token id: %w", err)
	}
	expiresAt := time.Now().Add(s.cfg.RefreshExpiry)

	// Convert optional fields to pgtype nullable wrappers.
	prevTokenID := pgtype.UUID{}
	if previousTokenID != nil {
		prevTokenID = pgtype.UUID{Bytes: *previousTokenID, Valid: true}
	}
	deviceName := pgtype.Text{String: meta.DeviceName, Valid: meta.DeviceName != ""}
	userAgent := pgtype.Text{String: meta.UserAgent, Valid: meta.UserAgent != ""}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TokenResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	qtx := s.q.WithTx(tx)

	_, err = qtx.InsertRefreshToken(ctx, db.InsertRefreshTokenParams{
		ID:              newID,
		UserID:          userID,
		TokenHash:       hashedRefresh,
		TokenFamily:     tokenFamily,
		PreviousTokenID: prevTokenID,
		DeviceName:      deviceName,
		IpAddress:       meta.IPAddress,
		UserAgent:       userAgent,
		ExpiresAt:       pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return TokenResult{}, fmt.Errorf("insert refresh token: %w", err)
	}

	if previousTokenID != nil {
		if err := qtx.RevokeRefreshToken(ctx, *previousTokenID); err != nil {
			return TokenResult{}, fmt.Errorf("revoke old refresh token: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return TokenResult{}, fmt.Errorf("commit: %w", err)
	}

	return TokenResult{
		AccessToken:        tp.AccessToken,
		AccessTokenExpiry:  tp.ExpiresAt,
		RefreshToken:       rawRefresh,
		RefreshTokenExpiry: expiresAt,
	}, nil
}

func (s *AuthService) sendVerificationCode(ctx context.Context, user db.User) error {
	code, err := auth.GenerateVerificationCode()
	if err != nil {
		return err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate code id: %w", err)
	}
	_, err = s.q.InsertEmailVerificationCode(ctx, db.InsertEmailVerificationCodeParams{
		ID:        id,
		UserID:    user.ID,
		Code:      code,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(15 * time.Minute), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("insert verification code: %w", err)
	}

	subj, htmlBody, text := email.VerificationEmail(s.cfg.AppName, code)
	return s.mailer.Send(user.Email, subj, htmlBody, text)
}
