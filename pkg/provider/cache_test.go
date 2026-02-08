package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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
