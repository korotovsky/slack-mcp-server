package handler

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	slackGoUtil "github.com/takara2314/slack-go-util"
)

var messageTimestampPattern = regexp.MustCompile(`^[0-9]+\.[0-9]{6}$`)

// parseMessageMutationTarget applies the dedicated channel policy even when the
// tool was explicitly selected through SLACK_MCP_ENABLED_TOOLS.
func (ch *ConversationsHandler) parseMessageMutationTarget(ctx context.Context, request mcp.CallToolRequest, toolName, envName string) (string, string, error) {
	selected := slices.Contains(ch.enabledTools, toolName)
	if len(ch.enabledTools) > 0 && !selected {
		return "", "", fmt.Errorf("%s is disabled by the enabled-tools selection", toolName)
	}
	config := os.Getenv(envName)
	if config == "" {
		if !selected {
			return "", "", fmt.Errorf("%s is disabled by default; set %s to true, 1, a channel ID list, or an ! exclusion list, or explicitly select it in SLACK_MCP_ENABLED_TOOLS", toolName, envName)
		}
		config = "true"
	}

	channel := request.GetString("channel_id", "")
	if channel == "" {
		return "", "", fmt.Errorf("channel_id must be a non-empty string")
	}
	timestamp := request.GetString("timestamp", "")
	if !messageTimestampPattern.MatchString(timestamp) {
		return "", "", fmt.Errorf("timestamp must be a valid message timestamp in format 1234567890.123456")
	}
	channel, err := ch.resolveChannelID(ctx, channel)
	if err != nil {
		return "", "", err
	}
	if !isChannelAllowedForConfig(channel, config) {
		return "", "", fmt.Errorf("%s tool is not allowed for channel %q", toolName, channel)
	}
	return channel, timestamp, nil
}

// ConversationsUpdateMessageHandler edits text or broadcasts an existing reply.
func (ch *ConversationsHandler) ConversationsUpdateMessageHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if ready, err := ch.apiProvider.IsReady(); !ready {
		return nil, err
	}
	channel, timestamp, err := ch.parseMessageMutationTarget(ctx, request, "conversations_update_message", "SLACK_MCP_UPDATE_MESSAGE_TOOL")
	if err != nil {
		return nil, err
	}
	if value, present := request.GetArguments()["reply_broadcast"]; present {
		if _, ok := value.(bool); !ok {
			return nil, fmt.Errorf("reply_broadcast must be a boolean")
		}
	}
	var options []slack.MsgOption
	if request.GetBool("reply_broadcast", false) {
		// Slack rejects broadcasting an old reply and editing its content in
		// the same request. Omitting text/blocks also preserves file captions.
		for _, key := range []string{"text", "blocks", "attachments", "file_ids", "markdown_text"} {
			if _, present := request.GetArguments()[key]; present {
				return nil, fmt.Errorf("reply_broadcast cannot be combined with %s; broadcast without changing the reply's content", key)
			}
		}
		options = append(options, slack.MsgOptionBroadcast())
	} else {
		text := request.GetString("text", "")
		if text == "" {
			return nil, fmt.Errorf("text must be a non-empty string, or set reply_broadcast to true")
		}
		contentType := request.GetString("content_type", "text/markdown")
		if contentType != "text/plain" && contentType != "text/markdown" {
			return nil, fmt.Errorf("content_type must be either 'text/plain' or 'text/markdown'")
		}
		// Replace the notification fallback along with the rendered content.
		options = append(options, slack.MsgOptionText(text, false))
		if contentType == "text/markdown" {
			blocks, err := slackGoUtil.ConvertMarkdownTextToBlocks(text)
			if err == nil && len(blocks) > 0 {
				options = append(options, slack.MsgOptionBlocks(blocks...))
			} else {
				options = append(options, slack.MsgOptionDisableMarkdown())
			}
		} else {
			options = append(options, slack.MsgOptionDisableMarkdown())
		}
	}
	if ch.apiProvider.IsBotToken() {
		options = append(options, slack.MsgOptionAsUser(true))
	}

	respChannel, respTimestamp, _, err := ch.apiProvider.Slack().UpdateMessageContext(ctx, channel, timestamp, options...)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("Successfully updated message %s in channel %s", respTimestamp, respChannel)), nil
}

// ConversationsDeleteMessageHandler performs one delete attempt without retries.
func (ch *ConversationsHandler) ConversationsDeleteMessageHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if ready, err := ch.apiProvider.IsReady(); !ready {
		return nil, err
	}
	channel, timestamp, err := ch.parseMessageMutationTarget(ctx, request, "conversations_delete_message", "SLACK_MCP_DELETE_MESSAGE_TOOL")
	if err != nil {
		return nil, err
	}
	respChannel, respTimestamp, err := ch.apiProvider.Slack().DeleteMessageContext(ctx, channel, timestamp)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("Successfully deleted message %s in channel %s", respTimestamp, respChannel)), nil
}
