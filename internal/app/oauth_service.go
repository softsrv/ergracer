package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/softsrv/rowbot/internal/db"
	"github.com/softsrv/rowbot/internal/oauth"
	"github.com/softsrv/rowbot/internal/secrets"
	"github.com/softsrv/rowbot/internal/users"
)

var ErrDiscordUnverifiedEmail = errors.New("discord account has an unverified email")
var ErrDiscordAlreadyLinkedToOtherUser = errors.New("this Discord account is already linked to a different RowBot account")
var ErrConcept2AlreadyLinkedToOtherUser = errors.New("this Concept2 account is already linked to a different RowBot account")

const discordProvider = "discord"
const concept2Provider = "concept2"

// OAuthService handles OAuth-based account linking and provisioning.
type OAuthService struct {
	q         *db.Queries
	pool      pgxBeginner
	discord   *oauth.DiscordClient
	concept2  *oauth.Concept2Client
	encrypter *secrets.Encrypter
	authSvc   *AuthService
}

// NewOAuthService constructs an OAuthService. discord and concept2 may be nil
// when those providers are not configured. encrypter is required when concept2
// is non-nil (Concept2 tokens must be stored encrypted).
func NewOAuthService(
	q *db.Queries,
	pool pgxBeginner,
	discord *oauth.DiscordClient,
	concept2 *oauth.Concept2Client,
	encrypter *secrets.Encrypter,
	authSvc *AuthService,
) *OAuthService {
	return &OAuthService{
		q:         q,
		pool:      pool,
		discord:   discord,
		concept2:  concept2,
		encrypter: encrypter,
		authSvc:   authSvc,
	}
}

// IsDiscordEnabled reports whether Discord is configured on this OAuthService.
func (s *OAuthService) IsDiscordEnabled() bool { return s.discord != nil }

// IsConcept2Enabled reports whether Concept2 is configured on this OAuthService.
func (s *OAuthService) IsConcept2Enabled() bool { return s.concept2 != nil }

// HandleDiscordCallback exchanges the OAuth code for an access token, fetches
// the Discord user profile, resolves or provisions a local account, and returns
// a token pair for the resolved user. The state parameter has already been
// validated by the handler before this call.
func (s *OAuthService) HandleDiscordCallback(ctx context.Context, code string, meta DeviceMeta) (TokenResult, error) {
	tok, err := s.discord.Exchange(ctx, code)
	if err != nil {
		return TokenResult{}, fmt.Errorf("discord exchange: %w", err)
	}

	discordUser, err := s.discord.CurrentUser(ctx, tok.AccessToken)
	if err != nil {
		return TokenResult{}, fmt.Errorf("discord user fetch: %w", err)
	}

	userID, err := s.resolveDiscordAccount(ctx, discordUser)
	if err != nil {
		return TokenResult{}, err
	}

	user, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return TokenResult{}, fmt.Errorf("get user: %w", err)
	}

	result, err := s.authSvc.issueTokenPair(ctx, user.ID, user.Email, meta)
	if err != nil {
		return TokenResult{}, err
	}
	result.EmailVerified = user.EmailVerified
	return result, nil
}

