package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mutationSlack struct {
	provider.SlackAPI
	client *slack.Client
}

func (s mutationSlack) UpdateMessageContext(ctx context.Context, channel, ts string, options ...slack.MsgOption) (string, string, string, error) {
	return s.client.UpdateMessageContext(ctx, channel, ts, options...)
}

func (s mutationSlack) DeleteMessageContext(ctx context.Context, channel, ts string) (string, string, error) {
	return s.client.DeleteMessageContext(ctx, channel, ts)
}

type mutationProvider struct {
	*provider.ApiProvider
	api provider.SlackAPI
	bot bool
}

func (p mutationProvider) IsBotToken() bool         { return p.bot }
func (p mutationProvider) IsReady() (bool, error)   { return true, nil }
func (p mutationProvider) Slack() provider.SlackAPI { return p.api }
func (p mutationProvider) ProvideChannelsMaps() *provider.ChannelsCache {
	return &provider.ChannelsCache{
		Channels:    map[string]provider.Channel{"C123": {ID: "C123", Name: "general"}},
		ChannelsInv: map[string]string{"#general": "C123", "@alice_dm": "C123"},
	}
}

func TestUnitMessageMutations(t *testing.T) {
	for _, op := range []string{"update", "delete"} {
		t.Run(op, func(t *testing.T) {
			tool := "conversations_" + op + "_message"
			env := "SLACK_MCP_" + strings.ToUpper(op) + "_MESSAGE_TOOL"
			for _, tc := range []struct {
				name, config, selected, channel, timestamp, text, contentType, slackError, wantError string
				wantCall                                                                             bool
				bot                                                                                  bool
			}{
				{name: "disabled", channel: "C123", timestamp: "1234567890.123456", text: "replacement", wantError: "disabled by default"},
				{name: "explicit selection", selected: tool, channel: "C123", timestamp: "1234567890.123456", text: "replacement", wantCall: true},
				{name: "substring selection denied", selected: tool + "_other", channel: "C123", timestamp: "1234567890.123456", text: "replacement", wantError: "disabled by the enabled-tools selection"},
				{name: "selection excludes enabled tool", config: "true", selected: "channels_list", channel: "C123", timestamp: "1234567890.123456", text: "replacement", wantError: "disabled by the enabled-tools selection"},
				{name: "allowed alias", config: "C123", channel: "#general", timestamp: "1234567890.123456", text: "replacement", wantCall: true},
				{name: "allowed DM alias", config: "C123", channel: "@alice_dm", timestamp: "1234567890.123456", text: "replacement", wantCall: true},
				{name: "blocked channel despite selection", config: "C999", selected: tool, channel: "C123", timestamp: "1234567890.123456", text: "replacement", wantError: "not allowed"},
				{name: "excluded alias", config: "!C123", selected: tool, channel: "#general", timestamp: "1234567890.123456", text: "replacement", wantError: "not allowed"},
				{name: "other exclusion", config: "!C999", channel: "C123", timestamp: "1234567890.123456", text: "replacement", wantCall: true},
				{name: "missing channel", config: "true", timestamp: "1234567890.123456", text: "replacement", wantError: "channel_id"},
				{name: "bad timestamp", config: "true", channel: "C123", timestamp: "bad.timestamp", text: "replacement", wantError: "timestamp"},
				{name: "integer timestamp", config: "true", channel: "C123", timestamp: "1234567890", text: "replacement", wantError: "timestamp"},
				{name: "missing timestamp", config: "true", channel: "C123", text: "replacement", wantError: "timestamp"},
				{name: "short fraction", config: "true", channel: "C123", timestamp: "1234567890.123", text: "replacement", wantError: "timestamp"},
				{name: "permission denied", config: "true", channel: "C123", timestamp: "1234567890.123456", text: "replacement", slackError: "missing_scope", wantError: "missing_scope", wantCall: true},
				{name: "not owner", config: "true", channel: "C123", timestamp: "1234567890.123456", text: "replacement", slackError: "cant_" + op + "_message", wantError: "cant_" + op + "_message", wantCall: true},
				{name: "own bot message", config: "true", channel: "C123", timestamp: "1234567890.123456", text: "replacement", wantCall: true, bot: true},
				{name: "plain replacement", config: "1", channel: "C123", timestamp: "1234567890.123456", text: "*literal*", contentType: "text/plain", wantCall: true},
				{name: "markdown replacement", config: "true", channel: "C123", timestamp: "1234567890.123456", text: "**bold**", wantCall: true},
				{name: "unsupported markdown fallback", config: "true", channel: "C123", timestamp: "1234567890.123456", text: "---", wantCall: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Setenv(env, tc.config)
					t.Setenv("SLACK_MCP_ENABLED_TOOLS", "") // selection may come from --enabled-tools
					var selected []string
					if tc.selected != "" {
						selected = strings.Split(tc.selected, ",")
					}
					calls := 0
					var form url.Values
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						require.Equal(t, "/chat."+op, r.URL.Path)
						require.Equal(t, http.MethodPost, r.Method)
						require.NoError(t, r.ParseForm())
						form = r.PostForm
						w.Header().Set("Content-Type", "application/json")
						if tc.slackError != "" {
							fmt.Fprintf(w, `{"ok":false,"error":%q}`, tc.slackError)
							return
						}
						fmt.Fprint(w, `{"ok":true,"channel":"C123","ts":"1234567890.123456","text":"replacement"}`)
					}))
					defer srv.Close()
					h := &ConversationsHandler{enabledTools: selected, apiProvider: mutationProvider{bot: tc.bot, api: mutationSlack{client: slack.New("test-only", slack.OptionAPIURL(srv.URL+"/"))}}, logger: zap.NewNop()}
					req := mcp.CallToolRequest{}
					req.Params.Name = tool
					args := map[string]interface{}{"channel_id": tc.channel, "timestamp": tc.timestamp, "text": tc.text}
					if tc.contentType != "" {
						args["content_type"] = tc.contentType
					}
					req.Params.Arguments = args
					var result *mcp.CallToolResult
					var err error
					if op == "update" {
						result, err = h.ConversationsUpdateMessageHandler(context.Background(), req)
					} else {
						result, err = h.ConversationsDeleteMessageHandler(context.Background(), req)
					}
					if tc.wantError != "" {
						require.ErrorContains(t, err, tc.wantError)
						require.Nil(t, result)
					} else {
						require.NoError(t, err)
						require.Contains(t, result.Content[0].(mcp.TextContent).Text, "C123")
						require.Contains(t, result.Content[0].(mcp.TextContent).Text, "1234567890.123456")
					}
					if !tc.wantCall {
						require.Zero(t, calls)
						return
					}
					require.Equal(t, 1, calls, "a mutation must not be retried")
					require.Equal(t, "C123", form.Get("channel"))
					require.Equal(t, tc.timestamp, form.Get("ts"))
					if op == "update" {
						if tc.bot {
							require.Equal(t, "true", form.Get("as_user"))
						} else {
							require.Empty(t, form.Get("as_user"))
						}
						require.Equal(t, tc.text, form.Get("text"), "replace stale notification fallback")
						if tc.contentType == "text/plain" || tc.text == "---" {
							require.Empty(t, form.Get("blocks"), "text without blocks removes old blocks")
							require.Equal(t, "false", form.Get("mrkdwn"))
						} else {
							require.Contains(t, form.Get("blocks"), "type")
						}
					}
				})
			}
		})
	}
}

func TestUnitUpdateMessageInvalidText(t *testing.T) {
	t.Setenv("SLACK_MCP_UPDATE_MESSAGE_TOOL", "true")
	for _, args := range []map[string]interface{}{
		{"text": ""}, {"text": 123}, {"text": "replacement", "content_type": "text/html"},
	} {
		args["channel_id"] = "C123"
		args["timestamp"] = "1234567890.123456"
		req := mcp.CallToolRequest{}
		req.Params.Arguments = args
		// Any accidental Slack invocation panics through the nil embedded API.
		h := &ConversationsHandler{apiProvider: mutationProvider{}, logger: zap.NewNop()}
		_, err := h.ConversationsUpdateMessageHandler(context.Background(), req)
		require.Error(t, err)
	}
}
