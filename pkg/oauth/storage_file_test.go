package oauth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestToken(accessToken, userID, teamID string) *TokenResponse {
	return &TokenResponse{
		AccessToken: accessToken,
		BotToken:    "xoxb-bot-" + userID,
		UserID:      userID,
		TeamID:      teamID,
		BotUserID:   "B" + userID,
		ExpiresAt:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestNewFileStorage_DefaultPath(t *testing.T) {
	storage := NewFileStorage("")
	assert.NotEmpty(t, storage.path, "default path should not be empty")
	assert.Contains(t, storage.path, "token.json", "default path should end with token.json")
	assert.Contains(t, storage.path, ".slack-mcp", "default path should include .slack-mcp directory")
}

func TestNewFileStorage_CustomPath(t *testing.T) {
	customPath := "/tmp/my-custom/tokens.json"
	storage := NewFileStorage(customPath)
	assert.Equal(t, customPath, storage.path, "custom path should be used as-is")
}

func TestFileStorage_StoreAndGet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	storage := NewFileStorage(path)

	token := newTestToken("xoxp-access-123", "U123", "T456")

	err := storage.Store("U123", token)
	require.NoError(t, err, "Store should not return an error")

	got, err := storage.Get("U123")
	require.NoError(t, err, "Get should not return an error")

	assert.Equal(t, token.AccessToken, got.AccessToken)
	assert.Equal(t, token.BotToken, got.BotToken)
	assert.Equal(t, token.UserID, got.UserID)
	assert.Equal(t, token.TeamID, got.TeamID)
	assert.Equal(t, token.BotUserID, got.BotUserID)
	assert.True(t, token.ExpiresAt.Equal(got.ExpiresAt), "ExpiresAt should match")
}

func TestFileStorage_GetNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	storage := NewFileStorage(path)

	_, err := storage.Get("UNONEXISTENT")
	require.Error(t, err, "Get for non-existent user should return an error")
	assert.Contains(t, err.Error(), "token not found for user UNONEXISTENT")
}

func TestFileStorage_HasToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	storage := NewFileStorage(path)

	assert.False(t, storage.HasToken("U123"), "HasToken should return false before storing")

	token := newTestToken("xoxp-access-123", "U123", "T456")
	err := storage.Store("U123", token)
	require.NoError(t, err)

	assert.True(t, storage.HasToken("U123"), "HasToken should return true after storing")
	assert.False(t, storage.HasToken("U999"), "HasToken should return false for a different user")
}

func TestFileStorage_GetAnyToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	storage := NewFileStorage(path)

	// Empty storage should return error
	_, err := storage.GetAnyToken()
	require.Error(t, err, "GetAnyToken on empty storage should return an error")
	assert.Contains(t, err.Error(), "no tokens found")

	// Store a token and verify GetAnyToken returns it
	token := newTestToken("xoxp-access-123", "U123", "T456")
	err = storage.Store("U123", token)
	require.NoError(t, err)

	got, err := storage.GetAnyToken()
	require.NoError(t, err, "GetAnyToken should succeed when a token exists")
	assert.Equal(t, token.AccessToken, got.AccessToken)
}

func TestFileStorage_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")

	// Store with one instance
	storage1 := NewFileStorage(path)
	token := newTestToken("xoxp-persist-123", "U100", "T200")
	err := storage1.Store("U100", token)
	require.NoError(t, err)

	// Read with a completely new instance pointing to the same file
	storage2 := NewFileStorage(path)
	got, err := storage2.Get("U100")
	require.NoError(t, err, "new FileStorage instance should read persisted token")
	assert.Equal(t, token.AccessToken, got.AccessToken)
	assert.Equal(t, token.UserID, got.UserID)
	assert.Equal(t, token.TeamID, got.TeamID)
}

func TestFileStorage_MergeTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	storage := NewFileStorage(path)

	token1 := newTestToken("xoxp-access-aaa", "UAAA", "T1")
	token2 := newTestToken("xoxp-access-bbb", "UBBB", "T1")
	token3 := newTestToken("xoxp-access-ccc", "UCCC", "T2")

	require.NoError(t, storage.Store("UAAA", token1))
	require.NoError(t, storage.Store("UBBB", token2))
	require.NoError(t, storage.Store("UCCC", token3))

	got1, err := storage.Get("UAAA")
	require.NoError(t, err)
	assert.Equal(t, "xoxp-access-aaa", got1.AccessToken)

	got2, err := storage.Get("UBBB")
	require.NoError(t, err)
	assert.Equal(t, "xoxp-access-bbb", got2.AccessToken)

	got3, err := storage.Get("UCCC")
	require.NoError(t, err)
	assert.Equal(t, "xoxp-access-ccc", got3.AccessToken)

	// All three should be present
	assert.True(t, storage.HasToken("UAAA"))
	assert.True(t, storage.HasToken("UBBB"))
	assert.True(t, storage.HasToken("UCCC"))
}

func TestFileStorage_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	storage := NewFileStorage(path)

	token := newTestToken("xoxp-access-perms", "U999", "T999")
	err := storage.Store("U999", token)
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err, "token file should exist after Store")
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), "token file should have 0600 permissions")
}