// resolveDiscordAccount implements the three-branch account resolution logic:
// 1. Existing Discord link → return that user's ID.
// 2. No link but email matches an existing verified user → auto-link and return.
// 3. Neither → provision a new user and link.
func (s *OAuthService) resolveDiscordAccount(ctx context.Context, discordUser oauth.DiscordUser) (uuid.UUID, error) {
	// Branch 1: existing link
	identity, err := s.q.GetOAuthIdentityByProviderUserID(ctx, db.GetOAuthIdentityByProviderUserIDParams{
		Provider:       discordProvider,
		ProviderUserID: discordUser.ID,
	})
	if err == nil {
		return identity.UserID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("lookup discord identity: %w", err)
	}

	// Branch 2: email match — wrap in a transaction so GetUserByEmail and
	// createOAuthIdentity are atomic. A concurrent DeleteAccount between the two
	// would otherwise make the insert fail with a foreign-key error, and a
	// concurrent second login would cause a unique-constraint violation.
	normalizedEmail := users.NormalizeEmail(discordUser.Email)
	existingUser, err := s.q.GetUserByEmail(ctx, normalizedEmail)
	if err == nil {
		if !discordUser.Verified {
			return uuid.Nil, ErrDiscordUnverifiedEmail
		}
		tx2, txErr := s.pool.Begin(ctx)
		if txErr != nil {
			return uuid.Nil, fmt.Errorf("begin transaction: %w", txErr)
		}
		defer tx2.Rollback(ctx) //nolint:errcheck

		qtx2 := s.q.WithTx(tx2)
		linkErr := s.createOAuthIdentity(ctx, qtx2, existingUser.ID, discordProvider, discordUser.ID, discordUser.Username)
		if linkErr != nil {
			var pgErr *pgconn.PgError
			if errors.As(linkErr, &pgErr) && pgErr.Code == "23505" {
				// Another concurrent request already created the identity; treat as success.
				tx2.Rollback(ctx) //nolint:errcheck
				return existingUser.ID, nil
			}
			return uuid.Nil, linkErr
		}
		if commitErr := tx2.Commit(ctx); commitErr != nil {
			return uuid.Nil, fmt.Errorf("commit transaction: %w", commitErr)
		}
		return existingUser.ID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("lookup user by email: %w", err)
	}

	// Branch 3: provision new user + link in a single transaction
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	qtx := s.q.WithTx(tx)

	newUserID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate user id: %w", err)
	}
	newUser, err := qtx.CreateUserNoPassword(ctx, db.CreateUserNoPasswordParams{
		ID:    newUserID,
		Email: normalizedEmail,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create user: %w", err)
	}

	if err := s.createOAuthIdentity(ctx, qtx, newUser.ID, discordProvider, discordUser.ID, discordUser.Username); err != nil {
		return uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit transaction: %w", err)
	}
	return newUser.ID, nil
}

// LinkDiscord exchanges an OAuth code and links the resulting Discord identity
// to the given user. Returns ErrDiscordAlreadyLinkedToOtherUser if the Discord
// account is already bound to a different local user.
func (s *OAuthService) LinkDiscord(ctx context.Context, userID uuid.UUID, code string) error {
	tok, err := s.discord.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("discord exchange: %w", err)
	}

	discordUser, err := s.discord.CurrentUser(ctx, tok.AccessToken)
	if err != nil {
		return fmt.Errorf("discord user fetch: %w", err)
	}

	existing, err := s.q.GetOAuthIdentityByProviderUserID(ctx, db.GetOAuthIdentityByProviderUserIDParams{
		Provider:       discordProvider,
		ProviderUserID: discordUser.ID,
	})
	if err == nil && existing.UserID != userID {
		return ErrDiscordAlreadyLinkedToOtherUser
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lookup discord identity: %w", err)
	}

	return s.createOAuthIdentity(ctx, s.q, userID, discordProvider, discordUser.ID, discordUser.Username)
}

// GetDiscordIdentity returns the Discord oauth identity for the given user.
func (s *OAuthService) GetDiscordIdentity(ctx context.Context, userID uuid.UUID) (db.OauthIdentity, error) {
	return s.q.GetOAuthIdentityByUserAndProvider(ctx, db.GetOAuthIdentityByUserAndProviderParams{
		UserID:   userID,
		Provider: discordProvider,
	})
}

