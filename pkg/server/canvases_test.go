package server

import (
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCanvasRegistration(t *testing.T) {
	t.Setenv("SLACK_MCP_CANVASES_EDIT_TOOL", "")
	s := server.NewMCPServer("test", "1")
	registerCanvasTools(s, nil, nil)
	require.NotNil(t, s.GetTool(ToolConversationsCanvasInfo))
	require.NotNil(t, s.GetTool(ToolCanvasesSectionsLookup))
	require.NotNil(t, s.GetTool(ToolCanvasesRead))
	require.Nil(t, s.GetTool(ToolCanvasesEdit))
	t.Setenv("SLACK_MCP_CANVASES_EDIT_TOOL", "true")
	s = server.NewMCPServer("test", "1")
	registerCanvasTools(s, nil, nil)
	require.NotNil(t, s.GetTool(ToolCanvasesEdit))
	s = server.NewMCPServer("test", "1")
	registerCanvasTools(s, nil, []string{ToolChannelsList})
	require.Empty(t, s.ListTools())
	s = server.NewMCPServer("test", "1")
	registerCanvasTools(s, nil, []string{ToolCanvasesEdit})
	require.Len(t, s.ListTools(), 1)
	s = server.NewMCPServer("test", "1")
	registerCanvasTools(s, nil, []string{ToolCanvasesRead})
	require.Len(t, s.ListTools(), 1)
	require.NotNil(t, s.GetTool(ToolCanvasesRead))
}
