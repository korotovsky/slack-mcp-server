package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func property(t *testing.T, tool mcp.Tool, name string) map[string]any {
	t.Helper()

	raw, ok := tool.InputSchema.Properties[name]
	require.Truef(t, ok, "tool %q has no %q property", tool.Name, name)
	prop, ok := raw.(map[string]any)
	require.Truef(t, ok, "property %q is %T, want map[string]any", name, raw)

	return prop
}

func description(t *testing.T, tool mcp.Tool, name string) string {
	t.Helper()

	desc, ok := property(t, tool, name)["description"].(string)
	require.Truef(t, ok, "property %q has no string description", name)

	return desc
}

// assertMentions keeps the contract assertions substring-based so wording can be
// polished without touching the test, while the rules themselves stay pinned.
func assertMentions(t *testing.T, subject string, desc string, tokens ...string) {
	t.Helper()

	lower := strings.ToLower(desc)
	for _, token := range tokens {
		assert.Containsf(t, lower, strings.ToLower(token), "%s description must mention %q", subject, token)
	}
}

// The TestUnit prefix is load-bearing rather than stylistic: `make test` runs
// `go test -run=".*Unit.*"` (see Makefile), so a test named without it never
// executes in CI.
func TestUnitConversationsAddMessageToolSchema(t *testing.T) {
	tool := newConversationsAddMessageTool()

	assert.Equal(t, ToolConversationsAddMessage, tool.Name)
	assert.Equal(t, "object", tool.InputSchema.Type)
	assert.Equal(t, []string{"channel_id"}, tool.InputSchema.Required,
		"text and blocks are individually optional; the handler requires one of them at runtime")

	for _, name := range []string{"channel_id", "thread_ts", "text", "content_type", "blocks"} {
		if assert.Contains(t, tool.InputSchema.Properties, name) {
			assert.Equal(t, "string", property(t, tool, name)["type"], "property %q", name)
		}
	}
	assert.NotContains(t, tool.InputSchema.Properties, "payload",
		"payload stays an undocumented backward-compatibility alias")

	contentType := property(t, tool, "content_type")
	assert.Equal(t, "text/markdown", contentType["default"])
	assert.ElementsMatch(t, []string{"text/markdown", "text/plain"}, contentType["enum"],
		"the enum must stay in sync with the values parseParamsToolAddMessage accepts")

	// What clients actually receive is the marshalled schema, not the Go map.
	encoded, err := json.Marshal(tool)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"enum":["text/markdown","text/plain"]`)
}

// Each token below pins one rule that callers demonstrably get wrong, so losing
// any of them regresses the contract silently. newConversationsAddMessageTool
// documents why each rule exists.
func TestUnitConversationsAddMessageDescribesMarkdownDialect(t *testing.T) {
	tool := newConversationsAddMessageTool()

	assertMentions(t, "tool", tool.Description, "text/markdown", "Slack mrkdwn", "silently")

	textDesc := description(t, tool, "text")
	assertMentions(t, "text", textDesc,
		"Slack mrkdwn",
		"`<url|label>`", "`<url>`", "`[label](url)`", "silently deleted",
		"`<@U123>`", "literal text",
		"single newlines", "no separator", "blank line", "`- `", "`• `",
		"`**bold**`", "`*text*`", "italic",
		"tables", "task lists", "~~strikethrough~~",
		"text/plain",
	)
	for _, wrong := range []string{"GitHub-flavored", "GFM"} {
		assert.NotContainsf(t, textDesc, wrong,
			"the converter runs goldmark without extension.GFM, so %q would be a false promise", wrong)
	}

	assertMentions(t, "content_type", description(t, tool, "content_type"),
		"text/markdown", "text/plain", "Block Kit", "Slack mrkdwn", "blocks")

	assertMentions(t, "blocks", description(t, tool, "blocks"),
		"JSON-encoded string", "Block Kit", "content_type", "fallback", "notifications", "accessibility", "At least one")

	threadTsDesc := description(t, tool, "thread_ts")
	assertMentions(t, "thread_ts", threadTsDesc, "parent message", "1234567890.123456", "Optional")
	assert.NotContains(t, threadTsDesc, "message in the thread_ts",
		"the inherited sentence was garbled mid-clause")
}