// LinkConcept2 exchanges an OAuth code and links the resulting Concept2 identity
// to the given user. The access and refresh tokens are encrypted and stored in
// oauth_tokens for later API use.
func (s *OAuthService) LinkConcept2(ctx context.Context, userID uuid.UUID, code string) error {
	tok, err := s.concept2.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("concept2 exchange: %w", err)
	}

	c2User, err := s.concept2.CurrentUser(ctx, tok.AccessToken)
	if err != nil {
		return fmt.Errorf("concept2 user fetch: %w", err)
	}

	providerUserID := strconv.Itoa(c2User.ID)

	existing, err := s.q.GetOAuthIdentityByProviderUserID(ctx, db.GetOAuthIdentityByProviderUserIDParams{
		Provider:       concept2Provider,
		ProviderUserID: providerUserID,
	})
	if err == nil && existing.UserID != userID {
		return ErrConcept2AlreadyLinkedToOtherUser
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lookup concept2 identity: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	qtx := s.q.WithTx(tx)

	if err := s.createOAuthIdentity(ctx, qtx, userID, concept2Provider, providerUserID, c2User.DisplayName()); err != nil {
		return err
	}

	identity, err := qtx.GetOAuthIdentityByUserAndProvider(ctx, db.GetOAuthIdentityByUserAndProviderParams{
		UserID:   userID,
		Provider: concept2Provider,
	})
	if err != nil {
		return fmt.Errorf("get concept2 identity after create: %w", err)
	}

	if err := s.storeEncryptedTokens(ctx, qtx, identity.ID, tok.AccessToken, tok.RefreshToken, tok.Scope, tok.ExpiresIn); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// UnlinkConcept2 removes the Concept2 oauth_identities row for the given user.
// Concept2 is never a login method, so disconnecting it carries no lockout
// risk and is always allowed once an identity is found.
func (s *OAuthService) UnlinkConcept2(ctx context.Context, userID uuid.UUID) error {
	identity, err := s.q.GetOAuthIdentityByUserAndProvider(ctx, db.GetOAuthIdentityByUserAndProviderParams{
		UserID:   userID,
		Provider: concept2Provider,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lookup concept2 identity: %w", err)
	}

	return s.q.DeleteOAuthIdentity(ctx, identity.ID)
}

// GetConcept2Identity returns the Concept2 oauth identity for the given user.
func (s *OAuthService) GetConcept2Identity(ctx context.Context, userID uuid.UUID) (db.OauthIdentity, error) {
	return s.q.GetOAuthIdentityByUserAndProvider(ctx, db.GetOAuthIdentityByUserAndProviderParams{
		UserID:   userID,
		Provider: concept2Provider,
	})
}

// RefreshConcept2Token fetches a fresh access token using the stored refresh
// token and updates the oauth_tokens row. Call this before any Concept2 API
// request when the stored token may be expired.
func (s *OAuthService) RefreshConcept2Token(ctx context.Context, userID uuid.UUID) error {
	identity, err := s.q.GetOAuthIdentityByUserAndProvider(ctx, db.GetOAuthIdentityByUserAndProviderParams{
		UserID:   userID,
		Provider: concept2Provider,
	})
	if err != nil {
		return fmt.Errorf("concept2 identity not found: %w", err)
	}

	stored, err := s.q.GetOAuthTokenByIdentity(ctx, identity.ID)
	if err != nil {
		return fmt.Errorf("get concept2 token: %w", err)
	}

	if len(stored.RefreshTokenEnc) == 0 {
		return fmt.Errorf("concept2 refresh token not stored")
	}

	refreshPlain, err := s.encrypter.Decrypt(stored.RefreshTokenEnc)
	if err != nil {
		return fmt.Errorf("decrypt concept2 refresh token: %w", err)
	}

	tok, err := s.concept2.RefreshToken(ctx, string(refreshPlain))
	if err != nil {
		return fmt.Errorf("concept2 token refresh: %w", err)
	}

	return s.storeEncryptedTokens(ctx, s.q, identity.ID, tok.AccessToken, tok.RefreshToken, tok.Scope, tok.ExpiresIn)
}

// storeEncryptedTokens encrypts and upserts OAuth tokens for the given identity.
func (s *OAuthService) storeEncryptedTokens(ctx context.Context, q *db.Queries, identityID uuid.UUID, accessToken, refreshToken, scope string, expiresIn int) error {
	accessEnc, err := s.encrypter.Encrypt([]byte(accessToken))
	if err != nil {
		return fmt.Errorf("encrypt access token: %w", err)
	}

	var refreshEnc []byte
	if refreshToken != "" {
		refreshEnc, err = s.encrypter.Encrypt([]byte(refreshToken))
		if err != nil {
			return fmt.Errorf("encrypt refresh token: %w", err)
		}
	}

	tokenID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate token id: %w", err)
	}

	var expiresAt pgtype.Timestamptz
	if expiresIn > 0 {
		expiresAt = pgtype.Timestamptz{
			Time:  time.Now().Add(time.Duration(expiresIn) * time.Second),
			Valid: true,
		}
	}

	_, err = q.UpsertOAuthToken(ctx, db.UpsertOAuthTokenParams{
		ID:              tokenID,
		OauthIdentityID: identityID,
		AccessTokenEnc:  accessEnc,
		RefreshTokenEnc: refreshEnc,
		Scope:           pgtype.Text{String: scope, Valid: scope != ""},
		ExpiresAt:       expiresAt,
	})
	return err
}

func (s *OAuthService) createOAuthIdentity(ctx context.Context, q *db.Queries, userID uuid.UUID, provider, providerUserID, providerUsername string) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate identity id: %w", err)
	}
	_, err = q.CreateOAuthIdentity(ctx, db.CreateOAuthIdentityParams{
		ID:               id,
		UserID:           userID,
		Provider:         provider,
		ProviderUserID:   providerUserID,
		ProviderUsername: pgtype.Text{String: providerUsername, Valid: providerUsername != ""},
	})
	if err != nil {
		return fmt.Errorf("create oauth identity: %w", err)
	}
	return nil
}
