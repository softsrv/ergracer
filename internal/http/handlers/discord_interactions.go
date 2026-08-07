package handlers

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/softsrv/rowbot/internal/db"
	"github.com/softsrv/rowbot/internal/discord"
)

// discordServicer is the subset of app.DiscordService used by DiscordHandler.
// Keeping it as a local interface makes the handler independently testable.
type discordServicer interface {
	SetChannel(ctx context.Context, guildID, guildName, channelID, channelName, setByUserID string) (db.DiscordGuildSetting, error)
}

// DiscordHandler handles Discord HTTP interaction requests.
type DiscordHandler struct {
	pubKey     ed25519.PublicKey
	botToken   string
	httpClient *http.Client
	svc        discordServicer
}

// NewDiscordHandler constructs a DiscordHandler with the given Ed25519 public key,
// bot token, HTTP client, and Discord service. When httpClient is nil,
// http.DefaultClient is used. When svc is nil, the setchannel command logs a
// warning and returns an ephemeral error response rather than panicking.
func NewDiscordHandler(pubKey ed25519.PublicKey, botToken string, httpClient *http.Client, svc discordServicer) *DiscordHandler {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &DiscordHandler{pubKey: pubKey, botToken: botToken, httpClient: httpClient, svc: svc}
}

// Interactions is the single public endpoint Discord POSTs all interactions to.
// Signature verification happens before any semantic parsing.
func (h *DiscordHandler) Interactions(w http.ResponseWriter, r *http.Request) {
	// Read the raw body first; BodyLimit middleware has already capped it at 1 MiB.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid request signature", http.StatusUnauthorized)
		return
	}

	timestamp := r.Header.Get("X-Signature-Timestamp")
	sigHex := r.Header.Get("X-Signature-Ed25519")
	if timestamp == "" || sigHex == "" || !discord.VerifySignature(h.pubKey, timestamp, body, sigHex) {
		slog.WarnContext(r.Context(), "discord interactions: signature verification failed")
		http.Error(w, "invalid request signature", http.StatusUnauthorized)
		return
	}

	var interaction discord.Interaction
	if err := json.Unmarshal(body, &interaction); err != nil {
		slog.WarnContext(r.Context(), "discord interactions: unmarshal body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var resp discord.InteractionResponse
	switch interaction.Type {
	case discord.InteractionTypePing:
		resp = discord.PongResponse()
	case discord.InteractionTypeApplicationCommand:
		resp = h.handleCommand(r, interaction)
	default:
		slog.WarnContext(r.Context(), "discord interactions: unknown type", "type", interaction.Type)
		http.Error(w, "unknown interaction type", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.ErrorContext(r.Context(), "discord interactions: encode response", "error", err)
	}
}

func (h *DiscordHandler) handleCommand(r *http.Request, interaction discord.Interaction) discord.InteractionResponse {
	switch interaction.Data.Name {
	case "setchannel":
		return h.handleSetChannel(r, interaction)
	default:
		slog.WarnContext(r.Context(), "discord interactions: unknown command", "command", interaction.Data.Name)
		return discord.EphemeralResponse("unknown command")
	}
}

// handleSetChannel processes the /setchannel command. It stores the current
// channel as the guild's Concept2 reporting channel. Only guild admins and
// members with MANAGE_GUILD may invoke this command (enforced both by
// default_member_permissions at the Discord level and defensively here).
func (h *DiscordHandler) handleSetChannel(r *http.Request, interaction discord.Interaction) discord.InteractionResponse {
	// Must be invoked inside a guild, not a DM.
	if interaction.GuildID == "" || interaction.Member == nil {
		return discord.EphemeralResponse("This command can only be used in a server.")
	}

	// Defensive permission check: parse the member's permission bitfield and
	// verify they hold MANAGE_GUILD (32) or ADMINISTRATOR (8). Discord's
	// default_member_permissions on the command definition is the primary gate;
	// this check defends against misconfiguration or client-side bypass.
	perms, parseErr := strconv.ParseInt(interaction.Member.Permissions, 10, 64)
	if parseErr != nil || (perms&discord.PermissionManageGuild == 0 && perms&discord.PermissionAdministrator == 0) {
		slog.WarnContext(r.Context(), "discord interactions: setchannel permission denied",
			"guild_id", interaction.GuildID,
			"user_id", interaction.Member.User.ID,
			"permissions", interaction.Member.Permissions,
		)
		return discord.EphemeralResponse("You need the Manage Server permission to use this command.")
	}

	// The channel is inferred from the interaction payload — no option needed.
	if interaction.ChannelID == "" {
		slog.WarnContext(r.Context(), "discord interactions: setchannel missing channel_id",
			"guild_id", interaction.GuildID,
		)
		return discord.EphemeralResponse("Could not determine the current channel. Please try again.")
	}

	if h.svc == nil {
		slog.WarnContext(r.Context(), "discord interactions: setchannel called but DiscordService is not configured")
		return discord.EphemeralResponse("This feature is not available right now. Please try again later.")
	}

	// Resolve guild name, falling back gracefully if the API call fails.
	guildName := "unknown server"
	guildCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if name, err := discord.GetGuildName(guildCtx, h.httpClient, h.botToken, interaction.GuildID); err == nil {
		guildName = name
	} else {
		slog.WarnContext(r.Context(), "discord interactions: get guild name", "guild_id", interaction.GuildID, "error", err)
	}

	// Discord includes a partial channel object (with its name) directly on
	// guild interactions, so no extra API call is needed here the way
	// guildName above requires one.
	channelName := ""
	if interaction.Channel != nil {
		channelName = interaction.Channel.Name
	}

	if _, err := h.svc.SetChannel(r.Context(), interaction.GuildID, guildName, interaction.ChannelID, channelName, interaction.Member.User.ID); err != nil {
		slog.ErrorContext(r.Context(), "discord interactions: set channel",
			"guild_id", interaction.GuildID,
			"channel_id", interaction.ChannelID,
			"user_id", interaction.Member.User.ID,
			"error", err,
		)
		return discord.EphemeralResponse("Something went wrong — please try again.")
	}

	return discord.EphemeralResponse("Done! I'll post Concept2 results in this channel.")
}
