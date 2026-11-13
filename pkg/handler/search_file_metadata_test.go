package handler

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type searchFilesProvider struct{ mutationProvider }

func (p searchFilesProvider) ProvideUsersMap() *provider.UsersCache {
	return &provider.UsersCache{Users: map[string]slack.User{"U1": {ID: "U1", Name: "test-user", RealName: "Test User"}}}
}

type searchFilesAPI struct {
	workflowSlack
	results *provider.SearchMessagesWithFiles
}

func (api searchFilesAPI) SearchMessagesWithFilesContext(context.Context, string, slack.SearchParameters) (*provider.SearchMessagesWithFiles, error) {
	return api.results, nil
}

func TestUnitSearchHydratesExactThreadReplyAndLabelsFailures(t *testing.T) {
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		require.NoError(t, r.ParseForm())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/conversations.replies" {
			require.Equal(t, "1234567890.123456", r.Form.Get("ts"))
			require.Equal(t, "1234567891.123456", r.Form.Get("oldest"))
			require.Equal(t, "1234567891.123456", r.Form.Get("latest"))
			fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"1234567890.123456","files":[{"id":"WRONG"}]},{"ts":"1234567891.123456","files":[{"id":"FREPLY","name":"reply.pdf"}]}]}`)
		} else {
			fmt.Fprint(w, `{"ok":false,"error":"missing_scope"}`)
		}
	}))
	defer server.Close()
	base := workflowHandler(server.URL)
	client := base.apiProvider.(mutationProvider).api.(workflowSlack)
	result := &provider.SearchMessagesWithFiles{Matches: []provider.SearchMessageWithFiles{
		{SearchMessage: slack.SearchMessage{User: "U1", Timestamp: "1234567891.123456", Text: "thread file", Channel: slack.CtxChannel{ID: "C123", Name: "general"}, Permalink: "https://example.slack.com/archives/C123/p1234567891123456?thread_ts=1234567890.123456"}},
		{SearchMessage: slack.SearchMessage{User: "U1", Timestamp: "1234567892.123456", Text: "wire file", Channel: slack.CtxChannel{ID: "C123", Name: "general"}}, Files: []slack.File{{ID: "FWIRE", Name: "wire.pdf"}}},
		{SearchMessage: slack.SearchMessage{User: "U1", Timestamp: "1234567893.123456", Text: "cannot inspect", Channel: slack.CtxChannel{ID: "C123", Name: "general"}}},
	}}
	handler := &ConversationsHandler{logger: zap.NewNop(), apiProvider: searchFilesProvider{mutationProvider{api: searchFilesAPI{workflowSlack: client, results: result}}}}
	req := mcp.CallToolRequest{}
	req.Params.Name = "conversations_search_messages"
	req.Params.Arguments = map[string]any{"search_query": "file"}
	response, err := handler.ConversationsSearchHandler(context.Background(), req)
	require.NoError(t, err)
	rows, err := csv.NewReader(strings.NewReader(response.Content[0].(mcp.TextContent).Text)).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 4)
	indices := map[string]int{}
	for i, name := range rows[0] {
		indices[name] = i
	}
	require.Contains(t, indices, "AttachmentIDs")
	require.Contains(t, indices, "FileMetadataStatus")
	require.Contains(t, rows[1][indices["AttachmentIDs"]], "FREPLY")
	require.NotContains(t, rows[1][indices["AttachmentIDs"]], "WRONG")
	require.Contains(t, rows[2][indices["AttachmentIDs"]], "FWIRE")
	require.Equal(t, "unavailable", rows[3][indices["FileMetadataStatus"]])
	require.Equal(t, 1, calls["/conversations.replies"])
	require.Equal(t, 1, calls["/conversations.history"])
}

func TestUnitSearchMetadataOptOutMakesNoFallbackCalls(t *testing.T) {
	handler := &ConversationsHandler{logger: zap.NewNop()}
	matches := []provider.SearchMessageWithFiles{{SearchMessage: slack.SearchMessage{Timestamp: "1234567890.123456", Channel: slack.CtxChannel{ID: "C123"}}}}
	handler.resolveSearchFiles(context.Background(), matches, false)
	require.Equal(t, "not_requested", matches[0].FileMetadataStatus)
	handler.resolveSearchFiles(context.Background(), []provider.SearchMessageWithFiles{{Files: []slack.File{}}}, true)
}
