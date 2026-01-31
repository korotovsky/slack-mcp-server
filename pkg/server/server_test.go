package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShouldAddTool_EmptyEnabledTools(t *testing.T) {
	t.Run("all tools registered with empty enabledTools", func(t *testing.T) {
		for _, tool := range ValidToolNames {
			result := shouldAddTool(tool, []string{})
			assert.True(t, result, "tool %s should be registered when enabledTools is empty", tool)
		}
	})

	t.Run("all tools registered with nil enabledTools", func(t *testing.T) {
		for _, tool := range ValidToolNames {
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
			toolName:     ToolConversationsHistory,
			enabledTools: []string{ToolConversationsHistory, ToolChannelsList},
			expected:     true,
		},
		{
			name:         "tool not in enabledTools list is not registered",
			toolName:     ToolConversationsAddMessage,
			enabledTools: []string{ToolConversationsHistory, ToolChannelsList},
			expected:     false,
		},
		{
			name:         "write tool can be explicitly enabled",
			toolName:     ToolConversationsAddMessage,
			enabledTools: []string{ToolConversationsAddMessage},
			expected:     true,
		},
		{
			name:         "read-only tool blocked when not in explicit list",
			toolName:     ToolConversationsHistory,
			enabledTools: []string{ToolChannelsList},
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
			enabledTools: []string{ToolConversationsHistory},
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
	enabledTools := []string{ToolChannelsList}

	for _, tool := range ValidToolNames {
		result := shouldAddTool(tool, enabledTools)
		if tool == ToolChannelsList {
			assert.True(t, result, "channels_list should be registered")
		} else {
			assert.False(t, result, "%s should NOT be registered when only channels_list is enabled", tool)
		}
	}
}

func TestValidToolNames(t *testing.T) {
	t.Run("ValidToolNames contains all expected tools", func(t *testing.T) {
		expectedTools := map[string]bool{
			ToolConversationsHistory:        true,
			ToolConversationsReplies:        true,
			ToolConversationsAddMessage:     true,
			ToolReactionsAdd:                true,
			ToolReactionsRemove:             true,
			ToolAttachmentGetData:           true,
			ToolConversationsSearchMessages: true,
			ToolChannelsList:                true,
		}

		assert.Equal(t, len(expectedTools), len(ValidToolNames), "ValidToolNames should have %d tools", len(expectedTools))

		for _, tool := range ValidToolNames {
			assert.True(t, expectedTools[tool], "unexpected tool in ValidToolNames: %s", tool)
		}
	})

	t.Run("constants match their string values", func(t *testing.T) {
		assert.Equal(t, "conversations_history", ToolConversationsHistory)
		assert.Equal(t, "conversations_replies", ToolConversationsReplies)
		assert.Equal(t, "conversations_add_message", ToolConversationsAddMessage)
		assert.Equal(t, "reactions_add", ToolReactionsAdd)
		assert.Equal(t, "reactions_remove", ToolReactionsRemove)
		assert.Equal(t, "attachment_get_data", ToolAttachmentGetData)
		assert.Equal(t, "conversations_search_messages", ToolConversationsSearchMessages)
		assert.Equal(t, "channels_list", ToolChannelsList)
	})
}

func TestValidateEnabledTools(t *testing.T) {
	t.Run("empty list is valid", func(t *testing.T) {
		err := ValidateEnabledTools([]string{})
		assert.NoError(t, err)
	})

	t.Run("nil list is valid", func(t *testing.T) {
		err := ValidateEnabledTools(nil)
		assert.NoError(t, err)
	})

	t.Run("all valid tool names pass", func(t *testing.T) {
		err := ValidateEnabledTools(ValidToolNames)
		assert.NoError(t, err)
	})

	t.Run("single valid tool passes", func(t *testing.T) {
		err := ValidateEnabledTools([]string{ToolChannelsList})
		assert.NoError(t, err)
	})

	t.Run("single invalid tool fails", func(t *testing.T) {
		err := ValidateEnabledTools([]string{"invalid_tool"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid_tool")
		assert.Contains(t, err.Error(), "Valid tools are:")
	})

	t.Run("multiple invalid tools listed in error", func(t *testing.T) {
		err := ValidateEnabledTools([]string{"foo", "bar"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "foo")
		assert.Contains(t, err.Error(), "bar")
	})

	t.Run("mix of valid and invalid tools fails", func(t *testing.T) {
		err := ValidateEnabledTools([]string{ToolChannelsList, "invalid_tool", ToolReactionsAdd})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid tool name(s): invalid_tool.")
	})

	t.Run("typo in tool name fails", func(t *testing.T) {
		err := ValidateEnabledTools([]string{"channel_list"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "channel_list")
	})
}
