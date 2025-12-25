package oauth

import (
	"fmt"
	"sync"
)

// MemoryStorage is an in-memory implementation of TokenStorage
type MemoryStorage struct {
	mu     sync.RWMutex
	tokens map[string]*TokenResponse
}

// NewMemoryStorage creates a new in-memory token storage
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		tokens: make(map[string]*TokenResponse),
	}
}

// Store saves a token for a user
func (s *MemoryStorage) Store(userID string, token *TokenResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[userID] = token
	return nil
}

// Get retrieves a token for a user
func (s *MemoryStorage) Get(userID string) (*TokenResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	token, ok := s.tokens[userID]
	if !ok {
		return nil, fmt.Errorf("token not found for user %s", userID)
	}

	return token, nil
}


