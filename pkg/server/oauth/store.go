package oauth

import (
	"sync"
	"time"
)

// TokenInfo stores information about an OAuth2 token
type TokenInfo struct {
	AccessToken  string    // MCP access token (for LiteLLM → MCP auth)
	SlackToken   string    // Slack OAuth token (for MCP → Slack auth)
	RefreshToken string    // Optional refresh token
	ExpiresAt    time.Time // Token expiration time
	UserID       string    // Slack user ID
	TeamID       string    // Slack team ID
	Scopes       []string  // Granted scopes
}

// Getter methods to implement the auth.TokenInfo interface
func (t *TokenInfo) GetAccessToken() string {
	return t.AccessToken
}

func (t *TokenInfo) GetSlackToken() string {
	return t.SlackToken
}

func (t *TokenInfo) GetUserID() string {
	return t.UserID
}

func (t *TokenInfo) GetTeamID() string {
	return t.TeamID
}

// StateInfo stores OAuth2 state for CSRF protection
type StateInfo struct {
	State     string
	CreatedAt time.Time
}

// Store manages OAuth2 tokens and states
type Store interface {
	// Token operations
	SaveToken(accessToken string, info *TokenInfo) error
	GetToken(accessToken string) (*TokenInfo, error)
	DeleteToken(accessToken string) error

	// State operations (for CSRF protection)
	SaveState(state string) error
	ValidateState(state string) bool
	DeleteState(state string) error

	// Cleanup expired tokens and states
	Cleanup()
}

// InMemoryStore is an in-memory implementation of TokenStore
type InMemoryStore struct {
	tokens map[string]*TokenInfo
	states map[string]*StateInfo
	mu     sync.RWMutex
}

// NewInMemoryStore creates a new in-memory token store
func NewInMemoryStore() *InMemoryStore {
	store := &InMemoryStore{
		tokens: make(map[string]*TokenInfo),
		states: make(map[string]*StateInfo),
	}

	// Start cleanup goroutine
	go store.cleanupLoop()

	return store
}

func (s *InMemoryStore) SaveToken(accessToken string, info *TokenInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[accessToken] = info
	return nil
}

func (s *InMemoryStore) GetToken(accessToken string) (*TokenInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	info, ok := s.tokens[accessToken]
	if !ok {
		return nil, nil
	}

	// Check if token is expired
	if !info.ExpiresAt.IsZero() && time.Now().After(info.ExpiresAt) {
		return nil, nil
	}

	// Return the pointer which implements the interface
	return info, nil
}

func (s *InMemoryStore) DeleteToken(accessToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, accessToken)
	return nil
}

func (s *InMemoryStore) SaveState(state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[state] = &StateInfo{
		State:     state,
		CreatedAt: time.Now(),
	}
	return nil
}

func (s *InMemoryStore) ValidateState(state string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	info, ok := s.states[state]
	if !ok {
		return false
	}

	// State should be used within 10 minutes
	if time.Since(info.CreatedAt) > 10*time.Minute {
		return false
	}

	return true
}

func (s *InMemoryStore) DeleteState(state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, state)
	return nil
}

func (s *InMemoryStore) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	// Cleanup expired tokens
	for token, info := range s.tokens {
		if !info.ExpiresAt.IsZero() && now.After(info.ExpiresAt) {
			delete(s.tokens, token)
		}
	}

	// Cleanup old states (older than 10 minutes)
	for state, info := range s.states {
		if now.Sub(info.CreatedAt) > 10*time.Minute {
			delete(s.states, state)
		}
	}
}

func (s *InMemoryStore) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.Cleanup()
	}
}
