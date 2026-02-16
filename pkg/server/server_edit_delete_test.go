package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Tests for conversations_edit_message tool registration via shouldAddTool
func TestShouldAddTool_WriteTool_EditMessage(t *testing.T) {
	t.Run("empty enabledTools and empty env var - not registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_EDIT_MESSAGE_TOOL", "")
		defer cleanup()

		result := shouldAddTool(ToolConversationsEditMessage, []string{}, "SLACK_MCP_EDIT_MESSAGE_TOOL")
		assert.False(t, result, "edit_message tool should NOT be registered when both enabledTools is empty and env var is not set")
	})

	t.Run("empty enabledTools and env var set to true - registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_EDIT_MESSAGE_TOOL", "true")
		defer cleanup()

		result := shouldAddTool(ToolConversationsEditMessage, []string{}, "SLACK_MCP_EDIT_MESSAGE_TOOL")
		assert.True(t, result, "edit_message tool should be registered when enabledTools is empty but env var is set")
	})

	t.Run("empty enabledTools and env var set to channel list - registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_EDIT_MESSAGE_TOOL", "C123,C456")
		defer cleanup()

		result := shouldAddTool(ToolConversationsEditMessage, []string{}, "SLACK_MCP_EDIT_MESSAGE_TOOL")
		assert.True(t, result, "edit_message tool should be registered when enabledTools is empty but env var has channel list")
	})

	t.Run("explicit enabledTools includes tool and empty env var - registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_EDIT_MESSAGE_TOOL", "")
		defer cleanup()

		result := shouldAddTool(ToolConversationsEditMessage, []string{ToolConversationsEditMessage}, "SLACK_MCP_EDIT_MESSAGE_TOOL")
		assert.True(t, result, "edit_message tool should be registered when explicitly in enabledTools even without env var")
	})

	t.Run("explicit enabledTools excludes tool - not registered even with env var", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_EDIT_MESSAGE_TOOL", "true")
		defer cleanup()

		result := shouldAddTool(ToolConversationsEditMessage, []string{ToolConversationsHistory}, "SLACK_MCP_EDIT_MESSAGE_TOOL")
		assert.False(t, result, "edit_message tool should NOT be registered when not in explicit enabledTools list")
	})
}

// Tests for conversations_delete_message tool registration via shouldAddTool
func TestShouldAddTool_WriteTool_DeleteMessage(t *testing.T) {
	t.Run("empty enabledTools and empty env var - not registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_DELETE_MESSAGE_TOOL", "")
		defer cleanup()

		result := shouldAddTool(ToolConversationsDeleteMessage, []string{}, "SLACK_MCP_DELETE_MESSAGE_TOOL")
		assert.False(t, result, "delete_message tool should NOT be registered when both enabledTools is empty and env var is not set")
	})

	t.Run("empty enabledTools and env var set to true - registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_DELETE_MESSAGE_TOOL", "true")
		defer cleanup()

		result := shouldAddTool(ToolConversationsDeleteMessage, []string{}, "SLACK_MCP_DELETE_MESSAGE_TOOL")
		assert.True(t, result, "delete_message tool should be registered when enabledTools is empty but env var is set")
	})

	t.Run("empty enabledTools and env var set to channel list - registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_DELETE_MESSAGE_TOOL", "C123,C456")
		defer cleanup()

		result := shouldAddTool(ToolConversationsDeleteMessage, []string{}, "SLACK_MCP_DELETE_MESSAGE_TOOL")
		assert.True(t, result, "delete_message tool should be registered when enabledTools is empty but env var has channel list")
	})

	t.Run("explicit enabledTools includes tool and empty env var - registered", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_DELETE_MESSAGE_TOOL", "")
		defer cleanup()

		result := shouldAddTool(ToolConversationsDeleteMessage, []string{ToolConversationsDeleteMessage}, "SLACK_MCP_DELETE_MESSAGE_TOOL")
		assert.True(t, result, "delete_message tool should be registered when explicitly in enabledTools even without env var")
	})

	t.Run("explicit enabledTools excludes tool - not registered even with env var", func(t *testing.T) {
		cleanup := setEnv("SLACK_MCP_DELETE_MESSAGE_TOOL", "true")
		defer cleanup()

		result := shouldAddTool(ToolConversationsDeleteMessage, []string{ToolConversationsHistory}, "SLACK_MCP_DELETE_MESSAGE_TOOL")
		assert.False(t, result, "delete_message tool should NOT be registered when not in explicit enabledTools list")
	})
}

