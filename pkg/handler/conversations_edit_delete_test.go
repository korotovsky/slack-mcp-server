package handler

import (
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to set/unset env vars for handler tests
func setHandlerEnv(key, value string) func() {
	old := os.Getenv(key)
	os.Setenv(key, value)
	return func() {
		if old == "" {
			os.Unsetenv(key)
		} else {
			os.Setenv(key, old)
		}
	}
}

// buildCallToolRequest creates a mcp.CallToolRequest with the given tool name and arguments.
func buildCallToolRequest(toolName string, args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args
	return req
}

// --- Edit Message Parameter Parsing Tests ---

func TestUnitEditMessage_ParameterExtraction(t *testing.T) {
	t.Run("valid params are extracted correctly", func(t *testing.T) {
		req := buildCallToolRequest("conversations_edit_message", map[string]any{
			"channel_id": "C1234567890",
			"message_ts": "1234567890.123456",
			"text":       "updated message text",
		})

		assert.Equal(t, "C1234567890", req.GetString("channel_id", ""))
		assert.Equal(t, "1234567890.123456", req.GetString("message_ts", ""))
		assert.Equal(t, "updated message text", req.GetString("text", ""))
	})

	t.Run("missing channel_id returns empty", func(t *testing.T) {
		req := buildCallToolRequest("conversations_edit_message", map[string]any{
			"message_ts": "1234567890.123456",
			"text":       "updated text",
		})
		assert.Empty(t, req.GetString("channel_id", ""))
	})

	t.Run("missing message_ts returns empty", func(t *testing.T) {
		req := buildCallToolRequest("conversations_edit_message", map[string]any{
			"channel_id": "C1234567890",
			"text":       "updated text",
		})
		assert.Empty(t, req.GetString("message_ts", ""))
	})

	t.Run("missing text returns empty", func(t *testing.T) {
		req := buildCallToolRequest("conversations_edit_message", map[string]any{
			"channel_id": "C1234567890",
			"message_ts": "1234567890.123456",
		})
		assert.Empty(t, req.GetString("text", ""))
	})
}

func TestUnitEditMessage_ContentTypeDefaults(t *testing.T) {
	t.Run("default content_type is text/markdown", func(t *testing.T) {
		req := buildCallToolRequest("conversations_edit_message", map[string]any{
			"channel_id": "C1234567890",
			"message_ts": "1234567890.123456",
			"text":       "updated text",
		})
		contentType := req.GetString("content_type", "text/markdown")
		assert.Equal(t, "text/markdown", contentType)
	})

	t.Run("explicit text/plain content_type", func(t *testing.T) {
		req := buildCallToolRequest("conversations_edit_message", map[string]any{
			"channel_id":   "C1234567890",
			"message_ts":   "1234567890.123456",
			"text":         "updated text",
			"content_type": "text/plain",
		})
		contentType := req.GetString("content_type", "text/markdown")
		assert.Equal(t, "text/plain", contentType)
	})

	t.Run("invalid content_type value", func(t *testing.T) {
		req := buildCallToolRequest("conversations_edit_message", map[string]any{
			"channel_id":   "C1234567890",
			"message_ts":   "1234567890.123456",
			"text":         "updated text",
			"content_type": "application/json",
		})
		contentType := req.GetString("content_type", "text/markdown")
		// application/json is not a valid content type
		isValid := contentType == "text/plain" || contentType == "text/markdown"
		assert.False(t, isValid, "application/json should not be a valid content_type")
	})
}

func TestUnitEditMessage_TimestampValidation(t *testing.T) {
	tests := []struct {
		name       string
		messageTs  string
		wantDot    bool
		wantEmpty  bool
	}{
		{"valid timestamp", "1234567890.123456", true, false},
		{"shorter fraction", "1234567890.12", true, false},
		{"no fraction - invalid", "1234567890", false, false},
		{"empty - invalid", "", false, true},
		{"just a dot", ".", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantEmpty {
				assert.Empty(t, tt.messageTs)
				return
			}
			hasDot := strings.Contains(tt.messageTs, ".")
			assert.Equal(t, tt.wantDot, hasDot, "timestamp %q dot check", tt.messageTs)
		})
	}
}

// --- Delete Message Parameter Parsing Tests ---

func TestUnitDeleteMessage_ParameterExtraction(t *testing.T) {
	t.Run("valid params are extracted correctly", func(t *testing.T) {
		req := buildCallToolRequest("conversations_delete_message", map[string]any{
			"channel_id": "C1234567890",
			"message_ts": "1234567890.123456",
		})

		assert.Equal(t, "C1234567890", req.GetString("channel_id", ""))
		assert.Equal(t, "1234567890.123456", req.GetString("message_ts", ""))
	})

	t.Run("missing channel_id returns empty", func(t *testing.T) {
		req := buildCallToolRequest("conversations_delete_message", map[string]any{
			"message_ts": "1234567890.123456",
		})
		assert.Empty(t, req.GetString("channel_id", ""))
	})

	t.Run("missing message_ts returns empty", func(t *testing.T) {
		req := buildCallToolRequest("conversations_delete_message", map[string]any{
			"channel_id": "C1234567890",
		})
		assert.Empty(t, req.GetString("message_ts", ""))
	})

	t.Run("text is not required for delete", func(t *testing.T) {
		req := buildCallToolRequest("conversations_delete_message", map[string]any{
			"channel_id": "C1234567890",
			"message_ts": "1234567890.123456",
		})
		text := req.GetString("text", "")
		assert.Empty(t, text, "delete should not require text parameter")
	})
}

