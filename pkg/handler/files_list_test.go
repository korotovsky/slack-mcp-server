package handler

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestUnitParseParamsToolFilesList(t *testing.T) {
	ch := &ConversationsHandler{}

	tests := []struct {
		name    string
		args    map[string]any
		want    filesListParams
		wantErr bool
	}{
		{
			name: "defaults when no args provided",
			args: map[string]any{},
			want: filesListParams{limit: 50},
		},
		{
			name: "channel_id and user_id set",
			args: map[string]any{"channel_id": "C0123456789", "user_id": "U0123456789"},
			want: filesListParams{channel: "C0123456789", user: "U0123456789", limit: 50},
		},
		{
			name: "types and cursor set",
			args: map[string]any{"types": "images,pdfs", "cursor": "dXNlcjpXMDYxTkZU"},
			want: filesListParams{types: "images,pdfs", cursor: "dXNlcjpXMDYxTkZU", limit: 50},
		},
		{
			name: "custom valid limit",
			args: map[string]any{"limit": "10"},
			want: filesListParams{limit: 10},
		},
		{
			name: "limit at max boundary",
			args: map[string]any{"limit": "200"},
			want: filesListParams{limit: 200},
		},
		{
			name: "non-numeric limit falls back to default",
			args: map[string]any{"limit": "abc"},
			want: filesListParams{limit: 50},
		},
		{
			name: "zero limit ignored, falls back to default",
			args: map[string]any{"limit": "0"},
			want: filesListParams{limit: 50},
		},
		{
			name: "negative limit ignored, falls back to default",
			args: map[string]any{"limit": "-1"},
			want: filesListParams{limit: 50},
		},
		{
			name: "all params set",
			args: map[string]any{
				"channel_id": "C0123456789",
				"user_id":    "U0123456789",
				"types":      "images",
				"limit":      "25",
				"cursor":     "abc123",
			},
			want: filesListParams{
				channel: "C0123456789",
				user:    "U0123456789",
				types:   "images",
				limit:   25,
				cursor:  "abc123",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := mcp.CallToolRequest{}
			req.Params.Arguments = tt.args

			got, err := ch.parseParamsToolFilesList(req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseParamsToolFilesList() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if got.channel != tt.want.channel {
				t.Errorf("channel: got %q, want %q", got.channel, tt.want.channel)
			}
			if got.user != tt.want.user {
				t.Errorf("user: got %q, want %q", got.user, tt.want.user)
			}
			if got.types != tt.want.types {
				t.Errorf("types: got %q, want %q", got.types, tt.want.types)
			}
			if got.cursor != tt.want.cursor {
				t.Errorf("cursor: got %q, want %q", got.cursor, tt.want.cursor)
			}
			if got.limit != tt.want.limit {
				t.Errorf("limit: got %d, want %d", got.limit, tt.want.limit)
			}
		})
	}
}
