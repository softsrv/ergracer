package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// EmbedField is a single named field within a Discord embed.
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// EmbedFooter is the footer section of a Discord embed.
type EmbedFooter struct {
	Text string `json:"text"`
}

// Embed is a Discord message embed object.
type Embed struct {
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"` // ISO8601
}

// ChannelMessage is the request body for creating a Discord channel message.
type ChannelMessage struct {
	Embeds []Embed `json:"embeds"`
}

// SendChannelMessage posts a message with embeds to the given Discord channel.
// It uses the bot token for authentication.
func SendChannelMessage(ctx context.Context, client *http.Client, botToken, channelID string, msg ChannelMessage) error {
	if client == nil {
		client = http.DefaultClient
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("discord send message: marshal: %w", err)
	}
	url := fmt.Sprintf("%s/channels/%s/messages", discordAPIBase, channelID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord send message: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+botToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("discord send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("discord send message: status %d: %s", resp.StatusCode, bytes.TrimSpace(bodyBytes))
	}
	return nil
}
