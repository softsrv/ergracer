package handlers

import (
	"crypto/ed25519"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/softsrv/ergracer/internal/discord"
)

// DiscordHandler handles Discord HTTP interaction requests.
type DiscordHandler struct {
	pubKey ed25519.PublicKey
}

// NewDiscordHandler constructs a DiscordHandler with the given Ed25519 public key.
func NewDiscordHandler(pubKey ed25519.PublicKey) *DiscordHandler {
	return &DiscordHandler{pubKey: pubKey}
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
	case "register":
		return discord.MessageResponse("thanks for requesting registration")
	default:
		slog.WarnContext(r.Context(), "discord interactions: unknown command", "command", interaction.Data.Name)
		return discord.EphemeralResponse("unknown command")
	}
}
