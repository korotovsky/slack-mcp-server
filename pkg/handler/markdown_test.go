package handler

import (
	"testing"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	slackGoUtil "github.com/takara2314/slack-go-util"
)

// TestUnitMarkdownKeepsSingleNewlines pins the behaviour the conversations_add_message
// tool description promises for content_type=text/markdown: a single newline inside a
// paragraph survives as a line break. slack-go-util < v0.4.1 dropped soft line breaks,
// so "line one\nline two" was posted as "line oneline two"; this fails on a downgrade.
func TestUnitMarkdownKeepsSingleNewlines(t *testing.T) {
	blocks, err := slackGoUtil.ConvertMarkdownTextToBlocks(
		"Hello <@U0123ABCD>\nWe received a reversal request.\n• Transaction ID: 985d\n• Amount: 990.10 GHS\n\nThanks")
	require.NoError(t, err)
	require.Len(t, blocks, 2)

	section, ok := blocks[0].(*slack.SectionBlock)
	require.Truef(t, ok, "first block should be a section, got %T", blocks[0])
	assert.Equal(t, slack.MarkdownType, section.Text.Type)
	assert.Equal(t, "Hello <@U0123ABCD>\nWe received a reversal request.\n• Transaction ID: 985d\n• Amount: 990.10 GHS", section.Text.Text)
}

// TestUnitMarkdownKeepsHardLineBreaks covers the CommonMark hard break forms —
// two trailing spaces and a trailing backslash. goldmark flags those line ends as
// hard rather than soft breaks, and slack-go-util < v0.4.3 only honoured soft ones,
// so "line one  \nline two" was posted as "line oneline two".
func TestUnitMarkdownKeepsHardLineBreaks(t *testing.T) {
	blocks, err := slackGoUtil.ConvertMarkdownTextToBlocks("line one  \nline two\\\nline three")
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	section, ok := blocks[0].(*slack.SectionBlock)
	require.Truef(t, ok, "block should be a section, got %T", blocks[0])
	assert.Equal(t, slack.MarkdownType, section.Text.Type)
	assert.Equal(t, "line one\nline two\nline three", section.Text.Text)
}

// TestUnitMarkdownKeepsBlockquoteParagraphBreaks pins the second half of the
// v0.4.3 fix: goldmark trims the newline off a paragraph's last line, so before
// v0.4.3 consecutive paragraphs inside a blockquote were concatenated into
// "first paragraphsecond paragraph".
func TestUnitMarkdownKeepsBlockquoteParagraphBreaks(t *testing.T) {
	blocks, err := slackGoUtil.ConvertMarkdownTextToBlocks("> first paragraph\n>\n> second paragraph")
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	rich, ok := blocks[0].(*slack.RichTextBlock)
	require.Truef(t, ok, "block should be a rich text block, got %T", blocks[0])
	require.Len(t, rich.Elements, 1)

	quote, ok := rich.Elements[0].(*slack.RichTextQuote)
	require.Truef(t, ok, "element should be a quote, got %T", rich.Elements[0])
	require.Len(t, quote.Elements, 1)

	text, ok := quote.Elements[0].(*slack.RichTextSectionTextElement)
	require.Truef(t, ok, "quote element should be text, got %T", quote.Elements[0])
	assert.Equal(t, "first paragraph\n\nsecond paragraph", text.Text)
}
