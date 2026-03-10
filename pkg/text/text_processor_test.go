package text

import (
	"testing"

	"github.com/slack-go/slack"
)

func TestIsUnfurlingEnabled(t *testing.T) {
	tests := []struct {
		name string
		opt  string
		text string
		want bool
	}{
		{
			name: "no domains",
			opt:  "example.com,foo.io",
			text: "Hello world, no domains here.",
			want: true,
		},
		{
			name: "allowed URL",
			opt:  "example.com,foo.io",
			text: "Check this link: http://example.com/page",
			want: true,
		},
		{
			name: "disallowed URL",
			opt:  "example.com,foo.io",
			text: "Visit http://bad.com now",
			want: false,
		},
		{
			name: "allowed bare domain",
			opt:  "example.com,foo.io",
			text: "Visit example.com for info.",
			want: true,
		},
		{
			name: "disallowed bare domain",
			opt:  "example.com,foo.io",
			text: "Visit bad.com for info.",
			want: false,
		},
		{
			name: "multiple allowed mixed",
			opt:  "example.com,foo.io",
			text: "example.com and foo.io and https://example.com/test",
			want: true,
		},
		{
			name: "one disallowed among many",
			opt:  "example.com,foo.io",
			text: "example.com and bar.org",
			want: false,
		},
		{
			name: "subdomain not allowed",
			opt:  "example.com,foo.io",
			text: "Visit sub.example.com",
			want: false,
		},
		{
			name: "bare domain with port",
			opt:  "example.com",
			text: "Service at example.com:8080 is running",
			want: true,
		},
		{
			name: "invalid TLD skipped",
			opt:  "example.com",
			text: "Check foo.invalidtld and example.com",
			want: true, // foo.invalidtld is ignored, example.com is allowed
		},
		{
			name: "allowed subdomain check",
			opt:  "sub.example.com,bar.com",
			text: "Check sub.example.com forsubdomain",
			want: true,
		},
		{
			name: "enable for all - YOLO mode",
			opt:  "yes",
			text: "YOLO mode, any link works http://anydomain.com",
			want: true,
		},
		{
			name: "enable for all - YOLO mode",
			opt:  "1",
			text: "YOLO mode, any link works http://anydomain.com",
			want: true,
		},
		{
			name: "enable for all - YOLO mode",
			opt:  "true",
			text: "YOLO mode, any link works http://anydomain.com",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsUnfurlingEnabled(tt.text, tt.opt, nil)
			if got != tt.want {
				t.Fatalf("opt=%q text=%q → got %v; want %v",
					tt.opt, tt.text, got, tt.want)
			}
		})
	}
}

func TestFilterSpecialCharsWithCommas(t *testing.T) {
	usersMap := map[string]slack.User{
		"U12345678": {Name: "alice"},
		"W87654321": {Name: "bob"},
	}
	tests := []struct {
		name     string
		input    string
		userMap  map[string]slack.User
		expected string
	}{
		{
			name:     "Slack-style link in middle",
			input:    "aaabbcc <https://google.com|This is a link> aabbcc",
			expected: "aaabbcc https://google.com - This is a link, aabbcc",
		},
		{
			name:     "Slack-style link at end",
			input:    "aaabbcc <https://google.com|This is a link>",
			expected: "aaabbcc https://google.com - This is a link",
		},
		{
			name:     "Slack-style link at end with spaces",
			input:    "aaabbcc <https://google.com|This is a link>   ",
			expected: "aaabbcc https://google.com - This is a link",
		},
		{
			name:     "Two links, second at end",
			input:    "First <https://site1.com|Site One> then <https://site2.com|Site Two>",
			expected: "First https://site1.com - Site One, then https://site2.com - Site Two",
		},
		{
			name:     "Two links, text after second",
			input:    "First <https://site1.com|Site One> then <https://site2.com|Site Two> done",
			expected: "First https://site1.com - Site One, then https://site2.com - Site Two, done",
		},
		{
			name:     "Markdown link at end",
			input:    "Check this [Google](https://google.com)",
			expected: "Check this https://google.com - Google",
		},
		{
			name:     "Markdown link in middle",
			input:    "Check this [Google](https://google.com) out",
			expected: "Check this https://google.com - Google, out",
		},
		{
			name:     "known user mention resolved to name",
			input:    "Hey <@U12345678> how are you?",
			userMap:  usersMap,
			expected: "Hey @alice how are you?",
		},
		{
			name:     "unknown Slack bot mention",
			input:    "Hey <@B12348765> how are you?",
			userMap:  usersMap,
			expected: "Hey B12348765 how are you?",
		},
		{
			name:     "unknown user mention falls back to ID",
			input:    "Hey <@UUNKNOWN99> sup",
			userMap:  usersMap,
			expected: "Hey @UUNKNOWN99 sup",
		},
		{
			name:     "multiple known mentions",
			input:    "<@U12345678> and <@W87654321> are here",
			userMap:  usersMap,
			expected: "@alice and @bob are here",
		},
		{
			name:     "W-prefixed user ID resolved",
			input:    "Ping <@W87654321>",
			userMap:  usersMap,
			expected: "Ping @bob",
		},
		{
			name:     "mention alongside a link",
			input:    "<@U12345678> check <https://example.com|this>",
			userMap:  usersMap,
			expected: "@alice check https://example.com - this",
		},
		{
			name:     "empty users map falls back to ID",
			input:    "Hi <@U12345678>",
			userMap:  map[string]slack.User{},
			expected: "Hi @U12345678",
		},
		{
			name:     "normal text same to user ID",
			input:    "this machine is model-U12345678",
			userMap:  usersMap,
			expected: "this machine is model-U12345678",
		},
		{
			name:     "invalid Slack user mention format as normal text",
			input:    "I like <@  W87654321 > @U12345678 <U12345678> <@>",
			userMap:  usersMap,
			expected: "I like W87654321 U12345678 U12345678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterSpecialChars(tt.input, tt.userMap)
			if result != tt.expected {
				t.Errorf("filterSpecialChars() = %q, expected %q", result, tt.expected)
			}
		})
	}
}
