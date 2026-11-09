package handler

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarkdownToRichText(t *testing.T) {
	blocks := markdownToRichText("Hi team,\n\n**1. Heading (attached)**\n- first **bold** item\n- see [docs](https://example.com)\n  - nested `code`\n\nClosing line.")
	require.Len(t, blocks, 1)
	elements := blocks[0]["elements"].([]map[string]any)
	require.Len(t, elements, 4)

	assert.Equal(t, "rich_text_section", elements[0]["type"])
	first := elements[0]["elements"].([]map[string]any)
	assert.Equal(t, "Hi team,\n\n", first[0]["text"])
	assert.Equal(t, "1. Heading (attached)", first[1]["text"])
	assert.Equal(t, map[string]any{"bold": true}, first[1]["style"])

	assert.Equal(t, "rich_text_list", elements[1]["type"])
	assert.Equal(t, 0, elements[1]["indent"])
	assert.Len(t, elements[1]["elements"], 2)
	assert.Equal(t, 1, elements[2]["indent"])

	last := elements[3]["elements"].([]map[string]any)
	assert.Equal(t, "Closing line.", last[0]["text"])

	raw, err := json.Marshal(blocks)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"text":"docs","type":"link","url":"https://example.com"`)
	assert.Contains(t, string(raw), `"style":{"code":true}`)
}

func TestRichTextPreview(t *testing.T) {
	raw := `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"Hello"},{"type":"link","text":"there","url":"https://x"}]},{"type":"rich_text_list","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"item"}]}]}]}]`
	var blocks any
	require.NoError(t, json.Unmarshal([]byte(raw), &blocks))
	assert.Equal(t, "Hellothere item", richTextPreview(blocks, 100))
	assert.Equal(t, "Hello…", richTextPreview(blocks, 5))
}

func TestDraftsToolConfig(t *testing.T) {
	t.Setenv("SLACK_MCP_DRAFTS_TOOL", "")
	t.Setenv("SLACK_MCP_ENABLED_TOOLS", "")
	_, err := draftsToolConfig()
	assert.Error(t, err)
	t.Setenv("SLACK_MCP_ENABLED_TOOLS", "drafts_create")
	config, err := draftsToolConfig()
	assert.NoError(t, err)
	assert.Equal(t, "true", config)
	t.Setenv("SLACK_MCP_DRAFTS_TOOL", "C123")
	config, err = draftsToolConfig()
	assert.NoError(t, err)
	assert.Equal(t, "C123", config)
}
