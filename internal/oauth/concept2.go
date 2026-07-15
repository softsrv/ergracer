package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	concept2AuthorizePath = "/oauth/authorize"
	concept2TokenPath     = "/oauth/access_token"
	concept2UserPath      = "/api/users/me"
	concept2Scope         = "user:read,results:read"
)

// Concept2TokenResponse holds the OAuth token response from Concept2.
type Concept2TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// Concept2User holds the user profile from Concept2's /api/users/me endpoint.
type Concept2User struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
}

// DisplayName returns the best display name available for the Concept2 user.
func (u Concept2User) DisplayName() string {
	if u.Username != "" {
		return u.Username
	}
	if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
		return name
	}
	return fmt.Sprintf("user:%d", u.ID)
}

type concept2UserResponse struct {
	Data Concept2User `json:"data"`
}

// Concept2Client performs OAuth2 operations against the Concept2 Logbook API.
type Concept2Client struct {
	clientID     string
	clientSecret string
	redirectURI  string
	apiBase      string
	httpClient   *http.Client
}

// NewConcept2Client constructs a Concept2Client. Pass nil for httpClient to use
// the default. apiBase should be "https://log.concept2.com" in production.
func NewConcept2Client(clientID, clientSecret, redirectURI, apiBase string, httpClient *http.Client) *Concept2Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Concept2Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		apiBase:      apiBase,
		httpClient:   httpClient,
	}
}

// AuthorizeURL returns the Concept2 OAuth2 authorization URL for the given state.
func (c *Concept2Client) AuthorizeURL(state string) string {
	params := url.Values{
		"client_id":     {c.clientID},
		"redirect_uri":  {c.redirectURI},
		"response_type": {"code"},
		"scope":         {concept2Scope},
		"state":         {state},
	}
	return c.apiBase + concept2AuthorizePath + "?" + params.Encode()
}

// Exchange trades an authorization code for Concept2 access and refresh tokens.
func (c *Concept2Client) Exchange(ctx context.Context, code string) (Concept2TokenResponse, error) {
	body := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+concept2TokenPath, strings.NewReader(body.Encode()))
	if err != nil {
		return Concept2TokenResponse{}, fmt.Errorf("concept2 exchange: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Concept2TokenResponse{}, fmt.Errorf("concept2 exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Concept2TokenResponse{}, fmt.Errorf("concept2 exchange: status %d", resp.StatusCode)
	}
	var tok Concept2TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return Concept2TokenResponse{}, fmt.Errorf("concept2 exchange: decode: %w", err)
	}
	return tok, nil
}

// RefreshToken exchanges a Concept2 refresh token for a new token pair.
func (c *Concept2Client) RefreshToken(ctx context.Context, refreshToken string) (Concept2TokenResponse, error) {
	body := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+concept2TokenPath, strings.NewReader(body.Encode()))
	if err != nil {
		return Concept2TokenResponse{}, fmt.Errorf("concept2 refresh: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Concept2TokenResponse{}, fmt.Errorf("concept2 refresh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Concept2TokenResponse{}, fmt.Errorf("concept2 refresh: status %d", resp.StatusCode)
	}
	var tok Concept2TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return Concept2TokenResponse{}, fmt.Errorf("concept2 refresh: decode: %w", err)
	}
	return tok, nil
}

// CurrentUser fetches the authenticated Concept2 user's profile.
func (c *Concept2Client) CurrentUser(ctx context.Context, accessToken string) (Concept2User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+concept2UserPath, nil)
	if err != nil {
		return Concept2User{}, fmt.Errorf("concept2 current user: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Concept2User{}, fmt.Errorf("concept2 current user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Concept2User{}, fmt.Errorf("concept2 current user: status %d", resp.StatusCode)
	}
	var wrapper concept2UserResponse
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return Concept2User{}, fmt.Errorf("concept2 current user: decode: %w", err)
	}
	return wrapper.Data, nil
}
