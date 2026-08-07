package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

var discordAPIBase = "https://discord.com/api/v10"

// ApplicationCommand is the subset of Discord's ApplicationCommand object needed
// for registration. Type 1 = CHAT_INPUT (slash command).
type ApplicationCommand struct {
	Name                     string  `json:"name"`
	Description              string  `json:"description"`
	Type                     int     `json:"type"`
	DefaultMemberPermissions *string `json:"default_member_permissions,omitempty"`
}

// manageGuildPerm is the decimal string Discord expects for the MANAGE_GUILD
// permission bit (1<<5 = 32). ADMINISTRATOR implies MANAGE_GUILD, so setting
// this value restricts the command to admins and guild managers.
var manageGuildPerm = "32"

// Commands is the authoritative list of slash commands this application exposes.
// Add new commands here; they are registered on every startup via a bulk
// overwrite (see RegisterCommands), which also deletes from Discord any
// previously-registered command that's no longer listed here.
var Commands = []ApplicationCommand{
	{Type: 1, Name: "setchannel", Description: "Set the current channel as the Concept2 reporting channel", DefaultMemberPermissions: &manageGuildPerm},
}

// RegisterCommands bulk-overwrites global application commands via Discord's REST
// API. If client is nil, http.DefaultClient is used. A non-2xx response from
// Discord is returned as an error.
func RegisterCommands(ctx context.Context, client *http.Client, botToken, applicationID string, commands []ApplicationCommand) error {
	if client == nil {
		client = http.DefaultClient
	}
	body, err := json.Marshal(commands)
	if err != nil {
		return fmt.Errorf("marshal commands: %w", err)
	}
	url := fmt.Sprintf("%s/applications/%s/commands", discordAPIBase, applicationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+botToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("discord api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("discord api responded %d: %s", resp.StatusCode, bytes.TrimSpace(bodyBytes))
	}
	return nil
}
