package oauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Manager struct {
	clientID     string
	clientSecret string
	redirectURI  string
	storage      TokenStorage
	httpClient   *http.Client
}

// NewManager creates a new OAuth manager
func NewManager(clientID, clientSecret, redirectURI string, storage TokenStorage) *Manager {
	return &Manager{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		storage:      storage,
		httpClient: &http.Client{
			Timeout: 10 * time.Second, // Prevent hanging requests
		},
	}
}

// GetAuthURL generates the Slack OAuth authorization URL
func (m *Manager) GetAuthURL(state string) string {
	// User token scopes for OAuth v2
	// Note: Bot scopes removed - users always act as themselves
	userScopes := []string{
		"channels:history",
		"channels:read",
		"groups:history",
		"groups:read",
		"im:history",
		"im:read",
		"im:write",
		"mpim:history",
		"mpim:read",
		"mpim:write",
		"users:read",
		"chat:write",
		"search:read",
	}

	params := url.Values{
		"client_id":    {m.clientID},
		"user_scope":   {strings.Join(userScopes, ",")}, // User scopes only
		"redirect_uri": {m.redirectURI},
		"state":        {state},
	}

	return "https://slack.com/oauth/v2/authorize?" + params.Encode()
}

// HandleCallback exchanges OAuth code for access token
func (m *Manager) HandleCallback(code, state string) (*TokenResponse, error) {
	data := url.Values{
		"client_id":     {m.clientID},
		"client_secret": {m.clientSecret},
		"code":          {code},
		"redirect_uri":  {m.redirectURI},
	}

	resp, err := m.httpClient.PostForm("https://slack.com/api/oauth.v2.access", data)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		AccessToken string `json:"access_token"` // Bot token (if bot scopes requested)
		AuthedUser  struct {
			ID          string `json:"id"`
			AccessToken string `json:"access_token"` // User token
		} `json:"authed_user"`
		BotUserID string `json:"bot_user_id"` // Bot user ID (if bot installed)
		Team      struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if !result.OK {
		return nil, fmt.Errorf("slack error: %s", result.Error)
	}

	token := &TokenResponse{
		AccessToken: result.AuthedUser.AccessToken,        // User token (xoxp-...)
		BotToken:    result.AccessToken,                   // Bot token (xoxb-...) if available
		UserID:      result.AuthedUser.ID,
		TeamID:      result.Team.ID,
		BotUserID:   result.BotUserID,
		ExpiresAt:   time.Now().Add(365 * 24 * time.Hour), // Slack tokens don't expire by default
	}

	// Store token
	if err := m.storage.Store(token.UserID, token); err != nil {
		return nil, fmt.Errorf("failed to store token: %w", err)
	}

	// Log whether we got bot token
	if token.BotToken != "" {
		// Bot token available - can post as bot
	} else {
		// No bot token - will post as user only
	}

	return token, nil
}

// ValidateToken validates an access token with Slack
func (m *Manager) ValidateToken(accessToken string) (*TokenInfo, error) {
	req, err := http.NewRequest("POST", "https://slack.com/api/auth.test", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		UserID string `json:"user_id"`
		TeamID string `json:"team_id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if !result.OK {
		return nil, fmt.Errorf("invalid token: %s", result.Error)
	}

	return &TokenInfo{
		UserID: result.UserID,
		TeamID: result.TeamID,
	}, nil
}

// GetStoredToken retrieves the full token response for a user
func (m *Manager) GetStoredToken(userID string) (*TokenResponse, error) {
	return m.storage.Get(userID)
}
