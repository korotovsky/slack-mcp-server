package text

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// BlocksToText converts Slack block kit structures into plain text.
// Returns empty string if no text content is found in blocks.
func BlocksToText(blocks slack.Blocks) string {
	if len(blocks.BlockSet) == 0 {
		return ""
	}

	var parts []string
	for _, block := range blocks.BlockSet {
		t := blockToText(block)
		if t != "" {
			parts = append(parts, t)
		}
	}

	return strings.Join(parts, "\n")
}

func blockToText(block slack.Block) string {
	switch b := block.(type) {
	case *slack.SectionBlock:
		return sectionBlockToText(b)
	case *slack.HeaderBlock:
		return headerBlockToText(b)
	case *slack.RichTextBlock:
		return richTextBlockToText(b)
	case *slack.ContextBlock:
		return contextBlockToText(b)
	case *slack.DividerBlock:
		return ""
	default:
		return ""
	}
}

func sectionBlockToText(b *slack.SectionBlock) string {
	var parts []string

	if b.Text != nil && b.Text.Text != "" {
		parts = append(parts, b.Text.Text)
	}

	for _, field := range b.Fields {
		if field != nil && field.Text != "" {
			parts = append(parts, field.Text)
		}
	}

	return strings.Join(parts, "\n")
}

func headerBlockToText(b *slack.HeaderBlock) string {
	if b.Text != nil && b.Text.Text != "" {
		return b.Text.Text
	}
	return ""
}

func contextBlockToText(b *slack.ContextBlock) string {
	var parts []string
	for _, elem := range b.ContextElements.Elements {
		switch e := elem.(type) {
		case *slack.TextBlockObject:
			if e.Text != "" {
				parts = append(parts, e.Text)
			}
		case *slack.ImageBlockElement:
			if e.AltText != "" {
				parts = append(parts, e.AltText)
			}
		}
	}
	return strings.Join(parts, " ")
}

func richTextBlockToText(b *slack.RichTextBlock) string {
	var parts []string
	for _, elem := range b.Elements {
		t := richTextElementToText(elem)
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

func richTextElementToText(elem slack.RichTextElement) string {
	switch e := elem.(type) {
	case *slack.RichTextSection:
		return richTextSectionToText(e.Elements)
	case *slack.RichTextList:
		return richTextListToText(e)
	case *slack.RichTextQuote:
		section := slack.RichTextSection(*e)
		t := richTextSectionToText(section.Elements)
		if t != "" {
			return "> " + strings.ReplaceAll(t, "\n", "\n> ")
		}
		return ""
	case *slack.RichTextPreformatted:
		t := richTextSectionToText(e.RichTextSection.Elements)
		if t != "" {
			return "```\n" + t + "\n```"
		}
		return ""
	default:
		return ""
	}
}

func richTextSectionToText(elements []slack.RichTextSectionElement) string {
	var parts []string
	for _, elem := range elements {
		t := richTextSectionElementToText(elem)
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "")
}

func richTextSectionElementToText(elem slack.RichTextSectionElement) string {
	switch e := elem.(type) {
	case *slack.RichTextSectionTextElement:
		return e.Text
	case *slack.RichTextSectionLinkElement:
		if e.Text != "" {
			return fmt.Sprintf("%s (%s)", e.Text, e.URL)
		}
		return e.URL
	case *slack.RichTextSectionEmojiElement:
		return fmt.Sprintf(":%s:", e.Name)
	case *slack.RichTextSectionChannelElement:
		return fmt.Sprintf("#%s", e.ChannelID)
	case *slack.RichTextSectionUserElement:
		return fmt.Sprintf("@%s", e.UserID)
	case *slack.RichTextSectionBroadcastElement:
		return fmt.Sprintf("@%s", e.Range)
	case *slack.RichTextSectionDateElement:
		if e.Fallback != nil {
			return *e.Fallback
		}
		return ""
	case *slack.RichTextSectionColorElement:
		return e.Value
	case *slack.RichTextSectionUserGroupElement:
		return fmt.Sprintf("@%s", e.UsergroupID)
	default:
		return ""
	}
}

func richTextListToText(list *slack.RichTextList) string {
	var lines []string
	for i, elem := range list.Elements {
		t := richTextElementToText(elem)
		if t == "" {
			continue
		}
		prefix := "- "
		if list.Style == slack.RTEListOrdered {
			prefix = fmt.Sprintf("%d. ", i+list.Offset+1)
		}
		indent := strings.Repeat("  ", list.Indent)
		lines = append(lines, indent+prefix+t)
	}
	return strings.Join(lines, "\n")
}

// MergeBlocksWithText appends block-derived text to the message text
// when blocks contain content not already present in the text field.
func MergeBlocksWithText(msgText string, blocks slack.Blocks) string {
	blockText := BlocksToText(blocks)
	if blockText == "" {
		return msgText
	}
	if msgText == "" {
		return blockText
	}
	// If the text field already contains the block content, skip
	if strings.Contains(msgText, blockText) {
		return msgText
	}
	return msgText + "\n" + blockText
}