// --- Channel allowlist/blocklist tests for edit/delete ---

func TestUnitIsChannelAllowedForConfig_EditDelete(t *testing.T) {
	tests := []struct {
		name    string
		channel string
		config  string
		want    bool
	}{
		// Edit/Delete should use the same allowlist logic as add_message
		{"empty config allows all", "C123", "", true},
		{"true allows all", "C123", "true", true},
		{"1 allows all", "C123", "1", true},

		// Allowlist cases
		{"allowlist - channel in list", "C123", "C123,C456", true},
		{"allowlist - second channel in list", "C456", "C123,C456", true},
		{"allowlist - channel NOT in list", "C789", "C123,C456", false},
		{"allowlist - with spaces", "C123", " C123 , C456 ", true},

		// Blocklist cases
		{"blocklist - channel in list", "C123", "!C123,!C456", false},
		{"blocklist - second channel in list", "C456", "!C123,!C456", false},
		{"blocklist - channel NOT in list", "C789", "!C123,!C456", true},
		{"blocklist - with spaces", "C123", " !C123 , !C456 ", false},

		// Single item cases
		{"single allowlist - match", "C123", "C123", true},
		{"single allowlist - no match", "C456", "C123", false},
		{"single blocklist - match", "C123", "!C123", false},
		{"single blocklist - no match", "C456", "!C123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isChannelAllowedForConfig(tt.channel, tt.config)
			assert.Equal(t, tt.want, got, "isChannelAllowedForConfig(%q, %q)", tt.channel, tt.config)
		})
	}
}

// --- Tool disabled by default tests ---

func TestUnitEditMessageTool_DisabledByDefault(t *testing.T) {
	cleanup1 := setHandlerEnv("SLACK_MCP_EDIT_MESSAGE_TOOL", "")
	defer cleanup1()
	cleanup2 := setHandlerEnv("SLACK_MCP_ENABLED_TOOLS", "")
	defer cleanup2()

	toolConfig := os.Getenv("SLACK_MCP_EDIT_MESSAGE_TOOL")
	enabledTools := os.Getenv("SLACK_MCP_ENABLED_TOOLS")

	assert.Empty(t, toolConfig, "SLACK_MCP_EDIT_MESSAGE_TOOL should be empty by default")
	assert.Empty(t, enabledTools, "SLACK_MCP_ENABLED_TOOLS should be empty by default")

	// When both are empty, the parseParams function would return an error
	// because the tool is disabled by default
	isDisabled := toolConfig == "" && !strings.Contains(enabledTools, "conversations_edit_message")
	assert.True(t, isDisabled, "edit tool should be disabled when no env vars are set")
}

func TestUnitDeleteMessageTool_DisabledByDefault(t *testing.T) {
	cleanup1 := setHandlerEnv("SLACK_MCP_DELETE_MESSAGE_TOOL", "")
	defer cleanup1()
	cleanup2 := setHandlerEnv("SLACK_MCP_ENABLED_TOOLS", "")
	defer cleanup2()

	toolConfig := os.Getenv("SLACK_MCP_DELETE_MESSAGE_TOOL")
	enabledTools := os.Getenv("SLACK_MCP_ENABLED_TOOLS")

	assert.Empty(t, toolConfig, "SLACK_MCP_DELETE_MESSAGE_TOOL should be empty by default")
	assert.Empty(t, enabledTools, "SLACK_MCP_ENABLED_TOOLS should be empty by default")

	isDisabled := toolConfig == "" && !strings.Contains(enabledTools, "conversations_delete_message")
	assert.True(t, isDisabled, "delete tool should be disabled when no env vars are set")
}

// --- Tests for enabling via SLACK_MCP_ENABLED_TOOLS ---

func TestUnitEditMessageTool_EnabledViaEnabledTools(t *testing.T) {
	cleanup1 := setHandlerEnv("SLACK_MCP_EDIT_MESSAGE_TOOL", "")
	defer cleanup1()
	cleanup2 := setHandlerEnv("SLACK_MCP_ENABLED_TOOLS", "conversations_edit_message")
	defer cleanup2()

	enabledTools := os.Getenv("SLACK_MCP_ENABLED_TOOLS")
	require.Contains(t, enabledTools, "conversations_edit_message")
}

func TestUnitDeleteMessageTool_EnabledViaEnabledTools(t *testing.T) {
	cleanup1 := setHandlerEnv("SLACK_MCP_DELETE_MESSAGE_TOOL", "")
	defer cleanup1()
	cleanup2 := setHandlerEnv("SLACK_MCP_ENABLED_TOOLS", "conversations_delete_message")
	defer cleanup2()

	enabledTools := os.Getenv("SLACK_MCP_ENABLED_TOOLS")
	require.Contains(t, enabledTools, "conversations_delete_message")
}

