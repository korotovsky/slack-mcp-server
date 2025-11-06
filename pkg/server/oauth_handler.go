package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/korotovsky/slack-mcp-server/pkg/oauth"
	"go.uber.org/zap"
)

// OAuthHandler handles OAuth authorization flow
type OAuthHandler struct {
	manager  oauth.OAuthManager
	logger   *zap.Logger
	states   map[string]time.Time
	statesMu sync.RWMutex
}

// NewOAuthHandler creates a new OAuth handler
func NewOAuthHandler(mgr oauth.OAuthManager, logger *zap.Logger) *OAuthHandler {
	h := &OAuthHandler{
		manager: mgr,
		logger:  logger,
		states:  make(map[string]time.Time),
	}
	go h.cleanupStates()
	return h
}

// HandleAuthorize initiates the OAuth flow
func (h *OAuthHandler) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	// Generate CSRF state
	state := generateState()

	h.statesMu.Lock()
	h.states[state] = time.Now().Add(10 * time.Minute)
	h.statesMu.Unlock()

	// Generate OAuth URL
	authURL := h.manager.GetAuthURL(state)

	// Security headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-XSS-Protection", "1; mode=block")

	json.NewEncoder(w).Encode(map[string]string{
		"authorization_url": authURL,
		"state":            state,
	})
}

// HandleCallback processes OAuth callback
func (h *OAuthHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		http.Error(w, "Missing code or state", http.StatusBadRequest)
		return
	}

	// Verify state
	h.statesMu.RLock()
	expiry, ok := h.states[state]
	h.statesMu.RUnlock()

	if !ok || time.Now().After(expiry) {
		http.Error(w, "Invalid or expired state", http.StatusBadRequest)
		return
	}

	// Clean up state
	h.statesMu.Lock()
	delete(h.states, state)
	h.statesMu.Unlock()

	// Exchange code for token
	token, err := h.manager.HandleCallback(code, state)
	if err != nil {
		h.logger.Error("OAuth callback failed", zap.Error(err))
		http.Error(w, "Authentication failed", http.StatusInternalServerError)
		return
	}

	h.logger.Info("User authenticated via OAuth",
		zap.String("userID", token.UserID),
		zap.String("teamID", token.TeamID),
	)

	// Security headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-XSS-Protection", "1; mode=block")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")

	// Return token to user
	// Note: Only user token returned - bot posting is disabled
	response := map[string]string{
		"access_token": token.AccessToken,
		"user_id":      token.UserID,
		"team_id":      token.TeamID,
		"message":      "Authentication successful! Use this access_token in your MCP client. You will post as yourself.",
	}
	
	json.NewEncoder(w).Encode(response)
}

func (h *OAuthHandler) cleanupStates() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		h.statesMu.Lock()
		now := time.Now()
		for state, expiry := range h.states {
			if now.After(expiry) {
				delete(h.states, state)
			}
		}
		h.statesMu.Unlock()
	}
}

func generateState() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is critical for security
		panic(fmt.Sprintf("failed to generate secure random state: %v", err))
	}
	return base64.URLEncoding.EncodeToString(b)
}

