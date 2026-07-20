package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/softsrv/ergracer/internal/concept2"
	"github.com/softsrv/ergracer/internal/db"
	"github.com/softsrv/ergracer/internal/discord"
	"github.com/softsrv/ergracer/internal/oauth"
	"github.com/softsrv/ergracer/internal/secrets"
)

// RowingService processes incoming Concept2 workout results and posts them to
// the appropriate Discord reporting channels.
type RowingService struct {
	q          *db.Queries
	concept2   *oauth.Concept2Client
	encrypter  *secrets.Encrypter
	botToken   string
	httpClient *http.Client
}

// NewRowingService constructs a RowingService. concept2Client and encrypter may
// be nil (ProcessResult will fail fast with a clear error if they are needed but
// absent). Pass nil for httpClient to use http.DefaultClient.
func NewRowingService(q *db.Queries, concept2Client *oauth.Concept2Client, encrypter *secrets.Encrypter, botToken string, httpClient *http.Client) *RowingService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &RowingService{
		q:          q,
		concept2:   concept2Client,
		encrypter:  encrypter,
		botToken:   botToken,
		httpClient: httpClient,
	}
}

// ProcessResult resolves a Concept2 webhook result through the full pipeline:
//  1. Look up the local user linked to the Concept2 account.
//  2. Retrieve and decrypt the stored OAuth token (refreshing if expired).
//  3. Fetch the result from the Concept2 API.
//  4. Find all Discord guilds the user is registered in.
//  5. Post a formatted embed to each guild's configured reporting channel.
//
// Business-logic discards (no linked user, no token, no Discord registrations,
// no guild settings) return nil — only unexpected failures return errors.
func (s *RowingService) ProcessResult(ctx context.Context, concept2UserID int64, resultID int64) error {
	// 1. Look up oauth_identity for this Concept2 user.
	identity, err := s.q.GetOAuthIdentityByProviderUserID(ctx, db.GetOAuthIdentityByProviderUserIDParams{
		Provider:       "concept2",
		ProviderUserID: strconv.FormatInt(concept2UserID, 10),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Info("concept2 result: no linked user, discarding",
				"concept2_user_id", concept2UserID,
			)
			return nil
		}
		return fmt.Errorf("concept2 result: lookup identity: %w", err)
	}

	// 2. Get the stored encrypted OAuth token for this identity.
	token, err := s.q.GetOAuthTokenByIdentity(ctx, identity.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Info("concept2 result: no stored token for user, discarding",
				"concept2_user_id", concept2UserID,
				"identity_id", identity.ID,
			)
			return nil
		}
		return fmt.Errorf("concept2 result: get token: %w", err)
	}

	// 3. Decrypt the access token.
	accessPlain, err := s.encrypter.Decrypt(token.AccessTokenEnc)
	if err != nil {
		return fmt.Errorf("concept2 result: decrypt access token: %w", err)
	}
	accessToken := string(accessPlain)

	// 4. Refresh token if expired or expiring within 60 seconds.
	if token.ExpiresAt.Valid && time.Until(token.ExpiresAt.Time) <= 60*time.Second {
		slog.Debug("concept2 result: token expired or expiring soon, refreshing",
			"identity_id", identity.ID,
			"expires_at", token.ExpiresAt.Time,
		)

		if len(token.RefreshTokenEnc) == 0 {
			return fmt.Errorf("concept2 result: token is expired and no refresh token stored")
		}

		refreshPlain, decErr := s.encrypter.Decrypt(token.RefreshTokenEnc)
		if decErr != nil {
			return fmt.Errorf("concept2 result: decrypt refresh token: %w", decErr)
		}

		newTok, refreshErr := s.concept2.RefreshToken(ctx, string(refreshPlain))
		if refreshErr != nil {
			slog.Error("concept2 result: token refresh failed",
				"identity_id", identity.ID,
				"error", refreshErr,
			)
			return fmt.Errorf("concept2 result: refresh token: %w", refreshErr)
		}

		// Re-encrypt and persist the refreshed token pair.
		if storeErr := s.storeRefreshedToken(ctx, identity.ID, newTok.AccessToken, newTok.RefreshToken, newTok.Scope, newTok.ExpiresIn); storeErr != nil {
			return fmt.Errorf("concept2 result: persist refreshed token: %w", storeErr)
		}
		accessToken = newTok.AccessToken
	}

	// 5. Fetch the result from the Concept2 API.
	result, err := concept2.GetResult(ctx, s.httpClient, s.concept2.APIBase(), accessToken, resultID)
	if err != nil {
		return fmt.Errorf("concept2 result: fetch result: %w", err)
	}

	// 6. Find Discord registrations for this user.
	registrations, err := s.q.ListDiscordRegistrationsByUser(ctx, pgtype.UUID{
		Bytes: identity.UserID,
		Valid: true,
	})
	if err != nil {
		return fmt.Errorf("concept2 result: list discord registrations: %w", err)
	}
	if len(registrations) == 0 {
		slog.Info("concept2 result: user has no discord registrations, discarding",
			"user_id", identity.UserID,
			"result_id", resultID,
		)
		return nil
	}

	// 7. For each registration, look up guild settings and send the embed.
	for _, reg := range registrations {
		settings, settingsErr := s.q.GetGuildSettings(ctx, reg.GuildID)
		if settingsErr != nil {
			if errors.Is(settingsErr, pgx.ErrNoRows) {
				slog.Debug("concept2 result: no guild settings configured, skipping guild",
					"guild_id", reg.GuildID,
				)
				continue
			}
			slog.Error("concept2 result: get guild settings failed",
				"guild_id", reg.GuildID,
				"error", settingsErr,
			)
			continue
		}

		if sendErr := s.sendResultEmbed(ctx, result, settings.ReportChannelID); sendErr != nil {
			slog.Error("concept2 result: send discord embed failed",
				"guild_id", reg.GuildID,
				"channel_id", settings.ReportChannelID,
				"result_id", resultID,
				"error", sendErr,
			)
		} else {
			slog.Info("concept2 result: discord embed sent",
				"guild_id", reg.GuildID,
				"channel_id", settings.ReportChannelID,
				"result_id", resultID,
			)
		}
	}

	return nil
}

