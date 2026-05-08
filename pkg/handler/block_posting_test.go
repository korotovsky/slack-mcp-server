package handler

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestParseRawBlocksAcceptsSlackTableBlock(t *testing.T) {
	blocks, err := parseRawBlocks(mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]any{
				"blocks": []any{
					map[string]any{
						"type": "table",
						"rows": []any{
							[]any{
								map[string]any{"type": "raw_text", "text": "Header"},
							},
							[]any{
								map[string]any{"type": "raw_text", "text": "Value"},
							},
						},
					},
				},
			},
		},
	})

	require.NoError(t, err)
	require.Len(t, blocks, 1)
	require.Equal(t, "table", string(blocks[0].BlockType()))
}

func TestParseRawBlocksRejectsBlockWithoutType(t *testing.T) {
	_, err := parseRawBlocks(mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]any{
				"blocks": []any{map[string]any{"text": "missing type"}},
			},
		},
	})

	require.ErrorContains(t, err, "blocks[0].type is required")
}
