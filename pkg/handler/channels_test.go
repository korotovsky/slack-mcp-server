package handler

import (
	"context"
	"encoding/csv"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/korotovsky/slack-mcp-server/pkg/test/util"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testEnv struct {
	mcpClient *client.Client
	ctx       context.Context
}

type matchingRule struct {
	csvFieldName    string
	csvFieldValueRE string
}

func setupTestEnv(t *testing.T) (*testEnv, func()) {
	t.Helper()

	sseKey := uuid.New().String()
	require.NotEmpty(t, sseKey, "sseKey must be generated for integration tests")

	cfg := util.MCPConfig{
		SSEKey:             sseKey,
		MessageToolEnabled: true,
		MessageToolMark:    true,
	}

	mcpServer, err := util.SetupMCP(cfg)
	require.NoError(t, err, "Failed to set up MCP server")

	fwd, err := util.SetupForwarding(context.Background(), "http://"+mcpServer.Host+":"+strconv.Itoa(mcpServer.Port))
	require.NoError(t, err, "Failed to set up ngrok forwarding")

	sseURL := fmt.Sprintf("%s://%s/sse", fwd.URL.Scheme, fwd.URL.Host)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

	mcpClient, err := client.NewSSEMCPClient(sseURL,
		client.WithHeaders(map[string]string{
			"Authorization": "Bearer " + sseKey,
		}),
	)
	require.NoError(t, err, "Failed to create MCP client")

	err = mcpClient.Start(ctx)
	require.NoError(t, err, "Failed to start MCP client")

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "channels-test-client",
		Version: "1.0.0",
	}
	initReq.Params.Capabilities = mcp.ClientCapabilities{}

	_, err = mcpClient.Initialize(ctx, initReq)
	require.NoError(t, err, "Failed to initialize MCP client")

	cleanup := func() {
		cancel()
		mcpClient.Close()
		fwd.Shutdown()
		mcpServer.Shutdown()
	}

	return &testEnv{
		mcpClient: mcpClient,
		ctx:       ctx,
	}, cleanup
}

func runChannelTest(t *testing.T, env *testEnv, channelType string, expectedChannels []matchingRule) {
	t.Helper()

	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "channels_list"
	callReq.Params.Arguments = map[string]any{
		"channel_types": channelType,
	}

	result, err := env.mcpClient.CallTool(env.ctx, callReq)
	require.NoError(t, err, "Tool call failed")
	require.NotNil(t, result, "Tool result is nil")
	require.False(t, result.IsError, "Tool returned error")

	var toolOutput strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(mcp.TextContent); ok {
			toolOutput.WriteString(textContent.Text)
		}
	}

	require.NotEmpty(t, toolOutput.String(), "No tool output captured")

	reader := csv.NewReader(strings.NewReader(toolOutput.String()))
	rows, err := reader.ReadAll()
	require.NoError(t, err, "Failed to parse CSV")
	require.GreaterOrEqual(t, len(rows), 1, "CSV must have at least a header row")

	header := rows[0]
	dataRows := rows[1:]
	colIndex := map[string]int{}
	for i, col := range header {
		colIndex[col] = i
	}

	for _, rule := range expectedChannels {
		idx, ok := colIndex[rule.csvFieldName]
		require.Truef(t, ok, "CSV did not contain column %q, toolOutput: %q", rule.csvFieldName, toolOutput.String())

		re, err := regexp.Compile(rule.csvFieldValueRE)
		require.NoErrorf(t, err, "Invalid regex %q", rule.csvFieldValueRE)

		found := false
		for _, row := range dataRows {
			if idx < len(row) && re.MatchString(row[idx]) {
				found = true
				break
			}
		}
		assert.Truef(t, found, "No row in column %q matched %q; full CSV:\n%s",
			rule.csvFieldName, rule.csvFieldValueRE, toolOutput.String())
	}
}

func TestIntegrationPublicChannelsList(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	expectedChannels := []matchingRule{
		{csvFieldName: "Name", csvFieldValueRE: `^#general$`},
		{csvFieldName: "Name", csvFieldValueRE: `^#testcase-1$`},
		{csvFieldName: "Name", csvFieldValueRE: `^#testcase-2$`},
		{csvFieldName: "Name", csvFieldValueRE: `^#testcase-3$`},
	}

	runChannelTest(t, env, "public_channel", expectedChannels)
}

func TestIntegrationPrivateChannelsList(t *testing.T) {
	env, cleanup := setupTestEnv(t)
	defer cleanup()

	expectedChannels := []matchingRule{
		{csvFieldName: "Name", csvFieldValueRE: `^#testcase-4$`},
	}

	runChannelTest(t, env, "private_channel", expectedChannels)
}