// Tests for the ENABLED_TOOLS + env var matrix for edit message
func TestShouldAddTool_Matrix_EditMessage(t *testing.T) {
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
			enabledTools: []string{ToolConversationsEditMessage},
			envVarValue:  "",
			expected:     true,
		},
		{
			name:         "includes tool + env var=list = registered",
			enabledTools: []string{ToolConversationsEditMessage},
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
			cleanup := setEnv("SLACK_MCP_EDIT_MESSAGE_TOOL", tt.envVarValue)
			defer cleanup()

			result := shouldAddTool(ToolConversationsEditMessage, tt.enabledTools, "SLACK_MCP_EDIT_MESSAGE_TOOL")
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Tests for the ENABLED_TOOLS + env var matrix for delete message
func TestShouldAddTool_Matrix_DeleteMessage(t *testing.T) {
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
			enabledTools: []string{ToolConversationsDeleteMessage},
			envVarValue:  "",
			expected:     true,
		},
		{
			name:         "includes tool + env var=list = registered",
			enabledTools: []string{ToolConversationsDeleteMessage},
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
			cleanup := setEnv("SLACK_MCP_DELETE_MESSAGE_TOOL", tt.envVarValue)
			defer cleanup()

			result := shouldAddTool(ToolConversationsDeleteMessage, tt.enabledTools, "SLACK_MCP_DELETE_MESSAGE_TOOL")
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test that ValidToolNames includes the new edit/delete tools
func TestValidToolNames_IncludesEditDelete(t *testing.T) {
	t.Run("ValidToolNames contains edit_message tool", func(t *testing.T) {
		found := false
		for _, tool := range ValidToolNames {
			if tool == ToolConversationsEditMessage {
				found = true
				break
			}
		}
		assert.True(t, found, "ValidToolNames should contain %s", ToolConversationsEditMessage)
	})

	t.Run("ValidToolNames contains delete_message tool", func(t *testing.T) {
		found := false
		for _, tool := range ValidToolNames {
			if tool == ToolConversationsDeleteMessage {
				found = true
				break
			}
		}
		assert.True(t, found, "ValidToolNames should contain %s", ToolConversationsDeleteMessage)
	})

	t.Run("new tool constants match their string values", func(t *testing.T) {
		assert.Equal(t, "conversations_edit_message", ToolConversationsEditMessage)
		assert.Equal(t, "conversations_delete_message", ToolConversationsDeleteMessage)
	})
}

// Test that ValidateEnabledTools accepts the new tool names
func TestValidateEnabledTools_EditDelete(t *testing.T) {
	t.Run("edit_message tool name is valid", func(t *testing.T) {
		err := ValidateEnabledTools([]string{ToolConversationsEditMessage})
		assert.NoError(t, err)
	})

	t.Run("delete_message tool name is valid", func(t *testing.T) {
		err := ValidateEnabledTools([]string{ToolConversationsDeleteMessage})
		assert.NoError(t, err)
	})

	t.Run("both new tools together are valid", func(t *testing.T) {
		err := ValidateEnabledTools([]string{ToolConversationsEditMessage, ToolConversationsDeleteMessage})
		assert.NoError(t, err)
	})

	t.Run("new tools combined with existing tools are valid", func(t *testing.T) {
		err := ValidateEnabledTools([]string{
			ToolConversationsHistory,
			ToolConversationsEditMessage,
			ToolConversationsDeleteMessage,
			ToolChannelsList,
		})
		assert.NoError(t, err)
	})
}

// Test that SingleToolEnabled still works when one of the new tools is the only one
func TestShouldAddTool_SingleToolEnabled_EditMessage(t *testing.T) {
	enabledTools := []string{ToolConversationsEditMessage}

	for _, tool := range ValidToolNames {
		result := shouldAddTool(tool, enabledTools, "")
		if tool == ToolConversationsEditMessage {
			assert.True(t, result, "%s should be registered", tool)
		} else {
			assert.False(t, result, "%s should NOT be registered when only %s is enabled", tool, ToolConversationsEditMessage)
		}
	}
}

func TestShouldAddTool_SingleToolEnabled_DeleteMessage(t *testing.T) {
	enabledTools := []string{ToolConversationsDeleteMessage}

	for _, tool := range ValidToolNames {
		result := shouldAddTool(tool, enabledTools, "")
		if tool == ToolConversationsDeleteMessage {
			assert.True(t, result, "%s should be registered", tool)
		} else {
			assert.False(t, result, "%s should NOT be registered when only %s is enabled", tool, ToolConversationsDeleteMessage)
		}
	}
}
