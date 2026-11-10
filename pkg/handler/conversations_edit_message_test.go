package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func editMessageRequest(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = "conversations_edit_message"
	req.Params.Arguments = args
	return req
}

func TestUnitParseParamsToolEditMessage(t *testing.T) {
	ch := &ConversationsHandler{logger: zap.NewNop()}
	ctx := context.Background()

	t.Run("disabled by default", func(t *testing.T) {
		t.Setenv("SLACK_MCP_EDIT_MESSAGE_TOOL", "")
		t.Setenv("SLACK_MCP_ENABLED_TOOLS", "")
		_, err := ch.parseParamsToolEditMessage(ctx, editMessageRequest(map[string]any{
			"channel_id": "C123", "timestamp": "1234567890.123456", "text": "hi",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "disabled")
	})

	t.Run("enabled through SLACK_MCP_ENABLED_TOOLS", func(t *testing.T) {
		t.Setenv("SLACK_MCP_EDIT_MESSAGE_TOOL", "")
		t.Setenv("SLACK_MCP_ENABLED_TOOLS", "conversations_edit_message")
		params, err := ch.parseParamsToolEditMessage(ctx, editMessageRequest(map[string]any{
			"channel_id": "C123", "timestamp": "1234567890.123456", "text": "hi",
		}))
		require.NoError(t, err)
		assert.Equal(t, "C123", params.channel)
	})

	t.Run("channel outside the allowlist", func(t *testing.T) {
		t.Setenv("SLACK_MCP_EDIT_MESSAGE_TOOL", "C999")
		_, err := ch.parseParamsToolEditMessage(ctx, editMessageRequest(map[string]any{
			"channel_id": "C123", "timestamp": "1234567890.123456", "text": "hi",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed")
	})

	t.Run("validates timestamp", func(t *testing.T) {
		t.Setenv("SLACK_MCP_EDIT_MESSAGE_TOOL", "true")
		_, err := ch.parseParamsToolEditMessage(ctx, editMessageRequest(map[string]any{
			"channel_id": "C123", "text": "hi",
		}))
		require.Error(t, err)
		_, err = ch.parseParamsToolEditMessage(ctx, editMessageRequest(map[string]any{
			"channel_id": "C123", "timestamp": "1234567890", "text": "hi",
		}))
		require.Error(t, err)
	})

	t.Run("requires text or blocks", func(t *testing.T) {
		t.Setenv("SLACK_MCP_EDIT_MESSAGE_TOOL", "true")
		_, err := ch.parseParamsToolEditMessage(ctx, editMessageRequest(map[string]any{
			"channel_id": "C123", "timestamp": "1234567890.123456",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "either text or blocks")
	})

	t.Run("rejects an unknown content_type", func(t *testing.T) {
		t.Setenv("SLACK_MCP_EDIT_MESSAGE_TOOL", "true")
		_, err := ch.parseParamsToolEditMessage(ctx, editMessageRequest(map[string]any{
			"channel_id": "C123", "timestamp": "1234567890.123456", "text": "hi", "content_type": "text/html",
		}))
		require.Error(t, err)
	})

	t.Run("parses blocks given as a JSON string or a raw array", func(t *testing.T) {
		t.Setenv("SLACK_MCP_EDIT_MESSAGE_TOOL", "true")
		raw := `[{"type":"section","text":{"type":"mrkdwn","text":"new"}}]`
		params, err := ch.parseParamsToolEditMessage(ctx, editMessageRequest(map[string]any{
			"channel_id": "C123", "timestamp": "1234567890.123456", "blocks": raw,
		}))
		require.NoError(t, err)
		assert.Len(t, params.blocks, 1)

		var decoded []any
		require.NoError(t, json.Unmarshal([]byte(raw), &decoded))
		params, err = ch.parseParamsToolEditMessage(ctx, editMessageRequest(map[string]any{
			"channel_id": "C123", "timestamp": "1234567890.123456", "blocks": decoded,
		}))
		require.NoError(t, err)
		assert.Len(t, params.blocks, 1)
	})

	t.Run("rejects invalid blocks JSON", func(t *testing.T) {
		t.Setenv("SLACK_MCP_EDIT_MESSAGE_TOOL", "true")
		_, err := ch.parseParamsToolEditMessage(ctx, editMessageRequest(map[string]any{
			"channel_id": "C123", "timestamp": "1234567890.123456", "blocks": "[{",
		}))
		require.Error(t, err)
	})
}

// captureUpdate sends the edit options through slack-go to a fake chat.update
// endpoint and returns the form values Slack would receive.
func captureUpdate(t *testing.T, params *editMessageParams) map[string]string {
	t.Helper()
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		got["path"] = r.URL.Path
		for _, k := range []string{"channel", "ts", "text", "blocks"} {
			if _, ok := r.PostForm[k]; ok {
				got[k] = r.PostForm.Get(k)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1234567890.123456","text":"x"}`))
	}))
	defer srv.Close()

	options, err := editMessageOptions(params)
	require.NoError(t, err)
	client := slack.New("xoxp-test", slack.OptionAPIURL(srv.URL+"/"))
	_, _, _, err = client.UpdateMessageContext(context.Background(), params.channel, params.timestamp, options...)
	require.NoError(t, err)
	return got
}

func TestUnitEditMessageOptions(t *testing.T) {
	t.Run("markdown sends new blocks and the new fallback text", func(t *testing.T) {
		got := captureUpdate(t, &editMessageParams{
			channel: "C123", timestamp: "1234567890.123456", contentType: "text/markdown", text: "**AEO:** with Louis",
		})
		assert.Equal(t, "/chat.update", got["path"])
		assert.Equal(t, "1234567890.123456", got["ts"])
		assert.Equal(t, "**AEO:** with Louis", got["text"])
		assert.NotEmpty(t, got["blocks"])
		assert.NotEqual(t, "[]", got["blocks"])
	})

	t.Run("plain text clears the old blocks", func(t *testing.T) {
		got := captureUpdate(t, &editMessageParams{
			channel: "C123", timestamp: "1234567890.123456", contentType: "text/plain", text: "plain",
		})
		assert.Equal(t, "plain", got["text"])
		assert.Equal(t, "[]", got["blocks"])
	})

	t.Run("raw blocks are sent as given, with the fallback text", func(t *testing.T) {
		block := slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, "new", false, false), nil, nil)
		got := captureUpdate(t, &editMessageParams{
			channel: "C123", timestamp: "1234567890.123456", contentType: "text/markdown", text: "new", blocks: []slack.Block{block},
		})
		assert.Equal(t, "new", got["text"])
		assert.Contains(t, got["blocks"], `"type":"section"`)
	})

	t.Run("unknown content type is an error", func(t *testing.T) {
		_, err := editMessageOptions(&editMessageParams{contentType: "text/html", text: "x"})
		require.Error(t, err)
	})
}
