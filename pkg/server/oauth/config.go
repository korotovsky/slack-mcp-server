package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"os"
)

// Config holds OAuth2 configuration for the MCP server
type Config struct {
	// Slack OAuth2 settings (for authenticating with Slack)
	SlackClientID     string
	SlackClientSecret string
	SlackRedirectURI  string
	SlackScopes       []string

	// MCP OAuth2 settings (for authenticating LiteLLM clients)
	MCPClientID     string
	MCPClientSecret string

	// Whether OAuth2 is enabled
	Enabled bool
}

// NewConfigFromEnv creates a new OAuth2 config from environment variables
func NewConfigFromEnv() *Config {
	slackClientID := os.Getenv("SLACK_OAUTH_CLIENT_ID")
	slackClientSecret := os.Getenv("SLACK_OAUTH_CLIENT_SECRET")
	slackRedirectURI := os.Getenv("SLACK_OAUTH_REDIRECT_URI")

	mcpClientID := os.Getenv("MCP_OAUTH_CLIENT_ID")
	mcpClientSecret := os.Getenv("MCP_OAUTH_CLIENT_SECRET")

	// OAuth2 is enabled if all required Slack credentials are provided
	enabled := slackClientID != "" && slackClientSecret != "" && slackRedirectURI != ""

	// Default Slack scopes
	scopes := []string{
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

	return &Config{
		SlackClientID:     slackClientID,
		SlackClientSecret: slackClientSecret,
		SlackRedirectURI:  slackRedirectURI,
		SlackScopes:       scopes,
		MCPClientID:       mcpClientID,
		MCPClientSecret:   mcpClientSecret,
		Enabled:           enabled,
	}
}

// GenerateState generates a random state string for CSRF protection
func GenerateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// GenerateToken generates a random token string
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
