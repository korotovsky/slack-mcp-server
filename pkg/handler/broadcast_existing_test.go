package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnitBroadcastExistingReplyPreservesContent(t *testing.T) {
	t.Setenv("SLACK_MCP_UPDATE_MESSAGE_TOOL", "C123")
	var form url.Values
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/chat.update", r.URL.Path)
		require.NoError(t, r.ParseForm())
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"channel":"C123","ts":"1234567891.123456","text":"Existing file caption"}`)
	}))
	defer srv.Close()
	req := newUploadRequest(map[string]any{"channel_id": "C123", "timestamp": "1234567891.123456", "reply_broadcast": true})
	req.Params.Name = "conversations_update_message"
	_, err := workflowHandler(srv.URL).ConversationsUpdateMessageHandler(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, "true", form.Get("reply_broadcast"))
	require.Equal(t, "1234567891.123456", form.Get("ts"), "target the reply, not its parent")
	for _, key := range []string{"text", "blocks", "attachments", "file_ids", "markdown_text"} {
		require.NotContains(t, form, key, "preserve existing message content")
	}
}

func TestUnitBroadcastRejectsContentChangesBeforeSending(t *testing.T) {
	t.Setenv("SLACK_MCP_UPDATE_MESSAGE_TOOL", "true")
	for _, key := range []string{"text", "blocks", "attachments", "file_ids", "markdown_text"} {
		t.Run(key, func(t *testing.T) {
			args := map[string]any{"channel_id": "C123", "timestamp": "1234567891.123456", "reply_broadcast": true, key: "replacement"}
			req := newUploadRequest(args)
			// Any unexpected API call would fail against an unused local port.
			h := workflowHandler("http://127.0.0.1:1")
			_, err := h.ConversationsUpdateMessageHandler(context.Background(), req)
			require.ErrorContains(t, err, "cannot be combined")
		})
	}
}
