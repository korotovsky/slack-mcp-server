package server

import (
	"fmt"
	"github.com/korotovsky/slack-mcp-server/internal/handler"
	"github.com/korotovsky/slack-mcp-server/internal/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type MCPServer struct {
	server *server.MCPServer
}

func NewMCPServer(provider *provider.ApiProvider) *MCPServer {
	s := server.NewMCPServer(
		"Slack MCP Server",
		"1.0.0",
		server.WithLogging(),
		server.WithRecovery(),
	)

	conversationsHandler := handler.NewConversationsHandler(provider)

	s.AddTool(mcp.NewTool("conversations_history",
		mcp.WithDescription("Get messages from the channel by channel_id, the last row/column in the response is used as 'cursor' parameter for pagination if not empty"),
		mcp.WithString("channel_id",
			mcp.Required(),
			mcp.Description("ID of the channel in format Cxxxxxxxxxx"),
		),
		mcp.WithString("cursor",
			mcp.Description("Cursor for pagination. Use the value of the last row and column in the response as next_cursor field returned from the previous request."),
		),
		mcp.WithString("limit",
			mcp.DefaultString("1d"),
			mcp.Description("Limit of messages to fetch in format of maximum ranges of time (e.g. 1d - 1 day, 30d - 30 days, 90d - 90 days which is a default limit for free tier history) or number of messages (e.g. 50). Must be empty when 'cursor' is provided."),
		),
	), conversationsHandler.ConversationsHistoryHandler)

	channelsHandler := handler.NewChannelsHandler(provider)

	s.AddTool(mcp.NewTool("channels_list",
		mcp.WithDescription("Get list of channels"),
		mcp.WithArray("channel_types",
			mcp.Required(),
			mcp.Description("Possible channel types. Allowed values: 'mpim', 'im', 'public_channel', 'private_channel'."),
			mcp.Items(map[string]any{
				"type": "string",
			}),
		),
		mcp.WithString("sort",
			mcp.Description("Type of sorting. Allowed values: 'popularity' - sort by number of members/participants in each channel."),
		),
	), channelsHandler.ChannelsHandler)

	return &MCPServer{
		server: s,
	}
}

func (s *MCPServer) ServeSSE(addr string) *server.SSEServer {
	return server.NewSSEServer(s.server,
		server.WithBaseURL(fmt.Sprintf("http://%s", addr)),
		server.WithSSEContextFunc(authFromRequest),
	)
}

func (s *MCPServer) ServeStdio() error {
	return server.ServeStdio(s.server)
}
