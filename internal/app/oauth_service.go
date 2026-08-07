package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
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

// discordTokenExpiryBuffer treats a Discord access token as expired slightly
// before its real expiry, so a request never races a token that dies
// mid-flight against the Discord API.
const discordTokenExpiryBuffer = time.Minute

// Discord permission bits relevant to admin detection, per
// https://docs.discord.com/developers/topics/permissions#permissions-bitwise-permission-flags.
const (
	discordPermAdministrator = 0x8
	discordPermManageGuild   = 0x20
)

// GuildMembership is one Discord guild the user belongs to where our bot is
// also installed.
type GuildMembership struct {
	GuildID   string
	GuildName string
	IsAdmin   bool
}

// UserGuild is one Discord guild the user currently belongs to (per live
// Discord data, see GetDiscordGuildMemberships) that they either manage or
// have registered a result subscription in via
// DiscordService.RegisterFromInteraction — the set shown in the guild-detail
// page's server switcher.
type UserGuild struct {
	GuildID      string
	GuildName    string
	IsRegistered bool
	IsAdmin      bool
}

var ErrDiscordUnverifiedEmail = errors.New("discord account has an unverified email")
var ErrDiscordAlreadyLinkedToOtherUser = errors.New("this Discord account is already linked to a different RowBot account")
var ErrConcept2AlreadyLinkedToOtherUser = errors.New("this Concept2 account is already linked to a different RowBot account")

const discordProvider = "discord"
const concept2Provider = "concept2"

// OAuthService handles OAuth-based account linking and provisioning.
type OAuthService struct {
	q                    *db.Queries
	pool                 pgxBeginner
	discord              *oauth.DiscordClient
	concept2             *oauth.Concept2Client
	encrypter            *secrets.Encrypter
	authSvc              *AuthService
	guildMembershipCache *discordGuildMembershipCache
}

