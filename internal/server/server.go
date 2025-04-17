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

	s.AddTool(mcp.NewTool("conversationsHistory",
		mcp.WithDescription("Get messages from the channel by channelID"),
		mcp.WithString("channelID",
			mcp.Required(),
			mcp.Description("ID of the channel in format Cxxxxxxxxxx"),
		),
		mcp.WithString("limit",
			mcp.DefaultString("28"),
			mcp.Description("Limit of messages to fetch"),
		),
	), conversationsHandler.ConversationsHistoryHandler)

	channelsHandler := handler.NewChannelsHandler(provider)

	s.AddTool(mcp.NewTool("channelsList",
		mcp.WithDescription("Get list of channels"),
		mcp.WithArray("channelTypes",
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
