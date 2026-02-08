package server

import (
	"os"
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

func TestShouldAddWriteTool(t *testing.T) {
	// Helper to set/unset env vars for tests
	setEnv := func(key, value string) func() {
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

	t.Run("empty enabledTools and empty env var - not registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", "")
		defer cleanup()

		result := shouldAddWriteTool(ToolConversationsAddMessage, []string{}, "SLACK_MCP_ADD_MESSAGE_TOOL")
		assert.False(t, result, "write tool should NOT be registered when both enabledTools is empty and env var is not set")
	})

	t.Run("empty enabledTools and env var set to true - registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
		defer cleanup()

		result := shouldAddWriteTool(ToolConversationsAddMessage, []string{}, "SLACK_MCP_ADD_MESSAGE_TOOL")
		assert.True(t, result, "write tool should be registered when enabledTools is empty but env var is set")
	})

	t.Run("empty enabledTools and env var set to channel list - registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", "C123,C456")
		defer cleanup()

		result := shouldAddWriteTool(ToolConversationsAddMessage, []string{}, "SLACK_MCP_ADD_MESSAGE_TOOL")
		assert.True(t, result, "write tool should be registered when enabledTools is empty but env var has channel list")
	})

	t.Run("explicit enabledTools includes tool and empty env var - registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", "")
		defer cleanup()

		result := shouldAddWriteTool(ToolConversationsAddMessage, []string{ToolConversationsAddMessage}, "SLACK_MCP_ADD_MESSAGE_TOOL")
		assert.True(t, result, "write tool should be registered when explicitly in enabledTools even without env var")
	})

	t.Run("explicit enabledTools includes tool and env var set - registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", "C123")
		defer cleanup()

		result := shouldAddWriteTool(ToolConversationsAddMessage, []string{ToolConversationsAddMessage}, "SLACK_MCP_ADD_MESSAGE_TOOL")
		assert.True(t, result, "write tool should be registered when explicitly in enabledTools with env var")
	})

	t.Run("explicit enabledTools excludes tool - not registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
		defer cleanup()

		result := shouldAddWriteTool(ToolConversationsAddMessage, []string{ToolConversationsHistory}, "SLACK_MCP_ADD_MESSAGE_TOOL")
		assert.False(t, result, "write tool should NOT be registered when not in explicit enabledTools list")
	})

	t.Run("reactions tools with empty enabledTools and no env var - not registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_REACTION_TOOL", "")
		defer cleanup()

		result := shouldAddWriteTool(ToolReactionsAdd, []string{}, "SLACK_MCP_REACTION_TOOL")
		assert.False(t, result, "reactions_add should NOT be registered when env var is not set")

		result = shouldAddWriteTool(ToolReactionsRemove, []string{}, "SLACK_MCP_REACTION_TOOL")
		assert.False(t, result, "reactions_remove should NOT be registered when env var is not set")
	})

	t.Run("attachment tool with empty enabledTools and no env var - not registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ATTACHMENT_TOOL", "")
		defer cleanup()

		result := shouldAddWriteTool(ToolAttachmentGetData, []string{}, "SLACK_MCP_ATTACHMENT_TOOL")
		assert.False(t, result, "attachment_get_data should NOT be registered when env var is not set")
	})

	t.Run("attachment tool explicitly enabled via enabledTools - registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_ATTACHMENT_TOOL", "")
		defer cleanup()

		result := shouldAddWriteTool(ToolAttachmentGetData, []string{ToolAttachmentGetData}, "SLACK_MCP_ATTACHMENT_TOOL")
		assert.True(t, result, "attachment_get_data should be registered when explicitly in enabledTools")
	})
}

func TestShouldAddWriteTool_Matrix(t *testing.T) {
	// Test the complete matrix from the plan:
	// | ENABLED_TOOLS | ADD_MESSAGE_TOOL | Result |
	// |---------------|------------------|--------|
	// | empty         | empty            | NOT registered |
	// | empty         | true/list        | Registered |
	// | includes tool | empty            | Registered |
	// | includes tool | list             | Registered |
	// | excludes tool | any              | NOT registered |

	setEnv := func(key, value string) func() {
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

	tests := []struct {
		name         string
		enabledTools []string
		envVarValue  string
		expected     bool
	}{
		{
			name:         "empty ENABLED_TOOLS + empty env var = NOT registered",
			enabledTools: []string{},
			envVarValue:  "",
			expected:     false,
		},
		{
			name:         "empty ENABLED_TOOLS + env var=true = registered",
			enabledTools: []string{},
			envVarValue:  "true",
			expected:     true,
		},
		{
			name:         "empty ENABLED_TOOLS + env var=channel list = registered",
			enabledTools: []string{},
			envVarValue:  "C123,C456",
			expected:     true,
		},
		{
			name:         "includes tool + empty env var = registered",
			enabledTools: []string{ToolConversationsAddMessage},
			envVarValue:  "",
			expected:     true,
		},
		{
			name:         "includes tool + env var=list = registered",
			enabledTools: []string{ToolConversationsAddMessage},
			envVarValue:  "C123",
			expected:     true,
		},
		{
			name:         "excludes tool + empty env var = NOT registered",
			enabledTools: []string{ToolConversationsHistory},
			envVarValue:  "",
			expected:     false,
		},
		{
			name:         "excludes tool + env var=true = NOT registered",
			enabledTools: []string{ToolConversationsHistory},
			envVarValue:  "true",
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setEnv("SLACK_MCP_ADD_MESSAGE_TOOL", tt.envVarValue)
			defer cleanup()

			result := shouldAddWriteTool(ToolConversationsAddMessage, tt.enabledTools, "SLACK_MCP_ADD_MESSAGE_TOOL")
			assert.Equal(t, tt.expected, result)
		})
	}
}
