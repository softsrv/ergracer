package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	discordAuthorizeURL = "https://discord.com/oauth2/authorize"
	discordTokenURL     = "https://discord.com/api/oauth2/token"
	discordUserURL      = "https://discord.com/api/users/@me"
	discordGuildsURL    = "https://discord.com/api/users/@me/guilds"

	// discordScope requests "guilds" in addition to the identify/email scopes
	// already in use so we can later ask Discord which guilds the user
	// administers (GetAdminGuilds in the OAuth service). This does mean
	// existing sessions must re-consent once to pick up the new scope.
	discordScope = "identify email guilds"
)

type DiscordUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
}

// DiscordGuild is one entry from GET /users/@me/guilds — a guild the user
// belongs to, along with their permissions in it. Permissions is a numeric
// bitfield here (unlike some other Discord API surfaces, e.g. interaction
// payloads, which encode permissions as a string to dodge JS's 53-bit safe
// integer limit) — decoding it as a string here fails outright, since
// Discord actually sends a JSON number.
type DiscordGuild struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Owner       bool   `json:"owner"`
	Permissions uint64 `json:"permissions"`
}

// DiscordTokenResponse holds the OAuth token response from Discord.
type DiscordTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// DiscordClient performs OAuth2 operations against the Discord API.
type DiscordClient struct {
	clientID     string
	clientSecret string
	redirectURI  string
	httpClient   *http.Client
}

// NewDiscordClient constructs a DiscordClient. Pass nil for httpClient to use the default.
func NewDiscordClient(clientID, clientSecret, redirectURI string, httpClient *http.Client) *DiscordClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &DiscordClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		httpClient:   httpClient,
	}
}

// BotInstallURL returns the Discord OAuth2 URL that lets a server admin add
// the application (bot) to their server. Including redirect_uri and
// response_type=code alongside scope=bot triggers Discord's "extended bot
// authorization" flow: on redirect, Discord appends guild_id (and
// permissions) as querystring parameters, which is how we capture which
// guild the bot was just installed into. We don't exchange the returned code
// for anything — the callback only reads guild_id off the redirect.
// https://docs.discord.com/developers/topics/oauth2
func (c *DiscordClient) BotInstallURL(permissions int) string {
	params := url.Values{
		"client_id":     {c.clientID},
		"scope":         {"bot applications.commands"},
		"permissions":   {strconv.Itoa(permissions)},
		"redirect_uri":  {c.redirectURI},
		"response_type": {"code"},
	}
	return discordAuthorizeURL + "?" + params.Encode()
}

// AuthorizeURL returns the Discord OAuth2 authorization URL for the given
// state. Discord always shows its authorization screen for this URL, even if
// the user has already authorized this application with these scopes before.
func (c *DiscordClient) AuthorizeURL(state string) string {
	return c.authorizeURL(state, "")
}

// SilentAuthorizeURL returns a Discord OAuth2 authorization URL with
// prompt=none, asking Discord to skip the authorization screen and redirect
// straight back — with either a code or an error — if the user has already
// authorized this application with these scopes. Because prompt=none can't
// show any UI, a user who hasn't authorized yet (or revoked access) gets
// redirected back with an error instead of a fresh consent screen, so
// callers must be ready to fall back to AuthorizeURL when that happens.
// https://docs.discord.com/developers/topics/oauth2
func (c *DiscordClient) SilentAuthorizeURL(state string) string {
	return c.authorizeURL(state, "none")
}

func (c *DiscordClient) authorizeURL(state, prompt string) string {
	params := url.Values{
		"client_id":     {c.clientID},
		"redirect_uri":  {c.redirectURI},
		"response_type": {"code"},
		"scope":         {discordScope},
		"state":         {state},
	}
	if prompt != "" {
		params.Set("prompt", prompt)
	}
	return discordAuthorizeURL + "?" + params.Encode()
}

// Exchange trades an authorization code for a Discord access token.
func (c *DiscordClient) Exchange(ctx context.Context, code string) (DiscordTokenResponse, error) {
	body := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discordTokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return DiscordTokenResponse{}, fmt.Errorf("discord exchange: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return DiscordTokenResponse{}, fmt.Errorf("discord exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DiscordTokenResponse{}, fmt.Errorf("discord exchange: status %d", resp.StatusCode)
	}
	var tok DiscordTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return DiscordTokenResponse{}, fmt.Errorf("discord exchange: decode: %w", err)
	}
	return tok, nil
}

// RefreshToken exchanges a Discord refresh token for a new token pair.
func (c *DiscordClient) RefreshToken(ctx context.Context, refreshToken string) (DiscordTokenResponse, error) {
	body := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discordTokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return DiscordTokenResponse{}, fmt.Errorf("discord refresh: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return DiscordTokenResponse{}, fmt.Errorf("discord refresh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DiscordTokenResponse{}, fmt.Errorf("discord refresh: status %d", resp.StatusCode)
	}
	var tok DiscordTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return DiscordTokenResponse{}, fmt.Errorf("discord refresh: decode: %w", err)
	}
	return tok, nil
}

// CurrentUser fetches the authenticated Discord user's profile.
func (c *DiscordClient) CurrentUser(ctx context.Context, accessToken string) (DiscordUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discordUserURL, nil)
	if err != nil {
		return DiscordUser{}, fmt.Errorf("discord current user: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return DiscordUser{}, fmt.Errorf("discord current user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DiscordUser{}, fmt.Errorf("discord current user: status %d", resp.StatusCode)
	}
	var u DiscordUser
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return DiscordUser{}, fmt.Errorf("discord current user: decode: %w", err)
	}
	return u, nil
}

// CurrentUserGuilds fetches the list of guilds the authenticated Discord user
// belongs to, including their per-guild owner flag and permissions bitfield.
// Requires the "guilds" scope on the access token.
func (c *DiscordClient) CurrentUserGuilds(ctx context.Context, accessToken string) ([]DiscordGuild, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discordGuildsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("discord current user guilds: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discord current user guilds: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discord current user guilds: status %d", resp.StatusCode)
	}
	var guilds []DiscordGuild
	if err := json.NewDecoder(resp.Body).Decode(&guilds); err != nil {
		return nil, fmt.Errorf("discord current user guilds: decode: %w", err)
	}
	return guilds, nil
}
