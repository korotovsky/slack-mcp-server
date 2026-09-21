package handler

import (
	"encoding/json"
	"testing"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func plain(s string) *slack.RichTextSectionTextElement {
	return slack.NewRichTextSectionTextElement(s, nil)
}

func styled(s string, style slack.RichTextSectionTextStyle) *slack.RichTextSectionTextElement {
	return slack.NewRichTextSectionTextElement(s, &style)
}

func user(id string) *slack.RichTextSectionUserElement {
	return slack.NewRichTextSectionUserElement(id, nil)
}

// paragraphSection asserts that block is a rich_text block holding a single
// rich_text_section and returns that section's elements.
func paragraphSection(t *testing.T, block slack.Block) []slack.RichTextSectionElement {
	t.Helper()
	rich, ok := block.(*slack.RichTextBlock)
	require.Truef(t, ok, "block should be rich_text, got %T", block)
	require.Len(t, rich.Elements, 1)
	section, ok := rich.Elements[0].(*slack.RichTextSection)
	require.Truef(t, ok, "element should be a rich_text_section, got %T", rich.Elements[0])
	return section.Elements
}

// TestUnitMarkdownParagraphIsRichText pins the fix for long text/markdown messages
// collapsing behind "Show more": slack-go-util turns a paragraph into a mrkdwn
// section block, which Slack truncates, so a paragraph must be posted as rich_text
// instead — with real user elements for mentions and its line breaks kept.
func TestUnitMarkdownParagraphIsRichText(t *testing.T) {
	blocks, err := markdownToBlocks(
		"Country owners:\n• Ghana: <@U0123ABCD>\n• Kenya: <@U0456EFGH>\n• Zambia: <@W0789IJKL|someone>")
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	assert.Equal(t, []slack.RichTextSectionElement{
		plain("Country owners:\n• Ghana: "),
		user("U0123ABCD"),
		plain("\n• Kenya: "),
		user("U0456EFGH"),
		plain("\n• Zambia: "),
		user("W0789IJKL"),
	}, paragraphSection(t, blocks[0]))

	j, err := json.Marshal(blocks[0])
	require.NoError(t, err)
	assert.Contains(t, string(j), `"type":"rich_text"`)
	assert.Contains(t, string(j), `{"type":"user","user_id":"U0123ABCD"}`)
	assert.Contains(t, string(j), `{"type":"text","text":"\n• Kenya: "}`)
	assert.NotContains(t, string(j), `"section"`)
}

func TestUnitMarkdownParagraphInlineElements(t *testing.T) {
	bold := slack.RichTextSectionTextStyle{Bold: true}
	tests := []struct {
		name     string
		markdown string
		want     []slack.RichTextSectionElement
	}{
		{
			name:     "emphasis, strikethrough and code",
			markdown: "**bold** *italic* ~~gone~~ ~also~ `x <@U1>` ***both***",
			want: []slack.RichTextSectionElement{
				styled("bold", bold), plain(" "),
				styled("italic", slack.RichTextSectionTextStyle{Italic: true}), plain(" "),
				styled("gone", slack.RichTextSectionTextStyle{Strike: true}), plain(" "),
				styled("also", slack.RichTextSectionTextStyle{Strike: true}), plain(" "),
				styled("x <@U1>", slack.RichTextSectionTextStyle{Code: true}), plain(" "),
				styled("both", slack.RichTextSectionTextStyle{Bold: true, Italic: true}),
			},
		},
		{
			name:     "mentions of users, channels, groups and everyone",
			markdown: "cc **<@U0123ABCD>** in <#C0123ABCD|general>, <!subteam^S0123ABCD|@it> and <!here>",
			want: []slack.RichTextSectionElement{
				plain("cc "),
				slack.NewRichTextSectionUserElement("U0123ABCD", &bold),
				plain(" in "),
				slack.NewRichTextSectionChannelElement("C0123ABCD", nil),
				plain(", "),
				slack.NewRichTextSectionUserGroupElement("S0123ABCD"),
				plain(" and "),
				slack.NewRichTextSectionBroadcastElement("here"),
			},
		},
		{
			name:     "links",
			markdown: "[docs](https://example.com/a_b) <https://example.com|short> <https://example.com/x|two words> https://example.com/bare www.example.org ops@example.com [rel](notes) [js](javascript:alert(1)) <ftp://x.org|old>",
			want: []slack.RichTextSectionElement{
				slack.NewRichTextSectionLinkElement("https://example.com/a_b", "docs", nil), plain(" "),
				slack.NewRichTextSectionLinkElement("https://example.com", "short", nil), plain(" "),
				slack.NewRichTextSectionLinkElement("https://example.com/x", "two words", nil), plain(" "),
				slack.NewRichTextSectionLinkElement("https://example.com/bare", "https://example.com/bare", nil), plain(" "),
				slack.NewRichTextSectionLinkElement("http://www.example.org", "www.example.org", nil), plain(" "),
				slack.NewRichTextSectionLinkElement("mailto:ops@example.com", "ops@example.com", nil),
				plain(" rel (notes) js (javascript:alert(1)) <ftp://x.org|old>"),
			},
		},
		{
			name:     "emoji shortcodes, but not times or ratios",
			markdown: "Done :white_check_mark: :+1::skin-tone-3: at 10:30:45, ratio 1:2:3",
			want: []slack.RichTextSectionElement{
				plain("Done "),
				slack.NewRichTextSectionEmojiElement("white_check_mark", 0, nil),
				plain(" "),
				slack.NewRichTextSectionEmojiElement("+1", 3, nil),
				plain(" at 10:30:45, ratio 1:2:3"),
			},
		},
		{
			name:     "hard line breaks, escapes and entities",
			markdown: "one  \ntwo\\\nthree \\*not bold\\* \\<@U0123ABCD> AT&amp;T",
			want: []slack.RichTextSectionElement{
				plain("one\ntwo\nthree *not bold* <@U0123ABCD> AT&T"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks, err := markdownToBlocks(tt.markdown)
			require.NoError(t, err)
			require.Len(t, blocks, 1)
			assert.Equal(t, tt.want, paragraphSection(t, blocks[0]))
		})
	}
}

// TestUnitMarkdownParagraphsShareBlock checks that consecutive paragraphs become
// one rich_text section separated by a blank line, as the composer posts them,
// while headings, lists, code and quotes still come from slack-go-util.
func TestUnitMarkdownParagraphsShareBlock(t *testing.T) {
	blocks, err := markdownToBlocks(
		"# Title\n\nFirst paragraph\nsecond line\n\nNext paragraph\n\n- item\n- item two\n\n```\ncode\n```\n\n> quoted\n\nLast")
	require.NoError(t, err)
	require.Len(t, blocks, 6)

	_, ok := blocks[0].(*slack.HeaderBlock)
	assert.Truef(t, ok, "block 0 should be a header, got %T", blocks[0])

	assert.Equal(t, []slack.RichTextSectionElement{
		plain("First paragraph\nsecond line\n\nNext paragraph"),
	}, paragraphSection(t, blocks[1]))

	for i, want := range []slack.RichTextElementType{slack.RTEList, slack.RTEPreformatted, slack.RTEQuote} {
		rich, ok := blocks[2+i].(*slack.RichTextBlock)
		require.Truef(t, ok, "block %d should be rich_text, got %T", 2+i, blocks[2+i])
		assert.Equal(t, want, rich.Elements[0].RichTextElementType())
	}

	assert.Equal(t, []slack.RichTextSectionElement{plain("Last")}, paragraphSection(t, blocks[5]))

	for i, b := range blocks {
		_, isSection := b.(*slack.SectionBlock)
		assert.Falsef(t, isSection, "block %d is a mrkdwn section, which Slack collapses", i)
	}
}
