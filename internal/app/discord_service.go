package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/softsrv/ergracer/internal/db"
)

// ErrMissingGuildChannel is returned by SetChannel when any required identifier
// (guild ID, channel ID, or user ID) is empty.
var ErrMissingGuildChannel = errors.New("guild_id, channel_id, and set_by_user_id are required")

// DiscordService handles business logic for Discord bot interactions.
// It is independent of the OAuth flow: a Discord user who runs a slash
// command need not have a site account.
type DiscordService struct {
	q *db.Queries
}

// NewDiscordService constructs a DiscordService.
func NewDiscordService(q *db.Queries) *DiscordService {
	return &DiscordService{q: q}
}

// RegisterFromInteraction records (or updates) a Discord user's registration
// from a slash command. The record is keyed on (discordUserID, guildID), so
// the same user registering from two different servers produces two rows, and
// a repeated /register in the same server is an idempotent upsert that refreshes
// the display name and guild name.
func (s *DiscordService) RegisterFromInteraction(ctx context.Context, discordUserID, discordUsername, guildID, guildName string) (db.DiscordRegistration, error) {
	// Look up whether this Discord user has already linked a site account via
	// OAuth. We fail open: a DB error here must not prevent the registration.
	var linkedUserID pgtype.UUID
	identity, err := s.q.GetOAuthIdentityByProviderUserID(ctx, db.GetOAuthIdentityByProviderUserIDParams{
		Provider:       "discord",
		ProviderUserID: discordUserID,
	})
	switch {
	case err == nil:
		// Identity found — prefer the authoritative stored username.
		if identity.ProviderUsername.Valid {
			discordUsername = identity.ProviderUsername.String
		}
		linkedUserID = pgtype.UUID{Bytes: identity.UserID, Valid: true}
	case err == pgx.ErrNoRows:
		// No linked account yet; proceed with the username from the interaction.
	default:
		slog.WarnContext(ctx, "discord_service: oauth identity lookup failed, proceeding without link",
			"discord_user_id", discordUserID,
			"error", err,
		)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return db.DiscordRegistration{}, fmt.Errorf("generate registration id: %w", err)
	}

	reg, err := s.q.UpsertDiscordRegistration(ctx, db.UpsertDiscordRegistrationParams{
		ID:              id,
		DiscordUserID:   discordUserID,
		DiscordUsername: discordUsername,
		GuildID:         guildID,
		GuildName:       guildName,
	})
	if err != nil {
		return db.DiscordRegistration{}, fmt.Errorf("upsert discord registration: %w", err)
	}

	if linkedUserID.Valid {
		if err := s.q.LinkDiscordRegistrationToUser(ctx, db.LinkDiscordRegistrationToUserParams{
			UserID:        linkedUserID,
			DiscordUserID: discordUserID,
		}); err != nil {
			return db.DiscordRegistration{}, fmt.Errorf("link discord registration to user: %w", err)
		}
	}

	return reg, nil
}

// SetChannel stores (or updates) the designated Concept2 reporting channel for
// a guild. The record is keyed on guild_id (unique); a repeated call updates the
// stored channel in place. guildID, channelID, and setByUserID must all be
// non-empty or ErrMissingGuildChannel is returned.
func (s *DiscordService) SetChannel(ctx context.Context, guildID, channelID, setByUserID string) (db.DiscordGuildSetting, error) {
	if guildID == "" || channelID == "" || setByUserID == "" {
		return db.DiscordGuildSetting{}, ErrMissingGuildChannel
	}

	id, err := uuid.NewV7()
	if err != nil {
		return db.DiscordGuildSetting{}, fmt.Errorf("generate guild settings id: %w", err)
	}

	setting, err := s.q.UpsertGuildSettings(ctx, db.UpsertGuildSettingsParams{
		ID:              id,
		GuildID:         guildID,
		ReportChannelID: channelID,
		SetByUserID:     setByUserID,
	})
	if err != nil {
		return db.DiscordGuildSetting{}, fmt.Errorf("upsert guild settings: %w", err)
	}

	return setting, nil
}