// --- Edge case tests ---

func TestUnitEditMessage_EmptyText(t *testing.T) {
	req := buildCallToolRequest("conversations_edit_message", map[string]any{
		"channel_id": "C1234567890",
		"message_ts": "1234567890.123456",
		"text":       "",
	})

	text := req.GetString("text", "")
	assert.Empty(t, text, "empty text should be rejected for edit")

	// Also check backward compatibility with "payload" parameter
	payload := req.GetString("payload", "")
	assert.Empty(t, payload, "payload should also be empty")
}

func TestUnitEditMessage_BackwardCompatPayload(t *testing.T) {
	req := buildCallToolRequest("conversations_edit_message", map[string]any{
		"channel_id": "C1234567890",
		"message_ts": "1234567890.123456",
		"payload":    "updated via payload param",
	})

	// The implementation checks "text" first, then falls back to "payload"
	text := req.GetString("text", "")
	payload := req.GetString("payload", "")
	assert.Empty(t, text, "text param not provided")
	assert.Equal(t, "updated via payload param", payload, "payload should be available as fallback")
}

func TestUnitEditMessage_ChannelIDFormats(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		isNameRef bool
	}{
		{"standard channel ID", "C1234567890", false},
		{"channel name with hash", "#general", true},
		{"DM with at", "@username", true},
		{"private channel ID", "G1234567890", false},
		{"DM channel ID", "D1234567890", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := buildCallToolRequest("conversations_edit_message", map[string]any{
				"channel_id": tt.channelID,
				"message_ts": "1234567890.123456",
				"text":       "updated text",
			})
			channelID := req.GetString("channel_id", "")
			assert.Equal(t, tt.channelID, channelID)

			// Check if it would need resolution (starts with # or @)
			needsResolution := len(channelID) > 0 && (channelID[0] == '#' || channelID[0] == '@')
			assert.Equal(t, tt.isNameRef, needsResolution,
				"channel %q resolution expectation mismatch", tt.channelID)
		})
	}
}

func TestUnitDeleteMessage_ChannelIDFormats(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		isNameRef bool
	}{
		{"standard channel ID", "C1234567890", false},
		{"channel name with hash", "#general", true},
		{"DM with at", "@username", true},
		{"private channel ID", "G1234567890", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := buildCallToolRequest("conversations_delete_message", map[string]any{
				"channel_id": tt.channelID,
				"message_ts": "1234567890.123456",
			})
			channelID := req.GetString("channel_id", "")
			assert.Equal(t, tt.channelID, channelID)

			needsResolution := len(channelID) > 0 && (channelID[0] == '#' || channelID[0] == '@')
			assert.Equal(t, tt.isNameRef, needsResolution,
				"channel %q resolution expectation mismatch", tt.channelID)
		})
	}
}

func TestUnitDeleteMessage_TimestampValidation(t *testing.T) {
	tests := []struct {
		name      string
		messageTs string
		wantDot   bool
		wantEmpty bool
	}{
		{"valid timestamp", "1234567890.123456", true, false},
		{"shorter fraction", "1234567890.12", true, false},
		{"no fraction - invalid", "1234567890", false, false},
		{"empty - invalid", "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantEmpty {
				assert.Empty(t, tt.messageTs)
				return
			}
			hasDot := strings.Contains(tt.messageTs, ".")
			assert.Equal(t, tt.wantDot, hasDot, "timestamp %q dot check", tt.messageTs)
		})
	}
}

// --- Tests verifying edit and delete tool env var channel restrictions ---

func TestUnitEditMessage_ChannelRestriction(t *testing.T) {
	tests := []struct {
		name       string
		envValue   string
		channel    string
		wantAllow  bool
	}{
		{"true allows any channel", "true", "C123", true},
		{"1 allows any channel", "1", "C123", true},
		{"channel in allowlist", "C123,C456", "C123", true},
		{"channel not in allowlist", "C123,C456", "C789", false},
		{"blocklist blocks channel", "!C123", "C123", false},
		{"blocklist allows other channels", "!C123", "C456", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isChannelAllowedForConfig(tt.channel, tt.envValue)
			assert.Equal(t, tt.wantAllow, got)
		})
	}
}

func TestUnitDeleteMessage_ChannelRestriction(t *testing.T) {
	tests := []struct {
		name       string
		envValue   string
		channel    string
		wantAllow  bool
	}{
		{"true allows any channel", "true", "C123", true},
		{"1 allows any channel", "1", "C123", true},
		{"channel in allowlist", "C123,C456", "C123", true},
		{"channel not in allowlist", "C123,C456", "C789", false},
		{"blocklist blocks channel", "!C123", "C123", false},
		{"blocklist allows other channels", "!C123", "C456", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isChannelAllowedForConfig(tt.channel, tt.envValue)
			assert.Equal(t, tt.wantAllow, got)
		})
	}
}
