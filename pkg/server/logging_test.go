package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestUnitLoggerOmitsToolPayloads(t *testing.T) {
	for _, name := range []string{ToolFileUpload, ToolConversationsUpdateMessage} {
		t.Run(name, func(t *testing.T) {
			core, observed := observer.New(zap.DebugLevel)
			logger := zap.New(core)
			req := mcp.CallToolRequest{}
			req.Params.Name = name
			req.Params.Arguments = map[string]any{"content": "private payload", "initial_comment": "private caption", "text": "private replacement"}
			called := false
			h := buildLoggerMiddleware(logger)(func(_ context.Context, got mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				called = true
				require.Equal(t, req.Params.Arguments, got.Params.Arguments)
				return mcp.NewToolResultText("ok"), nil
			})
			_, err := h(context.Background(), req)
			require.NoError(t, err)
			require.True(t, called)
			require.Len(t, observed.All(), 2)
			for _, entry := range observed.All() {
				fields := entry.ContextMap()
				require.Equal(t, name, fields["tool"])
				require.NotContains(t, fields, "params")
			}
		})
	}
}
