package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	discordAuthorizeURL = "https://discord.com/oauth2/authorize"
	discordTokenURL     = "https://discord.com/api/oauth2/token"
	discordUserURL      = "https://discord.com/api/users/@me"
)

type DiscordUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
}

type discordTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
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
		httpClient = &http.Client{}
	}
	return &DiscordClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		httpClient:   httpClient,
	}
}

// BotInstallURL returns the Discord OAuth2 URL that lets a server admin add
// the application (bot) to their server.
func (c *DiscordClient) BotInstallURL(permissions int) string {
	params := url.Values{
		"client_id":   {c.clientID},
		"scope":       {"bot applications.commands"},
		"permissions": {strconv.Itoa(permissions)},
	}
	return discordAuthorizeURL + "?" + params.Encode()
}

// AuthorizeURL returns the Discord OAuth2 authorization URL for the given state.
func (c *DiscordClient) AuthorizeURL(state string) string {
	params := url.Values{
		"client_id":     {c.clientID},
		"redirect_uri":  {c.redirectURI},
		"response_type": {"code"},
		"scope":         {"identify email"},
		"state":         {state},
	}
	return discordAuthorizeURL + "?" + params.Encode()
}

// Exchange trades an authorization code for a Discord access token.
func (c *DiscordClient) Exchange(ctx context.Context, code string) (discordTokenResponse, error) {
	body := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discordTokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return discordTokenResponse{}, fmt.Errorf("discord exchange: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return discordTokenResponse{}, fmt.Errorf("discord exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return discordTokenResponse{}, fmt.Errorf("discord exchange: status %d", resp.StatusCode)
	}
	var tok discordTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return discordTokenResponse{}, fmt.Errorf("discord exchange: decode: %w", err)
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
