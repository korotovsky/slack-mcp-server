package text

import (
	"testing"
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

func TestProcessText_LinkNormalization(t *testing.T) {
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
		{
			name:     "HTML anchor at end",
			input:    `Visit <a href="https://example.com">Example</a>`,
			expected: "Visit https://example.com - Example",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProcessText(tt.input)
			if result != tt.expected {
				t.Errorf("ProcessText() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestProcessText_PreservesContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "apostrophes in contractions",
			input:    "I'll let you know what didn't work, it's fine",
			expected: "I'll let you know what didn't work, it's fine",
		},
		{
			name:     "straight double quotes",
			input:    `she said "hello" and left`,
			expected: `she said "hello" and left`,
		},
		{
			name:     "curly quotes from iOS",
			input:    "\u2018don\u2019t\u2019 \u201csay\u201d that",
			expected: "\u2018don\u2019t\u2019 \u201csay\u201d that",
		},
		{
			name:     "exclamation and question marks",
			input:    "wow! really?! amazing!!",
			expected: "wow! really?! amazing!!",
		},
		{
			name:     "parentheses and brackets",
			input:    "see note (important) and [aside]",
			expected: "see note (important) and [aside]",
		},
		{
			name:     "blockquote marker",
			input:    "> this was quoted",
			expected: "> this was quoted",
		},
		{
			name:     "currency and math",
			input:    "costs $5.00 (2+2 = 4)",
			expected: "costs $5.00 (2+2 = 4)",
		},
		{
			name:     "markdown emphasis",
			input:    "*bold* _italic_ ~strike~ `code`",
			expected: "*bold* _italic_ ~strike~ `code`",
		},
		{
			name:     "unicode emoji",
			input:    "great work \U0001F389 \U0001F44D",
			expected: "great work \U0001F389 \U0001F44D",
		},
		{
			name:     "raw slack mention markup",
			input:    "cc <@U0123ABC> in <#C0456DEF|general>",
			expected: "cc <@U0123ABC> in <#C0456DEF|general>",
		},
		{
			name:     "raw broadcast mention",
			input:    "<!channel> please review",
			expected: "<!channel> please review",
		},
		{
			name:     "preserves newlines, collapses inline spaces",
			input:    "first line\n\nsecond  line   with   gaps",
			expected: "first line\n\nsecond line with gaps",
		},
		{
			name:     "strips bidi override (prompt injection vector)",
			input:    "safe\u202etext",
			expected: "safetext",
		},
		{
			name:     "strips ZWSP and BOM",
			input:    "a\u200bb\ufeffc",
			expected: "abc",
		},
		{
			name:     "preserves ZWJ in family emoji sequence",
			input:    "hi \U0001F468\u200D\U0001F469\u200D\U0001F467 bye",
			expected: "hi \U0001F468\u200D\U0001F469\u200D\U0001F467 bye",
		},
		{
			name:     "preserves ZWJ and VS16 in rainbow flag",
			input:    "\U0001F3F3\uFE0F\u200D\U0001F308",
			expected: "\U0001F3F3\uFE0F\u200D\U0001F308",
		},
		{
			name:     "preserves ZWNJ in Persian text",
			input:    "\u0645\u06CC\u200C\u062E\u0648\u0627\u0647\u0645",
			expected: "\u0645\u06CC\u200C\u062E\u0648\u0627\u0647\u0645",
		},
		{
			name:     "strips DEL and C0 controls; tabs collapse to space, newlines kept",
			input:    "ok\x01\x7fmessage\twith\ntabs",
			expected: "okmessage with\ntabs",
		},
		{
			name: "twelve Slack-style links in one message (regression for placeholder bug)",
			input: "see <https://a.example/1|one> and <https://b.example/2|two> and <https://c.example/3|three> " +
				"and <https://d.example/4|four> and <https://e.example/5|five> and <https://f.example/6|six> " +
				"and <https://g.example/7|seven> and <https://h.example/8|eight> and <https://i.example/9|nine> " +
				"and <https://j.example/10|ten> and <https://k.example/11|eleven> and <https://l.example/12|twelve>",
			expected: "see https://a.example/1 - one, and https://b.example/2 - two, and https://c.example/3 - three, " +
				"and https://d.example/4 - four, and https://e.example/5 - five, and https://f.example/6 - six, " +
				"and https://g.example/7 - seven, and https://h.example/8 - eight, and https://i.example/9 - nine, " +
				"and https://j.example/10 - ten, and https://k.example/11 - eleven, and https://l.example/12 - twelve",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProcessText(tt.input)
			if result != tt.expected {
				t.Errorf("ProcessText(%q)\n  got:  %q\n  want: %q", tt.input, result, tt.expected)
			}
		})
	}
}
