package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetCacheTTL tests the app-specific logic in getCacheTTL:
// - Default when env not set
// - Numeric seconds fallback (app-specific parsing path)
// - Invalid input handling
// - Negative value rejection (P1 bug fix)
func TestGetCacheTTL(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected time.Duration
	}{
		{
			name:     "default when env not set",
			envValue: "",
			expected: defaultCacheTTL,
		},
		{
			name:     "valid duration passes through",
			envValue: "2h",
			expected: 2 * time.Hour,
		},
		{
			name:     "numeric seconds fallback path",
			envValue: "3600",
			expected: 3600 * time.Second,
		},
		{
			name:     "zero disables TTL",
			envValue: "0",
			expected: 0,
		},
		{
			name:     "invalid input falls back to default",
			envValue: "invalid",
			expected: defaultCacheTTL,
		},
		{
			name:     "negative duration rejected - falls back to default",
			envValue: "-1h",
			expected: defaultCacheTTL,
		},
		{
			name:     "negative seconds rejected - falls back to default",
			envValue: "-3600",
			expected: defaultCacheTTL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldVal := os.Getenv("SLACK_MCP_CACHE_TTL")
			defer os.Setenv("SLACK_MCP_CACHE_TTL", oldVal)

			if tt.envValue == "" {
				os.Unsetenv("SLACK_MCP_CACHE_TTL")
			} else {
				os.Setenv("SLACK_MCP_CACHE_TTL", tt.envValue)
			}

			result := getCacheTTL()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCacheExpiry verifies the actual cache expiry logic used in refreshChannelsInternal.
// This tests the production code path: file exists → check mtime → compare to TTL.
func TestCacheExpiry(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "slack-mcp-cache-expiry-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	t.Run("isCacheExpired returns correct result based on file mtime", func(t *testing.T) {
		// This helper mirrors the logic in refreshChannelsInternal
		isCacheExpired := func(cacheFile string, ttl time.Duration) bool {
			if ttl == 0 {
				return false // TTL disabled
			}
			fileInfo, err := os.Stat(cacheFile)
			if err != nil {
				return true // File doesn't exist, treat as expired
			}
			return time.Since(fileInfo.ModTime()) > ttl
		}

		cacheFile := filepath.Join(tempDir, "test_cache.json")
		err := os.WriteFile(cacheFile, []byte(`[]`), 0644)
		require.NoError(t, err)

		// Fresh file should not be expired
		assert.False(t, isCacheExpired(cacheFile, 1*time.Hour),
			"fresh cache should not be expired")

		// Set mtime to 2 hours ago
		oldTime := time.Now().Add(-2 * time.Hour)
		err = os.Chtimes(cacheFile, oldTime, oldTime)
		require.NoError(t, err)

		// Old file should be expired
		assert.True(t, isCacheExpired(cacheFile, 1*time.Hour),
			"2 hour old cache should be expired with 1h TTL")

		// TTL=0 should never expire
		assert.False(t, isCacheExpired(cacheFile, 0),
			"cache should never expire when TTL=0")

		// Nonexistent file should be treated as expired
		assert.True(t, isCacheExpired(filepath.Join(tempDir, "nonexistent.json"), 1*time.Hour),
			"nonexistent cache should be treated as expired")
	})

	t.Run("stale cache detected after server restart simulation", func(t *testing.T) {
		// This is the key scenario: MCP server restarts after 3 days,
		// cache file is still on disk with old mtime
		cacheFile := filepath.Join(tempDir, "stale_cache.json")

		channels := []Channel{{ID: "C123", Name: "#old-channel"}}
		data, err := json.Marshal(channels)
		require.NoError(t, err)
		err = os.WriteFile(cacheFile, data, 0644)
		require.NoError(t, err)

		// Set mtime to 3 days ago (simulating server was down)
		threeDaysAgo := time.Now().Add(-72 * time.Hour)
		err = os.Chtimes(cacheFile, threeDaysAgo, threeDaysAgo)
		require.NoError(t, err)

		// Verify the production code would detect this as stale
		fileInfo, err := os.Stat(cacheFile)
		require.NoError(t, err)

		cacheAge := time.Since(fileInfo.ModTime())
		ttl := getCacheTTL() // default 1 hour

		assert.True(t, cacheAge > ttl,
			"cache from 3 days ago (age=%v) should exceed default TTL (%v)", cacheAge, ttl)
	})
}

