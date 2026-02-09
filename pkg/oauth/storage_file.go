package oauth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileStorage is a file-based implementation of TokenStorage
type FileStorage struct {
	mu   sync.RWMutex
	path string
}

// NewFileStorage creates a new file-based token storage.
// If path is empty, defaults to ~/.slack-mcp/token.json.
func NewFileStorage(path string) *FileStorage {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		path = filepath.Join(home, ".slack-mcp", "token.json")
	}
	return &FileStorage{path: path}
}

// Store saves a token for a user, merging it into the existing file.
func (s *FileStorage) Store(userID string, token *TokenResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tokens, err := s.readFile()
	if err != nil {
		return fmt.Errorf("reading token file: %w", err)
	}

	tokens[userID] = token

	return s.writeFile(tokens)
}

// Get retrieves a token for a user by reading from the file.
func (s *FileStorage) Get(userID string) (*TokenResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tokens, err := s.readFile()
	if err != nil {
		return nil, fmt.Errorf("reading token file: %w", err)
	}

	token, ok := tokens[userID]
	if !ok {
		return nil, fmt.Errorf("token not found for user %s", userID)
	}

	return token, nil
}

// HasToken checks if a valid token exists for the user.
func (s *FileStorage) HasToken(userID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tokens, err := s.readFile()
	if err != nil {
		return false
	}

	_, ok := tokens[userID]
	return ok
}

// GetAnyToken returns the first available token.
// Useful for single-user CLI mode where the userID is not known upfront.
func (s *FileStorage) GetAnyToken() (*TokenResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tokens, err := s.readFile()
	if err != nil {
		return nil, fmt.Errorf("reading token file: %w", err)
	}

	for _, token := range tokens {
		return token, nil
	}

	return nil, fmt.Errorf("no tokens found")
}

// readFile reads and parses the token file. Returns an empty map if the file doesn't exist.
// Caller must hold at least a read lock.
func (s *FileStorage) readFile() (map[string]*TokenResponse, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*TokenResponse), nil
		}
		return nil, err
	}

	tokens := make(map[string]*TokenResponse)
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("parsing token file: %w", err)
	}

	return tokens, nil
}

// writeFile writes the token map to the file, creating the directory if needed.
// Caller must hold the write lock.
func (s *FileStorage) writeFile(tokens map[string]*TokenResponse) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling tokens: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("writing token file: %w", err)
	}

	return nil
}
