package text

import (
	"testing"

	"github.com/slack-go/slack"
)

func TestFilesToText(t *testing.T) {
	tests := []struct {
		name  string
		files []slack.File
		want  string
	}{
		{
			name:  "empty files",
			files: nil,
			want:  "",
		},
		{
			// Covers: non-email filtering, From name+address, CC name+address,
			// CC address-only, "/" separator, "@" → "at" conversion, Subject
			name: "extracts email metadata and skips non-email files",
			files: []slack.File{
				{Filetype: "pdf", Title: "report.pdf"},
				{
					Filetype: "email",
					Subject:  "Team Update",
					From: []slack.EmailFileUserInfo{
						{Name: "Alice", Address: "alice@example.com"},
					},
					Cc: []slack.EmailFileUserInfo{
						{Name: "Bob Smith", Address: "bob@example.com"},
						{Address: "carol@example.com"},
					},
				},
				{Filetype: "png", Title: "chart.png"},
			},
			want: "Email, From: Alice - alice at example.com, CC: Bob Smith - bob at example.com/carol at example.com, Subject: Team Update",
		},
		{
			name: "from with name only",
			files: []slack.File{
				{
					Filetype: "email",
					Subject:  "Test",
					From: []slack.EmailFileUserInfo{
						{Name: "Support Team"},
					},
				},
			},
			want: "Email, From: Support Team, Subject: Test",
		},
		{
			name: "from with address only",
			files: []slack.File{
				{
					Filetype: "email",
					Subject:  "Test",
					From: []slack.EmailFileUserInfo{
						{Address: "noreply@example.com"},
					},
				},
			},
			want: "Email, From: noreply at example.com, Subject: Test",
		},
		{
			// Covers: Mode-based detection and Title → Subject fallback
			name: "mode detection with title fallback",
			files: []slack.File{
				{Mode: "email", Title: "Fwd: Hello"},
			},
			want: "Email, Subject: Fwd: Hello",
		},
		{
			name: "multiple email files",
			files: []slack.File{
				{Filetype: "email", Subject: "First"},
				{Filetype: "email", Subject: "Second"},
			},
			want: "Email, Subject: First Email, Subject: Second",
		},
		{
			name: "empty from and cc entries skipped",
			files: []slack.File{
				{
					Filetype: "email",
					Subject:  "Newsletter",
					From:     []slack.EmailFileUserInfo{{Name: "", Address: ""}},
					Cc:       []slack.EmailFileUserInfo{{Name: "", Address: ""}},
				},
			},
			want: "Email, Subject: Newsletter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilesToText(tt.files)
			if got != tt.want {
				t.Errorf("FilesToText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFilesToTextProcessTextPipeline verifies that FilesToText output
// survives the ProcessText pipeline (filterSpecialChars) without losing structure.
func TestFilesToTextProcessTextPipeline(t *testing.T) {
	tests := []struct {
		name  string
		files []slack.File
		want  string
	}{
		{
			// Covers: format survival, unicode \p{L}\p{M}, CC "/" separator
			name: "format with unicode and cc survives",
			files: []slack.File{
				{
					Filetype: "email",
					Subject:  "R\u00e9union g\u00e9n\u00e9rale",
					From: []slack.EmailFileUserInfo{
						{Name: "Ren\u00e9 M\u00fcller", Address: "rene@example.com"},
					},
					Cc: []slack.EmailFileUserInfo{
						{Address: "bob@example.com"},
					},
				},
			},
			want: "Email, From: Ren\u00e9 M\u00fcller - rene at example.com, CC: bob at example.com, Subject: R\u00e9union g\u00e9n\u00e9rale",
		},
		{
			// Covers: $, [], () stripped; URL preserved by placeholder mechanism
			name: "special chars stripped and URL preserved",
			files: []slack.File{
				{
					Filetype: "email",
					Subject:  "[Alert] $100 payment - https://example.com/invoice?id=42",
					From: []slack.EmailFileUserInfo{
						{Name: "Billing", Address: "billing@example.com"},
					},
				},
			},
			want: "Email, From: Billing - billing at example.com, Subject: Alert 100 payment - https://example.com/invoice?id=42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := FilesToText(tt.files)
			got := ProcessText(raw)
			if got != tt.want {
				t.Errorf("ProcessText(FilesToText()) = %q, want %q\n  raw = %q", got, tt.want, raw)
			}
		})
	}
}

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
	tests := []struct {
		name     string
		input    string
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterSpecialChars(tt.input)
			if result != tt.expected {
				t.Errorf("filterSpecialChars() = %q, expected %q", result, tt.expected)
			}
		})
	}
}