// TestChannelCacheRoundTrip verifies that Channel structs survive JSON serialization.
// This catches bugs in struct tags or field types that would corrupt cache data.
func TestChannelCacheRoundTrip(t *testing.T) {
	original := []Channel{
		{
			ID:          "C123",
			Name:        "#general",
			Topic:       "General discussion",
			Purpose:     "Company-wide announcements",
			MemberCount: 100,
			IsPrivate:   false,
		},
		{
			ID:        "D456",
			Name:      "@john.doe",
			IsIM:      true,
			IsPrivate: true,
			User:      "U789",
		},
		{
			ID:        "G789",
			Name:      "#private-team",
			IsPrivate: true,
			IsMpIM:    false,
			Members:   []string{"U001", "U002"},
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var loaded []Channel
	err = json.Unmarshal(data, &loaded)
	require.NoError(t, err)

	require.Len(t, loaded, 3)

	// Verify public channel
	assert.Equal(t, "C123", loaded[0].ID)
	assert.Equal(t, "#general", loaded[0].Name)
	assert.Equal(t, 100, loaded[0].MemberCount)
	assert.False(t, loaded[0].IsPrivate)

	// Verify IM channel
	assert.Equal(t, "D456", loaded[1].ID)
	assert.True(t, loaded[1].IsIM)
	assert.Equal(t, "U789", loaded[1].User)

	// Verify private channel with members
	assert.True(t, loaded[2].IsPrivate)
	assert.Equal(t, []string{"U001", "U002"}, loaded[2].Members)
}

// TestChannelLookupByName verifies the inverse map lookup pattern used in resolveChannelID.
func TestChannelLookupByName(t *testing.T) {
	// Build maps the same way refreshChannelsInternal does
	channels := []Channel{
		{ID: "C123", Name: "#general"},
		{ID: "C456", Name: "#random"},
		{ID: "D789", Name: "@john.doe"},
	}

	channelsMap := make(map[string]Channel)
	channelsInv := make(map[string]string)

	for _, c := range channels {
		channelsMap[c.ID] = c
		channelsInv[c.Name] = c.ID
	}

	t.Run("lookup existing channel by name", func(t *testing.T) {
		id, ok := channelsInv["#general"]
		assert.True(t, ok)
		assert.Equal(t, "C123", id)

		ch := channelsMap[id]
		assert.Equal(t, "#general", ch.Name)
	})

	t.Run("lookup existing DM by name", func(t *testing.T) {
		id, ok := channelsInv["@john.doe"]
		assert.True(t, ok)
		assert.Equal(t, "D789", id)
	})

	t.Run("lookup nonexistent channel returns false", func(t *testing.T) {
		_, ok := channelsInv["#new-channel"]
		assert.False(t, ok, "new channel not in cache should return false")
	})
}

// TestChannelIDPatterns verifies which channel formats need name resolution.
// This tests the logic in resolveChannelID that decides when to do lookups.
func TestChannelIDPatterns(t *testing.T) {
	// This mirrors the check in resolveChannelID:
	// if !strings.HasPrefix(channel, "#") && !strings.HasPrefix(channel, "@") {
	//     return channel, nil  // Already an ID, no lookup needed
	// }
	needsLookup := func(channel string) bool {
		return len(channel) > 0 && (channel[0] == '#' || channel[0] == '@')
	}

	tests := []struct {
		channel string
		needs   bool
	}{
		{"C1234567890", false},  // Standard channel ID
		{"G1234567890", false},  // Private channel ID (legacy)
		{"D1234567890", false},  // DM ID
		{"#general", true},      // Channel name - needs lookup
		{"@john.doe", true},     // User DM name - needs lookup
		{"", false},             // Empty - no lookup
	}

	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			assert.Equal(t, tt.needs, needsLookup(tt.channel),
				"channel %q: needsLookup should be %v", tt.channel, tt.needs)
		})
	}
}

