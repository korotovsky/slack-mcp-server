package text

import (
	"testing"

	"github.com/slack-go/slack"
)

func TestBlocksToText(t *testing.T) {
	tests := []struct {
		name   string
		blocks slack.Blocks
		want   string
	}{
		{
			name:   "empty blocks",
			blocks: slack.Blocks{},
			want:   "",
		},
		{
			name: "header block",
			blocks: slack.Blocks{
				BlockSet: []slack.Block{
					&slack.HeaderBlock{
						Type: slack.MBTHeader,
						Text: &slack.TextBlockObject{
							Type: "plain_text",
							Text: "Important Header",
						},
					},
				},
			},
			want: "Important Header",
		},
		{
			name: "section block with text",
			blocks: slack.Blocks{
				BlockSet: []slack.Block{
					&slack.SectionBlock{
						Type: slack.MBTSection,
						Text: &slack.TextBlockObject{
							Type: "mrkdwn",
							Text: "Hello from section",
						},
					},
				},
			},
			want: "Hello from section",
		},
		{
			name: "section block with fields",
			blocks: slack.Blocks{
				BlockSet: []slack.Block{
					&slack.SectionBlock{
						Type: slack.MBTSection,
						Text: &slack.TextBlockObject{
							Type: "mrkdwn",
							Text: "Main text",
						},
						Fields: []*slack.TextBlockObject{
							{Type: "mrkdwn", Text: "Field 1"},
							{Type: "mrkdwn", Text: "Field 2"},
						},
					},
				},
			},
			want: "Main text Field 1 Field 2",
		},
		{
			name: "context block",
			blocks: slack.Blocks{
				BlockSet: []slack.Block{
					&slack.ContextBlock{
						Type: slack.MBTContext,
						ContextElements: slack.ContextElements{
							Elements: []slack.MixedElement{
								&slack.TextBlockObject{
									Type: "mrkdwn",
									Text: "context info",
								},
							},
						},
					},
				},
			},
			want: "context info",
		},
		{
			name: "rich text block with text elements",
			blocks: slack.Blocks{
				BlockSet: []slack.Block{
					&slack.RichTextBlock{
						Type: slack.MBTRichText,
						Elements: []slack.RichTextElement{
							&slack.RichTextSection{
								Type: slack.RTESection,
								Elements: []slack.RichTextSectionElement{
									&slack.RichTextSectionTextElement{
										Type: slack.RTSEText,
										Text: "Hello ",
									},
									&slack.RichTextSectionTextElement{
										Type: slack.RTSEText,
										Text: "World",
									},
								},
							},
						},
					},
				},
			},
			want: "Hello World",
		},
		{
			name: "rich text block with link element",
			blocks: slack.Blocks{
				BlockSet: []slack.Block{
					&slack.RichTextBlock{
						Type: slack.MBTRichText,
						Elements: []slack.RichTextElement{
							&slack.RichTextSection{
								Type: slack.RTESection,
								Elements: []slack.RichTextSectionElement{
									&slack.RichTextSectionTextElement{
										Type: slack.RTSEText,
										Text: "Click ",
									},
									&slack.RichTextSectionLinkElement{
										Type: slack.RTSELink,
										URL:  "https://example.com",
										Text: "here",
									},
								},
							},
						},
					},
				},
			},
			want: "Click here",
		},
		{
			name: "rich text link without display text falls back to URL",
			blocks: slack.Blocks{
				BlockSet: []slack.Block{
					&slack.RichTextBlock{
						Type: slack.MBTRichText,
						Elements: []slack.RichTextElement{
							&slack.RichTextSection{
								Type: slack.RTESection,
								Elements: []slack.RichTextSectionElement{
									&slack.RichTextSectionLinkElement{
										Type: slack.RTSELink,
										URL:  "https://example.com",
									},
								},
							},
						},
					},
				},
			},
			want: "https://example.com",
		},
		{
			name: "multiple blocks combined",
			blocks: slack.Blocks{
				BlockSet: []slack.Block{
					&slack.HeaderBlock{
						Type: slack.MBTHeader,
						Text: &slack.TextBlockObject{
							Type: "plain_text",
							Text: "Alert",
						},
					},
					&slack.SectionBlock{
						Type: slack.MBTSection,
						Text: &slack.TextBlockObject{
							Type: "mrkdwn",
							Text: "Server is down",
						},
					},
					&slack.ContextBlock{
						Type: slack.MBTContext,
						ContextElements: slack.ContextElements{
							Elements: []slack.MixedElement{
								&slack.TextBlockObject{
									Type: "plain_text",
									Text: "sent by monitoring",
								},
							},
						},
					},
				},
			},
			want: "Alert Server is down sent by monitoring",
		},
		{
			name: "divider and image blocks are skipped",
			blocks: slack.Blocks{
				BlockSet: []slack.Block{
					&slack.SectionBlock{
						Type: slack.MBTSection,
						Text: &slack.TextBlockObject{
							Type: "mrkdwn",
							Text: "visible text",
						},
					},
					&slack.DividerBlock{
						Type: slack.MBTDivider,
					},
				},
			},
			want: "visible text",
		},
		{
			name: "rich text with broadcast element",
			blocks: slack.Blocks{
				BlockSet: []slack.Block{
					&slack.RichTextBlock{
						Type: slack.MBTRichText,
						Elements: []slack.RichTextElement{
							&slack.RichTextSection{
								Type: slack.RTESection,
								Elements: []slack.RichTextSectionElement{
									&slack.RichTextSectionBroadcastElement{
										Type:  slack.RTSEBroadcast,
										Range: "channel",
									},
									&slack.RichTextSectionTextElement{
										Type: slack.RTSEText,
										Text: " please review",
									},
								},
							},
						},
					},
				},
			},
			want: "@channel please review",
		},
		{
			name: "section block with nil text",
			blocks: slack.Blocks{
				BlockSet: []slack.Block{
					&slack.SectionBlock{
						Type: slack.MBTSection,
						Fields: []*slack.TextBlockObject{
							{Type: "mrkdwn", Text: "Only fields"},
						},
					},
				},
			},
			want: "Only fields",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BlocksToText(tt.blocks)
			if got != tt.want {
				t.Errorf("BlocksToText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAttachmentToTextWithBlocks(t *testing.T) {
	att := slack.Attachment{
		Blocks: slack.Blocks{
			BlockSet: []slack.Block{
				&slack.SectionBlock{
					Type: slack.MBTSection,
					Text: &slack.TextBlockObject{
						Type: "mrkdwn",
						Text: "block content",
					},
				},
			},
		},
	}

	result := AttachmentToText(att)
	if result != "Blocks: block content" {
		t.Errorf("AttachmentToText() = %q, want %q", result, "Blocks: block content")
	}
}

func TestAttachmentToTextWithBlocksAndText(t *testing.T) {
	att := slack.Attachment{
		Title: "Email Subject",
		Text:  "email body",
		Blocks: slack.Blocks{
			BlockSet: []slack.Block{
				&slack.SectionBlock{
					Type: slack.MBTSection,
					Text: &slack.TextBlockObject{
						Type: "mrkdwn",
						Text: "extra block info",
					},
				},
			},
		},
	}

	result := AttachmentToText(att)
	expected := "Title: Email Subject; Text: email body; Blocks: extra block info"
	if result != expected {
		t.Errorf("AttachmentToText() = %q, want %q", result, expected)
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