// sendResultEmbed builds and posts a Discord embed for a rowing result.
func (s *RowingService) sendResultEmbed(ctx context.Context, result concept2.Result, channelID string) error {
	embed := buildResultEmbed(result)
	msg := discord.ChannelMessage{Embeds: []discord.Embed{embed}}
	return discord.SendChannelMessage(ctx, s.httpClient, s.botToken, channelID, msg)
}

// buildResultEmbed formats a Concept2 result as a Discord embed.
func buildResultEmbed(result concept2.Result) discord.Embed {
	title, color := sportTitleAndColor(result.Type)

	// Use the API's pre-formatted time when available — it includes rest time
	// for interval workouts, which is what Concept2 displays in their UI.
	timeStr := result.TimeFormatted
	if timeStr == "" {
		timeStr = formatDuration(result.Time)
	}

	fields := []discord.EmbedField{
		{Name: "Distance", Value: formatDistance(result.Distance), Inline: true},
		{Name: "Time", Value: timeStr, Inline: true},
		{Name: "Stroke Rate", Value: fmt.Sprintf("%d spm", result.StrokeRate), Inline: true},
		{Name: "Workout Type", Value: result.WorkoutType, Inline: true},
	}

	// Render splits/intervals as three inline column fields so Discord displays
	// them as a table with Distance, Time, and Pace as headers.
	if pieces := result.Workout.Pieces(); len(pieces) > 0 {
		label := "Splits"
		if result.Workout.IsIntervals() {
			label = "Intervals"
		}

		var distances, times, paces []string
		for _, p := range pieces {
			distances = append(distances, formatDistance(p.Distance))
			times = append(times, formatDuration(p.Time))
			if pace := p.Pace(); pace > 0 {
				paces = append(paces, formatDuration(pace))
			} else {
				paces = append(paces, "—")
			}
		}

		// Non-inline header field breaks the flow from the summary fields above.
		// Zero-width space satisfies Discord's non-empty value requirement.
		fields = append(fields,
			discord.EmbedField{Name: label, Value: "​", Inline: false},
			discord.EmbedField{Name: "Distance", Value: strings.Join(distances, "\n"), Inline: true},
			discord.EmbedField{Name: "Time", Value: strings.Join(times, "\n"), Inline: true},
			discord.EmbedField{Name: "Pace /500m", Value: strings.Join(paces, "\n"), Inline: true},
		)
	}

	embed := discord.Embed{
		Title:  title,
		Color:  color,
		Fields: fields,
		Footer: &discord.EmbedFooter{Text: "Concept2 Logbook"},
	}

	// Parse the date as ISO8601 timestamp for the embed.
	// The API returns dates as either "2006-01-02" or "2006-01-02 15:04:05".
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, result.Date); err == nil {
			embed.Timestamp = t.UTC().Format(time.RFC3339)
			break
		}
	}

	return embed
}

// sportTitleAndColor returns the embed title and color for a given Concept2 sport type.
func sportTitleAndColor(sportType string) (string, int) {
	switch sportType {
	case "skierg":
		return "New SkiErg Result", 0x5B9BD5
	case "bikeerg":
		return "New BikeErg Result", 0xED7D31
	default:
		// "rower" and anything unknown default to rowing
		return "New Rowing Result", 0x4A90D9
	}
}

// formatDuration converts tenths of a second to "m:ss.t" format.
// For example, 13477 → "22:27.7".
func formatDuration(tenths int64) string {
	if tenths <= 0 {
		return "0:00.0"
	}
	t := tenths % 10
	totalSeconds := tenths / 10
	seconds := totalSeconds % 60
	minutes := totalSeconds / 60
	return fmt.Sprintf("%d:%02d.%d", minutes, seconds, t)
}

// formatDistance formats a distance in metres as a human-readable string.
// Values under 10 km are shown as whole metres with a thousands separator;
// values 10 km and over are shown in kilometres with one decimal place.
func formatDistance(metres float64) string {
	if metres >= 10000 {
		return fmt.Sprintf("%.1f km", metres/1000)
	}
	// Format with comma thousands separator for values >= 1000.
	m := int64(metres)
	if m >= 1000 {
		thousands := m / 1000
		remainder := m % 1000
		return fmt.Sprintf("%d,%03d m", thousands, remainder)
	}
	return fmt.Sprintf("%d m", m)
}

// storeRefreshedToken encrypts and upserts a refreshed token pair for the given identity.
func (s *RowingService) storeRefreshedToken(ctx context.Context, identityID uuid.UUID, accessToken, refreshToken, scope string, expiresIn int) error {
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

	_, err = s.q.UpsertOAuthToken(ctx, db.UpsertOAuthTokenParams{
		ID:              tokenID,
		OauthIdentityID: identityID,
		AccessTokenEnc:  accessEnc,
		RefreshTokenEnc: refreshEnc,
		Scope:           pgtype.Text{String: scope, Valid: scope != ""},
		ExpiresAt:       expiresAt,
	})
	return err
}