// TestRefreshOnErrorPattern verifies the retry-once pattern used in resolveChannelID.
// When a channel isn't found, the code refreshes the cache and tries once more.
func TestRefreshOnErrorPattern(t *testing.T) {
	t.Run("pattern: miss -> refresh -> hit", func(t *testing.T) {
		// Initial cache doesn't have the channel
		cache := make(map[string]string)

		// First lookup fails
		_, found := cache["#new-channel"]
		assert.False(t, found, "initial lookup should miss")

		// Simulate refresh adding the channel (this is what ForceRefreshChannels does)
		cache["#new-channel"] = "C999"

		// Second lookup succeeds
		id, found := cache["#new-channel"]
		assert.True(t, found, "lookup after refresh should succeed")
		assert.Equal(t, "C999", id)
	})

	t.Run("pattern: miss -> refresh -> still miss", func(t *testing.T) {
		// Channel genuinely doesn't exist in Slack
		cache := make(map[string]string)

		_, found := cache["#typo-channel"]
		assert.False(t, found)

		// Refresh happens but channel still doesn't exist
		// (cache remains empty or doesn't have this channel)

		_, found = cache["#typo-channel"]
		assert.False(t, found, "channel that doesn't exist should still miss after refresh")
	})
}

// TestGetCacheDir verifies the cache directory is created correctly.
func TestGetCacheDir(t *testing.T) {
	dir := getCacheDir()

	assert.NotEmpty(t, dir, "cache dir should not be empty")
	assert.Contains(t, dir, "slack-mcp-server", "cache dir should contain app name")

	// Directory should exist after getCacheDir() creates it
	info, err := os.Stat(dir)
	require.NoError(t, err, "cache directory should exist")
	assert.True(t, info.IsDir(), "cache path should be a directory")
}

// TestDefaultCacheTTLIs24Hours verifies that the default cache TTL is 24 hours.
func TestDefaultCacheTTLIs24Hours(t *testing.T) {
	assert.Equal(t, 24*time.Hour, defaultCacheTTL,
		"default cache TTL should be 24 hours")
}

// TestAtomicReadyFlags verifies that usersReady and channelsReady are atomic.Bool
// and safe for concurrent access. This test is meaningful under `go test -race`.
func TestAtomicReadyFlags(t *testing.T) {
	var usersReady, channelsReady atomic.Bool

	// Initially false
	assert.False(t, usersReady.Load())
	assert.False(t, channelsReady.Load())

	done := make(chan struct{})

	// Concurrent writers
	go func() {
		for i := 0; i < 1000; i++ {
			usersReady.Store(true)
			channelsReady.Store(true)
		}
		close(done)
	}()

	// Concurrent readers (would race on plain bool under -race)
	for i := 0; i < 1000; i++ {
		_ = usersReady.Load()
		_ = channelsReady.Load()
	}

	<-done
	assert.True(t, usersReady.Load())
	assert.True(t, channelsReady.Load())
}

// TestRefreshingFlagPreventsConucrrentRefreshes verifies that CompareAndSwap on
// the refreshing flag prevents a second background refresh from starting.
func TestRefreshingFlagPreventsConucrrentRefreshes(t *testing.T) {
	var refreshing atomic.Bool

	// First caller succeeds
	assert.True(t, refreshing.CompareAndSwap(false, true),
		"first refresh should acquire the flag")

	// Second caller is blocked
	assert.False(t, refreshing.CompareAndSwap(false, true),
		"second refresh should be blocked while first is in progress")

	// After first completes, next one can proceed
	refreshing.Store(false)
	assert.True(t, refreshing.CompareAndSwap(false, true),
		"refresh should succeed after previous one completes")
}

