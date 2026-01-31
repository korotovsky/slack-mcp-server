package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShouldAddTool(t *testing.T) {
	tests := []struct {
		name         string
		toolName     string
		enabledTools []string
		expected     bool
	}{
		{
			name:         "empty enabledTools allows all tools",
			toolName:     "conversations_history",
			enabledTools: []string{},
			expected:     true,
		},
		{
			name:         "nil enabledTools allows all tools",
			toolName:     "conversations_history",
			enabledTools: nil,
			expected:     true,
		},
		{
			name:         "tool in enabledTools list is allowed",
			toolName:     "conversations_history",
			enabledTools: []string{"conversations_history", "channels_list"},
			expected:     true,
		},
		{
			name:         "tool not in enabledTools list is blocked",
			toolName:     "conversations_add_message",
			enabledTools: []string{"conversations_history", "channels_list"},
			expected:     false,
		},
		{
			name:         "single tool in enabledTools",
			toolName:     "conversations_history",
			enabledTools: []string{"conversations_history"},
			expected:     true,
		},
		{
			name:         "different single tool in enabledTools blocks other tools",
			toolName:     "conversations_history",
			enabledTools: []string{"channels_list"},
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

func TestEnabledToolsAndMessageToolFlagInteraction(t *testing.T) {
	readTools := []string{
		"conversations_history",
		"conversations_replies",
		"conversations_search_messages",
		"channels_list",
		"attachment_get_data",
	}

	tests := []struct {
		name               string
		enabledTools       []string
		messageToolEnabled bool
		toolName           string
		expectedAvailable  bool
	}{
		{
			name:               "empty ENABLED_TOOLS + disabled MESSAGE_TOOL: conversations_history available",
			enabledTools:       []string{},
			messageToolEnabled: false,
			toolName:           "conversations_history",
			expectedAvailable:  true,
		},
		{
			name:               "empty ENABLED_TOOLS + enabled MESSAGE_TOOL: conversations_history available",
			enabledTools:       []string{},
			messageToolEnabled: true,
			toolName:           "conversations_history",
			expectedAvailable:  true,
		},
		{
			name:               "ENABLED_TOOLS=conversations_history + disabled MESSAGE_TOOL: conversations_history available",
			enabledTools:       []string{"conversations_history"},
			messageToolEnabled: false,
			toolName:           "conversations_history",
			expectedAvailable:  true,
		},
		{
			name:               "ENABLED_TOOLS=conversations_history + enabled MESSAGE_TOOL: conversations_history available",
			enabledTools:       []string{"conversations_history"},
			messageToolEnabled: true,
			toolName:           "conversations_history",
			expectedAvailable:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldAddTool(tt.toolName, tt.enabledTools)
			assert.Equal(t, tt.expectedAvailable, result)
		})
	}

	t.Run("all read tools available with empty ENABLED_TOOLS", func(t *testing.T) {
		for _, tool := range readTools {
			result := shouldAddTool(tool, []string{})
			assert.True(t, result, "read tool %s should be available with empty ENABLED_TOOLS", tool)
		}
	})

	t.Run("read tools blocked when not in ENABLED_TOOLS", func(t *testing.T) {
		enabledTools := []string{"channels_list"}
		for _, tool := range readTools {
			result := shouldAddTool(tool, enabledTools)
			if tool == "channels_list" {
				assert.True(t, result, "channels_list should be available when in ENABLED_TOOLS")
			} else {
				assert.False(t, result, "read tool %s should NOT be available when not in ENABLED_TOOLS", tool)
			}
		}
	})
}

func TestWriteToolsWithEnabledToolsFlag(t *testing.T) {
	tests := []struct {
		name              string
		enabledTools      []string
		toolName          string
		expectedAvailable bool
	}{
		{
			name:              "conversations_add_message available with empty ENABLED_TOOLS",
			enabledTools:      []string{},
			toolName:          "conversations_add_message",
			expectedAvailable: true,
		},
		{
			name:              "conversations_add_message available when in ENABLED_TOOLS",
			enabledTools:      []string{"conversations_add_message"},
			toolName:          "conversations_add_message",
			expectedAvailable: true,
		},
		{
			name:              "conversations_add_message blocked when not in ENABLED_TOOLS",
			enabledTools:      []string{"conversations_history"},
			toolName:          "conversations_add_message",
			expectedAvailable: false,
		},
		{
			name:              "reactions_add available with empty ENABLED_TOOLS",
			enabledTools:      []string{},
			toolName:          "reactions_add",
			expectedAvailable: true,
		},
		{
			name:              "reactions_add blocked when not in ENABLED_TOOLS",
			enabledTools:      []string{"conversations_history"},
			toolName:          "reactions_add",
			expectedAvailable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldAddTool(tt.toolName, tt.enabledTools)
			assert.Equal(t, tt.expectedAvailable, result)
		})
	}
}

func TestMessageToolBlocksRegardlessOfEnabledTools(t *testing.T) {
	tests := []struct {
		name               string
		enabledTools       []string
		messageToolEnabled bool
		toolName           string
		expectedAvailable  bool
	}{
		{
			name:               "MESSAGE_TOOL disabled + empty ENABLED_TOOLS: conversations_add_message blocked",
			enabledTools:       []string{},
			messageToolEnabled: false,
			toolName:           "conversations_add_message",
			expectedAvailable:  false,
		},
		{
			name:               "MESSAGE_TOOL disabled + ENABLED_TOOLS includes tool: conversations_add_message blocked",
			enabledTools:       []string{"conversations_add_message"},
			messageToolEnabled: false,
			toolName:           "conversations_add_message",
			expectedAvailable:  false,
		},
		{
			name:               "MESSAGE_TOOL enabled + empty ENABLED_TOOLS: conversations_add_message available",
			enabledTools:       []string{},
			messageToolEnabled: true,
			toolName:           "conversations_add_message",
			expectedAvailable:  true,
		},
		{
			name:               "MESSAGE_TOOL enabled + ENABLED_TOOLS includes tool: conversations_add_message available",
			enabledTools:       []string{"conversations_add_message"},
			messageToolEnabled: true,
			toolName:           "conversations_add_message",
			expectedAvailable:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldAddTool(tt.toolName, tt.enabledTools)
			if !tt.messageToolEnabled {
				assert.False(t, result, "conversations_add_message should be blocked when MESSAGE_TOOL is disabled")
			} else {
				assert.Equal(t, tt.expectedAvailable, result)
			}
		})
	}
}
