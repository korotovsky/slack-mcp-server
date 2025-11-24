package oauth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Handler handles OAuth2 flows
type Handler struct {
	config *Config
	store  Store
	logger *zap.Logger
}

// NewHandler creates a new OAuth2 handler
func NewHandler(config *Config, store Store, logger *zap.Logger) *Handler {
	return &Handler{
		config: config,
		store:  store,
		logger: logger,
	}
}

// SlackOAuthResponse represents Slack's OAuth response
type SlackOAuthResponse struct {
	OK          bool   `json:"ok"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	BotUserID   string `json:"bot_user_id"`
	AppID       string `json:"app_id"`
	Team        struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	} `json:"team"`
	Enterprise interface{} `json:"enterprise"`
	AuthedUser struct {
		ID          string `json:"id"`
		Scope       string `json:"scope"`
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	} `json:"authed_user"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// TokenResponse represents the OAuth2 token response sent to clients
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

// HandleAuthorize initiates the OAuth2 flow by redirecting to Slack
// GET /oauth/authorize
func (h *Handler) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("OAuth2 authorize request received",
		zap.String("client_id", h.config.SlackClientID),
	)

	// Generate and save state for CSRF protection
	state, err := GenerateState()
	if err != nil {
		h.logger.Error("Failed to generate state", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.store.SaveState(state); err != nil {
		h.logger.Error("Failed to save state", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Build Slack OAuth URL
	params := url.Values{}
	params.Add("client_id", h.config.SlackClientID)
	params.Add("scope", strings.Join(h.config.SlackScopes, ","))
	params.Add("state", state)
	params.Add("redirect_uri", h.config.SlackRedirectURI)

	authURL := fmt.Sprintf("https://slack.com/oauth/v2/authorize?%s", params.Encode())

	h.logger.Info("Redirecting to Slack OAuth",
		zap.String("redirect_uri", h.config.SlackRedirectURI),
		zap.String("state", state),
	)

	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// HandleCallback handles the OAuth2 callback from Slack
// GET /oauth/callback
func (h *Handler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("OAuth2 callback received")

	// Validate state
	state := r.URL.Query().Get("state")
	if !h.store.ValidateState(state) {
		h.logger.Error("Invalid or expired state", zap.String("state", state))
		http.Error(w, "Invalid state parameter", http.StatusBadRequest)
		return
	}

	// Delete state after validation
	h.store.DeleteState(state)

	// Get authorization code
	code := r.URL.Query().Get("code")
	if code == "" {
		h.logger.Error("No authorization code received")
		http.Error(w, "No authorization code received", http.StatusBadRequest)
		return
	}

	// Check for errors from Slack
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		h.logger.Error("Slack OAuth error", zap.String("error", errMsg))
		http.Error(w, fmt.Sprintf("Slack OAuth error: %s", errMsg), http.StatusBadRequest)
		return
	}

	h.logger.Info("Exchanging code for token", zap.String("code", code[:10]+"..."))

	// Exchange code for token
	slackToken, userID, teamID, scopes, err := h.exchangeCodeForToken(code)
	if err != nil {
		h.logger.Error("Failed to exchange code for token", zap.Error(err))
		http.Error(w, "Failed to exchange code for token", http.StatusInternalServerError)
		return
	}

	// Generate MCP access token
	mcpToken, err := GenerateToken()
	if err != nil {
		h.logger.Error("Failed to generate MCP token", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Store token mapping
	tokenInfo := &TokenInfo{
		AccessToken: mcpToken,
		SlackToken:  slackToken,
		ExpiresAt:   time.Now().Add(90 * 24 * time.Hour), // 90 days
		UserID:      userID,
		TeamID:      teamID,
		Scopes:      scopes,
	}

	if err := h.store.SaveToken(mcpToken, tokenInfo); err != nil {
		h.logger.Error("Failed to save token", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Info("OAuth2 flow completed successfully",
		zap.String("user_id", userID),
		zap.String("team_id", teamID),
	)

	// Return success page with token (in production, you might want a redirect instead)
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
    <title>OAuth2 Success</title>
    <style>
        body { font-family: Arial, sans-serif; max-width: 600px; margin: 50px auto; padding: 20px; }
        .success { background-color: #d4edda; border: 1px solid #c3e6cb; padding: 15px; border-radius: 5px; }
        .token { background-color: #f8f9fa; padding: 10px; border-radius: 3px; font-family: monospace; word-break: break-all; margin: 10px 0; }
    </style>
</head>
<body>
    <div class="success">
        <h2>✓ Authorization Successful</h2>
        <p>Your Slack workspace has been connected to the MCP server.</p>
        <p><strong>Access Token:</strong></p>
        <div class="token">%s</div>
        <p><small>Use this token in your LiteLLM configuration.</small></p>
    </div>
</body>
</html>
`, mcpToken)
}

// HandleToken handles the OAuth2 token endpoint
// POST /oauth/token
func (h *Handler) HandleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	h.logger.Info("OAuth2 token request received")

	// Parse form data
	if err := r.ParseForm(); err != nil {
		h.logger.Error("Failed to parse form", zap.Error(err))
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Validate client credentials
	clientID := r.FormValue("client_id")
	clientSecret := r.FormValue("client_secret")

	if h.config.MCPClientID != "" && clientID != h.config.MCPClientID {
		h.logger.Error("Invalid client ID", zap.String("client_id", clientID))
		http.Error(w, "Invalid client credentials", http.StatusUnauthorized)
		return
	}

	if h.config.MCPClientSecret != "" && clientSecret != h.config.MCPClientSecret {
		h.logger.Error("Invalid client secret")
		http.Error(w, "Invalid client credentials", http.StatusUnauthorized)
		return
	}

	grantType := r.FormValue("grant_type")

	switch grantType {
	case "authorization_code":
		// For now, we don't support authorization_code via this endpoint
		// since the callback already returns the token
		http.Error(w, "Use the /oauth/authorize flow instead", http.StatusBadRequest)
		return

	case "client_credentials":
		// Client credentials flow - for service-to-service auth
		// This would use the environment token instead of per-user tokens
		h.handleClientCredentials(w, r)
		return

	default:
		http.Error(w, "Unsupported grant type", http.StatusBadRequest)
		return
	}
}

func (h *Handler) handleClientCredentials(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("Handling client credentials flow")

	// For client credentials, we generate a token that will use the environment Slack token
	mcpToken, err := GenerateToken()
	if err != nil {
		h.logger.Error("Failed to generate token", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Store token with empty SlackToken (will fall back to env token)
	tokenInfo := &TokenInfo{
		AccessToken: mcpToken,
		SlackToken:  "", // Empty means use environment token
		ExpiresAt:   time.Now().Add(90 * 24 * time.Hour),
		Scopes:      h.config.SlackScopes,
	}

	if err := h.store.SaveToken(mcpToken, tokenInfo); err != nil {
		h.logger.Error("Failed to save token", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := TokenResponse{
		AccessToken: mcpToken,
		TokenType:   "Bearer",
		ExpiresIn:   90 * 24 * 60 * 60, // 90 days in seconds
		Scope:       strings.Join(h.config.SlackScopes, " "),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// exchangeCodeForToken exchanges an authorization code for a Slack access token
func (h *Handler) exchangeCodeForToken(code string) (token, userID, teamID string, scopes []string, err error) {
	data := url.Values{}
	data.Set("client_id", h.config.SlackClientID)
	data.Set("client_secret", h.config.SlackClientSecret)
	data.Set("code", code)
	data.Set("redirect_uri", h.config.SlackRedirectURI)

	resp, err := http.PostForm("https://slack.com/api/oauth.v2.access", data)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("failed to call Slack API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("failed to read response: %w", err)
	}

	var slackResp SlackOAuthResponse
	if err := json.Unmarshal(body, &slackResp); err != nil {
		return "", "", "", nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !slackResp.OK {
		return "", "", "", nil, fmt.Errorf("slack error: %s - %s", slackResp.Error, slackResp.ErrorDescription)
	}

	// Use the user token if available, otherwise use the bot token
	accessToken := slackResp.AuthedUser.AccessToken
	if accessToken == "" {
		accessToken = slackResp.AccessToken
	}

	// Parse scopes
	scopeStr := slackResp.AuthedUser.Scope
	if scopeStr == "" {
		scopeStr = slackResp.Scope
	}
	scopes = strings.Split(scopeStr, ",")

	return accessToken, slackResp.AuthedUser.ID, slackResp.Team.ID, scopes, nil
}
