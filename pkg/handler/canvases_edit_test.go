package handler

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestCanvasEditValidation(t *testing.T) {
	tests := []struct {
		name, op, section, markdown string
		confirm, valid              bool
	}{
		{"append", "insert_at_end", "", "hello", false, true},
		{"prepend", "insert_at_start", "", "hello", false, true},
		{"insert before", "insert_before", "temp:section", "hello", false, true},
		{"insert after", "insert_after", "temp:section", "hello", false, true},
		{"replace section", "replace", "temp:section", "hello", false, true},
		{"delete section", "delete", "temp:section", "", false, true},
		{"replace entire confirmed", "replace", "", "hello", true, true},
		{"replace entire blocked", "replace", "", "hello", false, false},
		{"whitespace section blocked", "replace", " ", "hello", false, false},
		{"delete entire blocked", "delete", "", "", true, false},
		{"before needs section", "insert_before", "", "hello", false, false},
		{"after needs section", "insert_after", "", "hello", false, false},
		{"append forbids section", "insert_at_end", "temp:section", "hello", false, false},
		{"empty content", "replace", "temp:section", "  ", false, false},
		{"delete content", "delete", "temp:section", "hello", false, false},
		{"unknown", "oops", "", "hello", false, false},
		{"too large", "insert_at_end", "", strings.Repeat("a", 1048577), false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]any{"canvas_id": "F123ABC", "operation": tc.op, "section_id": tc.section, "markdown": tc.markdown, "confirm_replace_entire_canvas": tc.confirm}
			params, err := parseCanvasEdit(request)
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, params.Changes, 1)
			require.Equal(t, tc.op, params.Changes[0].Operation)
			require.Equal(t, tc.markdown, params.Changes[0].DocumentContent.Markdown)
		})
	}
	for _, id := range []string{"", "C123ABC", "F123/../../", "https://slack.com/canvas/F123"} {
		r := mcp.CallToolRequest{}
		r.Params.Arguments = map[string]any{"canvas_id": id, "operation": "insert_at_end", "markdown": "hello"}
		_, err := parseCanvasEdit(r)
		require.Error(t, err)
	}
}