// NewOAuthService constructs an OAuthService. discord and concept2 may be nil
// when those providers are not configured. encrypter is required when concept2
// is non-nil (Concept2 tokens must be stored encrypted). ctx controls the
// lifetime of the guild-membership cache's background sweep goroutine; cancel
// it during application shutdown to cleanly stop the sweeper, the same as
// middleware.NewRateLimiter.
func NewOAuthService(
	ctx context.Context,
	q *db.Queries,
	pool pgxBeginner,
	discord *oauth.DiscordClient,
	concept2 *oauth.Concept2Client,
	encrypter *secrets.Encrypter,
	authSvc *AuthService,
) *OAuthService {
	cache := newDiscordGuildMembershipCache()
	go cache.sweep(ctx)
	return &OAuthService{
		q:                    q,
		pool:                 pool,
		discord:              discord,
		concept2:             concept2,
		encrypter:            encrypter,
		authSvc:              authSvc,
		guildMembershipCache: cache,
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

	userID, err := s.resolveDiscordAccount(ctx, discordUser, tok)
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
func (s *OAuthService) resolveDiscordAccount(ctx context.Context, discordUser oauth.DiscordUser, tok oauth.DiscordTokenResponse) (uuid.UUID, error) {
	// Branch 1: existing link
	identity, err := s.q.GetOAuthIdentityByProviderUserID(ctx, db.GetOAuthIdentityByProviderUserIDParams{
		Provider:       discordProvider,
		ProviderUserID: discordUser.ID,
	})
	if err == nil {
		// Storing the token is a bonus for the admin-guilds dashboard feature,
		// not core to login — never fail an otherwise-successful sign-in over it.
		if storeErr := s.storeEncryptedTokens(ctx, s.q, identity.ID, tok.AccessToken, tok.RefreshToken, tok.Scope, tok.ExpiresIn); storeErr != nil {
			slog.WarnContext(ctx, "oauth_service: store discord token failed", "user_id", identity.UserID, "error", storeErr)
		}
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
		newIdentity, linkErr := s.createOAuthIdentity(ctx, qtx2, existingUser.ID, discordProvider, discordUser.ID, discordUser.Username)
		if linkErr != nil {
			var pgErr *pgconn.PgError
			if errors.As(linkErr, &pgErr) && pgErr.Code == "23505" {
				// Another concurrent request already created the identity; treat as success.
				tx2.Rollback(ctx) //nolint:errcheck
				return existingUser.ID, nil
			}
			return uuid.Nil, linkErr
		}
		if storeErr := s.storeEncryptedTokens(ctx, qtx2, newIdentity.ID, tok.AccessToken, tok.RefreshToken, tok.Scope, tok.ExpiresIn); storeErr != nil {
			slog.WarnContext(ctx, "oauth_service: store discord token failed", "user_id", existingUser.ID, "error", storeErr)
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

	newIdentity, err := s.createOAuthIdentity(ctx, qtx, newUser.ID, discordProvider, discordUser.ID, discordUser.Username)
	if err != nil {
		return uuid.Nil, err
	}
	if storeErr := s.storeEncryptedTokens(ctx, qtx, newIdentity.ID, tok.AccessToken, tok.RefreshToken, tok.Scope, tok.ExpiresIn); storeErr != nil {
		slog.WarnContext(ctx, "oauth_service: store discord token failed", "user_id", newUser.ID, "error", storeErr)
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

	identity, err := s.createOAuthIdentity(ctx, s.q, userID, discordProvider, discordUser.ID, discordUser.Username)
	if err != nil {
		return err
	}
	// As in resolveDiscordAccount, token storage is a bonus for the
	// admin-guilds dashboard feature — never fail the link over it.
	if storeErr := s.storeEncryptedTokens(ctx, s.q, identity.ID, tok.AccessToken, tok.RefreshToken, tok.Scope, tok.ExpiresIn); storeErr != nil {
		slog.WarnContext(ctx, "oauth_service: store discord token failed", "user_id", userID, "error", storeErr)
	}
	return nil
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

	identity, err := s.createOAuthIdentity(ctx, qtx, userID, concept2Provider, providerUserID, c2User.DisplayName())
	if err != nil {
		return err
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

// RefreshDiscordToken fetches a fresh access token using the stored refresh
// token and updates the oauth_tokens row. Call this before any Discord API
// request when the stored token may be expired.
func (s *OAuthService) RefreshDiscordToken(ctx context.Context, userID uuid.UUID) error {
	identity, err := s.q.GetOAuthIdentityByUserAndProvider(ctx, db.GetOAuthIdentityByUserAndProviderParams{
		UserID:   userID,
		Provider: discordProvider,
	})
	if err != nil {
		return fmt.Errorf("discord identity not found: %w", err)
	}

	stored, err := s.q.GetOAuthTokenByIdentity(ctx, identity.ID)
	if err != nil {
		return fmt.Errorf("get discord token: %w", err)
	}

	if len(stored.RefreshTokenEnc) == 0 {
		return fmt.Errorf("discord refresh token not stored")
	}

	refreshPlain, err := s.encrypter.Decrypt(stored.RefreshTokenEnc)
	if err != nil {
		return fmt.Errorf("decrypt discord refresh token: %w", err)
	}

	tok, err := s.discord.RefreshToken(ctx, string(refreshPlain))
	if err != nil {
		return fmt.Errorf("discord token refresh: %w", err)
	}

	return s.storeEncryptedTokens(ctx, s.q, identity.ID, tok.AccessToken, tok.RefreshToken, tok.Scope, tok.ExpiresIn)
}

// discordGuildMembershipCacheTTL bounds how long a user's raw Discord guild
// list (which servers they belong to, and their permissions there) is cached
// before GetDiscordGuildMemberships re-fetches from Discord. Every
// dashboard/guild-detail page view calls it, and
// RegisterDiscordServer/UnregisterDiscordServer each call it again mid-flow
// before redirecting back into a page that calls it once more — so a single
// user interaction can otherwise cost 2-3 Discord API requests. Deliberately
// caches only that raw membership/permission data, not anything about which
// of those guilds have RowBot installed or which the user is registered
// in — those come from our own DB and are read fresh on every call — so
// adding the bot or registering never has to wait out a stale cache entry;
// only genuine Discord-side membership changes (join/leave a server,
// gain/lose admin) are subject to the TTL, visible within roughly one page
// reload, while comfortably absorbing the 2-3-call burst above and giving
// real headroom against Discord's per-route rate limits as usage grows.
const discordGuildMembershipCacheTTL = 90 * time.Second

// discordGuildMembershipCacheEntry holds one user's cached raw Discord guild
// list plus the time the entry stops being valid. A nil slice (the "no
// guilds" case) is a valid, cacheable value distinct from "not cached"
// (tracked via presence in the map, not by nil-ness of the slice).
type discordGuildMembershipCacheEntry struct {
	guilds    []oauth.DiscordGuild
	expiresAt time.Time
}

// discordGuildMembershipCache is an in-memory, per-user TTL cache of a
// user's raw Discord guild list (see GetDiscordGuildMemberships for what's
// deliberately excluded). It mirrors the sync.Map + TTL-per-entry +
// periodic sweep shape used by middleware.RateLimiter, for consistency with
// the rest of the codebase. Unlike the rate limiter (keyed by IP/request
// identity, potentially high cardinality), this cache is keyed by user ID,
// so it's naturally bounded by the active user count — the sweep goroutine
// here mainly reclaims entries left behind by users who load the dashboard
// once and never return, not a defense against unbounded growth.
type discordGuildMembershipCache struct {
	store sync.Map // uuid.UUID -> *discordGuildMembershipCacheEntry
}

func newDiscordGuildMembershipCache() *discordGuildMembershipCache {
	return &discordGuildMembershipCache{}
}

// get returns the cached raw guild list for userID and true if present and
// not yet expired.
func (c *discordGuildMembershipCache) get(userID uuid.UUID) ([]oauth.DiscordGuild, bool) {
	val, ok := c.store.Load(userID)
	if !ok {
		return nil, false
	}
	e := val.(*discordGuildMembershipCacheEntry)
	if time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.guilds, true
}

// set caches the raw guild list for userID for discordGuildMembershipCacheTTL.
func (c *discordGuildMembershipCache) set(userID uuid.UUID, guilds []oauth.DiscordGuild) {
	c.store.Store(userID, &discordGuildMembershipCacheEntry{
		guilds:    guilds,
		expiresAt: time.Now().Add(discordGuildMembershipCacheTTL),
	})
}

// sweep periodically removes expired entries. It exits when ctx is
// cancelled, which happens during application shutdown.
func (c *discordGuildMembershipCache) sweep(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			c.store.Range(func(k, v any) bool {
				if e, ok := v.(*discordGuildMembershipCacheEntry); ok && now.After(e.expiresAt) {
					c.store.Delete(k)
				}
				return true
			})
		}
	}
}

// GetDiscordGuildMemberships returns every Discord server userID belongs to
// where our bot is also installed, annotated with whether they administer it
// (owner, or holds Administrator/Manage Server) — enough for the dashboard to
// both nudge admins toward /register and offer members a self-service
// registration picker. This requires the user to have authenticated with the
// "guilds" scope (added alongside this feature); until they log in again, no
// oauth_tokens row will exist yet — an expected, common condition, not an
// error.
//
// Only the raw Discord fetch (discordRawGuildMemberships) goes through the
// cache; which of those guilds RowBot is actually installed in is looked up
// fresh from our own DB on every call, below, so adding the bot or
// registering into a server is reflected immediately regardless of the
// cache's age — no skipCache parameter or wizard-step-awareness needed here
// (an earlier version cached the already-bot-filtered list, which meant an
// action taken elsewhere on the site — installing the bot, registering —
// could still appear stale for up to the cache's TTL after the wizard
// finished; see the incident that prompted this).
func (s *OAuthService) GetDiscordGuildMemberships(ctx context.Context, userID uuid.UUID) ([]GuildMembership, error) {
	guilds, err := s.discordRawGuildMemberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	if guilds == nil {
		return nil, nil
	}

	botGuilds, err := s.q.ListDiscordGuilds(ctx)
	if err != nil {
		slog.WarnContext(ctx, "oauth_service: list discord guilds failed", "user_id", userID, "error", err)
		return nil, nil
	}
	botGuildIDs := make(map[string]struct{}, len(botGuilds))
	for _, g := range botGuilds {
		botGuildIDs[g.GuildID] = struct{}{}
	}

	memberships := make([]GuildMembership, 0, len(guilds))
	for _, g := range guilds {
		if _, botPresent := botGuildIDs[g.ID]; !botPresent {
			continue
		}
		memberships = append(memberships, GuildMembership{
			GuildID:   g.ID,
			GuildName: g.Name,
			IsAdmin:   isDiscordGuildAdmin(g),
		})
	}
	return memberships, nil
}

// discordRawGuildMemberships loads (refreshing if needed) the user's stored
// Discord token and fetches their full guild list from Discord — every
// server they belong to, with their permissions there, regardless of
// whether RowBot is installed in it. Cached (see discordGuildMembershipCache)
// since this is the actual Discord API call; the bot-installed filter is
// applied by the caller from fresh, uncached data. Any failure along the way
// (missing token, expired refresh token, decrypt failure, Discord API error)
// is logged and swallowed rather than propagated, since callers use this for
// supplementary dashboard info that must never break the page.
func (s *OAuthService) discordRawGuildMemberships(ctx context.Context, userID uuid.UUID) ([]oauth.DiscordGuild, error) {
	if cached, ok := s.guildMembershipCache.get(userID); ok {
		return cached, nil
	}

	guilds, err := s.fetchDiscordGuildMemberships(ctx, userID)
	if err != nil {
		// A genuine propagated error must not be cached — otherwise a
		// transient Discord/DB hiccup would appear "stuck" for the full TTL.
		return nil, err
	}
	s.guildMembershipCache.set(userID, guilds)
	return guilds, nil
}

// fetchDiscordGuildMemberships does the actual token lookup/refresh and
// Discord API call behind discordRawGuildMemberships, uncached.
func (s *OAuthService) fetchDiscordGuildMemberships(ctx context.Context, userID uuid.UUID) ([]oauth.DiscordGuild, error) {
	identity, err := s.q.GetOAuthIdentityByUserAndProvider(ctx, db.GetOAuthIdentityByUserAndProviderParams{
		UserID:   userID,
		Provider: discordProvider,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		slog.WarnContext(ctx, "oauth_service: lookup discord identity for guild memberships failed", "user_id", userID, "error", err)
		return nil, nil
	}

	stored, err := s.q.GetOAuthTokenByIdentity(ctx, identity.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		slog.WarnContext(ctx, "oauth_service: lookup discord token for guild memberships failed", "user_id", userID, "error", err)
		return nil, nil
	}

	if stored.ExpiresAt.Valid && time.Now().After(stored.ExpiresAt.Time.Add(-discordTokenExpiryBuffer)) {
		if err := s.RefreshDiscordToken(ctx, userID); err != nil {
			slog.WarnContext(ctx, "oauth_service: refresh discord token for guild memberships failed", "user_id", userID, "error", err)
			return nil, nil
		}
		stored, err = s.q.GetOAuthTokenByIdentity(ctx, identity.ID)
		if err != nil {
			slog.WarnContext(ctx, "oauth_service: reload discord token after refresh failed", "user_id", userID, "error", err)
			return nil, nil
		}
	}

	accessPlain, err := s.encrypter.Decrypt(stored.AccessTokenEnc)
	if err != nil {
		slog.WarnContext(ctx, "oauth_service: decrypt discord access token failed", "user_id", userID, "error", err)
		return nil, nil
	}

	guilds, err := s.discord.CurrentUserGuilds(ctx, string(accessPlain))
	if err != nil {
		slog.WarnContext(ctx, "oauth_service: fetch discord guilds failed", "user_id", userID, "error", err)
		return nil, nil
	}
	return guilds, nil
}

// isDiscordGuildAdmin reports whether the user is an admin of g: guild
// owner, or holds the Administrator or Manage Server permission bit.
func isDiscordGuildAdmin(g oauth.DiscordGuild) bool {
	if g.Owner {
		return true
	}
	return g.Permissions&discordPermAdministrator != 0 || g.Permissions&discordPermManageGuild != 0
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

func (s *OAuthService) createOAuthIdentity(ctx context.Context, q *db.Queries, userID uuid.UUID, provider, providerUserID, providerUsername string) (db.OauthIdentity, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return db.OauthIdentity{}, fmt.Errorf("generate identity id: %w", err)
	}
	identity, err := q.CreateOAuthIdentity(ctx, db.CreateOAuthIdentityParams{
		ID:               id,
		UserID:           userID,
		Provider:         provider,
		ProviderUserID:   providerUserID,
		ProviderUsername: pgtype.Text{String: providerUsername, Valid: providerUsername != ""},
	})
	if err != nil {
		return db.OauthIdentity{}, fmt.Errorf("create oauth identity: %w", err)
	}
	return identity, nil
}
