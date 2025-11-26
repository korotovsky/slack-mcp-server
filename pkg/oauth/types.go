package oauth

import "time"

// TokenResponse represents OAuth token response from Slack
type TokenResponse struct {
	AccessToken    string    `json:"access_token"`      // User token (xoxp-...)
	BotToken       string    `json:"bot_token"`         // Bot token (xoxb-...) - optional
	UserID         string    `json:"user_id"`
	TeamID         string    `json:"team_id"`
	BotUserID      string    `json:"bot_user_id"`       // Bot user ID - optional
	ExpiresAt      time.Time `json:"expires_at"`
}

// TokenInfo represents validated token information
type TokenInfo struct {
	UserID string
	TeamID string
}

// OAuthManager handles OAuth 2.0 flow with Slack
type OAuthManager interface {
	// GetAuthURL generates OAuth authorization URL
	GetAuthURL(state string) string

	// HandleCallback processes OAuth callback and exchanges code for token
	HandleCallback(code, state string) (*TokenResponse, error)

	// ValidateToken validates an access token
	ValidateToken(accessToken string) (*TokenInfo, error)
	
	// GetStoredToken retrieves stored token for a user
	GetStoredToken(userID string) (*TokenResponse, error)
}

// TokenStorage stores and retrieves OAuth tokens
type TokenStorage interface {
	// Store saves a token for a user
	Store(userID string, token *TokenResponse) error

	// Get retrieves a token for a user
	Get(userID string) (*TokenResponse, error)
}

