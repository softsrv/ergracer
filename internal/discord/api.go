package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GetGuildName fetches the name of a Discord guild by its ID. If client is nil,
// http.DefaultClient is used. A non-2xx response from Discord is returned as an error.
func GetGuildName(ctx context.Context, client *http.Client, botToken, guildID string) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	url := fmt.Sprintf("%s/guilds/%s", discordAPIBase, guildID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+botToken)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("discord api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("discord api responded %d: %s", resp.StatusCode, bytes.TrimSpace(bodyBytes))
	}
	var guild struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&guild); err != nil {
		return "", fmt.Errorf("decode guild: %w", err)
	}
	return guild.Name, nil
}