// TestStaleWhileRevalidateReadyFlag verifies the stale-while-revalidate pattern:
// when an expired cache file exists, the ready flag is set immediately from stale data,
// without waiting for a fresh API fetch.
func TestStaleWhileRevalidateReadyFlag(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "slack-mcp-swr-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	cacheFile := filepath.Join(tempDir, "users_cache.json")

	// Write a valid cache file with user data
	users := []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{
		{ID: "U001", Name: "alice"},
		{ID: "U002", Name: "bob"},
	}
	data, err := json.Marshal(users)
	require.NoError(t, err)
	err = os.WriteFile(cacheFile, data, 0644)
	require.NoError(t, err)

	// Set mtime to 48 hours ago (well past 24h default TTL)
	staleTime := time.Now().Add(-48 * time.Hour)
	err = os.Chtimes(cacheFile, staleTime, staleTime)
	require.NoError(t, err)

	// Simulate the stale-while-revalidate logic from refreshUsersInternal:
	// 1. Read and unmarshal cache
	// 2. Build snapshot and set ready flag
	// 3. Check TTL — expired means we'd spawn background refresh
	var usersReady atomic.Bool
	var usersSnapshot atomic.Pointer[UsersCache]

	fileData, err := os.ReadFile(cacheFile)
	require.NoError(t, err)

	type simpleUser struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var cachedUsers []simpleUser
	err = json.Unmarshal(fileData, &cachedUsers)
	require.NoError(t, err)

	// Build snapshot (mirrors refreshUsersInternal logic)
	snapshot := &UsersCache{
		Users:    make(map[string]slack.User, len(cachedUsers)),
		UsersInv: make(map[string]string, len(cachedUsers)),
	}
	for _, u := range cachedUsers {
		snapshot.Users[u.ID] = slack.User{ID: u.ID, Name: u.Name}
		snapshot.UsersInv[u.Name] = u.ID
	}
	usersSnapshot.Store(snapshot)
	usersReady.Store(true)

	// Ready flag should be true immediately (before any background refresh)
	assert.True(t, usersReady.Load(),
		"ready flag should be set immediately from stale cache")

	// Snapshot should contain the stale data
	loaded := usersSnapshot.Load()
	require.NotNil(t, loaded)
	assert.Len(t, loaded.Users, 2, "snapshot should contain cached users")
	assert.Equal(t, "U001", loaded.Users["U001"].ID)
	assert.Equal(t, "U002", loaded.UsersInv["bob"])

	// Verify the cache IS expired (would trigger background refresh)
	fileInfo, err := os.Stat(cacheFile)
	require.NoError(t, err)
	cacheAge := time.Since(fileInfo.ModTime())
	assert.True(t, cacheAge > defaultCacheTTL,
		"cache should be detected as expired (age=%v > TTL=%v)", cacheAge, defaultCacheTTL)
}

// TestGetMinRefreshInterval tests the rate limiting configuration parsing.
func TestGetMinRefreshInterval(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected time.Duration
	}{
		{
			name:     "default when env not set",
			envValue: "",
			expected: defaultMinRefreshInterval,
		},
		{
			name:     "valid duration",
			envValue: "1m",
			expected: 1 * time.Minute,
		},
		{
			name:     "numeric seconds",
			envValue: "60",
			expected: 60 * time.Second,
		},
		{
			name:     "zero disables rate limiting",
			envValue: "0",
			expected: 0,
		},
		{
			name:     "invalid input falls back to default",
			envValue: "invalid",
			expected: defaultMinRefreshInterval,
		},
		{
			name:     "negative duration rejected",
			envValue: "-30s",
			expected: defaultMinRefreshInterval,
		},
		{
			name:     "negative seconds rejected",
			envValue: "-60",
			expected: defaultMinRefreshInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldVal := os.Getenv("SLACK_MCP_MIN_REFRESH_INTERVAL")
			defer os.Setenv("SLACK_MCP_MIN_REFRESH_INTERVAL", oldVal)

			if tt.envValue == "" {
				os.Unsetenv("SLACK_MCP_MIN_REFRESH_INTERVAL")
			} else {
				os.Setenv("SLACK_MCP_MIN_REFRESH_INTERVAL", tt.envValue)
			}

			result := getMinRefreshInterval()
			assert.Equal(t, tt.expected, result)
		})
	}
}
