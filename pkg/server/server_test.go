package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShouldAddTool_EmptyEnabledTools(t *testing.T) {
	// All available tools
	allTools := []string{
		"conversations_history",
		"conversations_replies",
		"conversations_search_messages",
		"channels_list",
		"attachment_get_data",
		"conversations_add_message",
		"reactions_add",
		"reactions_remove",
	}

	t.Run("all tools registered with empty enabledTools", func(t *testing.T) {
		for _, tool := range allTools {
			result := shouldAddTool(tool, []string{})
			assert.True(t, result, "tool %s should be registered when enabledTools is empty", tool)
		}
	})

	t.Run("all tools registered with nil enabledTools", func(t *testing.T) {
		for _, tool := range allTools {
			result := shouldAddTool(tool, nil)
			assert.True(t, result, "tool %s should be registered when enabledTools is nil", tool)
		}
	})

	t.Run("unknown tools also registered with empty enabledTools", func(t *testing.T) {
		result := shouldAddTool("future_new_tool", []string{})
		assert.True(t, result, "unknown tools should be registered when enabledTools is empty")
	})
}

func TestShouldAddTool_ExplicitEnabledTools(t *testing.T) {
	tests := []struct {
		name         string
		toolName     string
		enabledTools []string
		expected     bool
	}{
		{
			name:         "tool in enabledTools list is registered",
			toolName:     "conversations_history",
			enabledTools: []string{"conversations_history", "channels_list"},
			expected:     true,
		},
		{
			name:         "tool not in enabledTools list is not registered",
			toolName:     "conversations_add_message",
			enabledTools: []string{"conversations_history", "channels_list"},
			expected:     false,
		},
		{
			name:         "write tool can be explicitly enabled",
			toolName:     "conversations_add_message",
			enabledTools: []string{"conversations_add_message"},
			expected:     true,
		},
		{
			name:         "read-only tool blocked when not in explicit list",
			toolName:     "conversations_history",
			enabledTools: []string{"channels_list"},
			expected:     false,
		},
		{
			name:         "unknown tool allowed when in explicit enabledTools",
			toolName:     "future_new_tool",
			enabledTools: []string{"future_new_tool"},
			expected:     true,
		},
		{
			name:         "unknown tool blocked when not in explicit enabledTools",
			toolName:     "future_new_tool",
			enabledTools: []string{"conversations_history"},
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldAddTool(tt.toolName, tt.enabledTools)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShouldAddTool_SingleToolEnabled(t *testing.T) {
	allTools := []string{
		"conversations_history",
		"conversations_replies",
		"conversations_search_messages",
		"channels_list",
		"attachment_get_data",
		"conversations_add_message",
		"reactions_add",
		"reactions_remove",
	}

	// When only one tool is enabled, only that tool should be registered
	enabledTools := []string{"channels_list"}

	for _, tool := range allTools {
		result := shouldAddTool(tool, enabledTools)
		if tool == "channels_list" {
			assert.True(t, result, "channels_list should be registered")
		} else {
			assert.False(t, result, "%s should NOT be registered when only channels_list is enabled", tool)
		}
	}
}
