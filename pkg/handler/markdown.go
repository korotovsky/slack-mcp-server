package handler

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/slack-go/slack"
	slackGoUtil "github.com/takara2314/slack-go-util"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// paragraphMarkdown parses paragraph inlines. Linkify and Strikethrough only add
// inline parsers, so the block structure is the same as slack-go-util's parser.
var paragraphMarkdown = goldmark.New(goldmark.WithExtensions(extension.Linkify, extension.Strikethrough))

// slackTokenRe matches Slack's own inline syntax, which CommonMark leaves as text:
// <@U…>, <#C…>, <!here>, <!subteam^S…> and <url|label> references, and :emoji:.
var slackTokenRe = regexp.MustCompile(
	`<(@[UW][A-Z0-9]+|#[CG][A-Z0-9]+|!here|!channel|!everyone|!subteam\^[A-Z0-9]+|(?i:https?|mailto):[^|>\s]+)(?:\|([^>]*))?>` +
		`|:([a-z0-9_+\-]+):`)

var skinToneRe = regexp.MustCompile(`^:skin-tone-([2-6]):`)

// linkSchemeRe lists the URL schemes posted as link elements. Slack rejects
// the whole message over a link URL it does not accept, so others stay text.
var linkSchemeRe = regexp.MustCompile(`(?i)^(https?|mailto):`)

// markdownToBlocks converts text/markdown message text to Block Kit blocks.
//
// Headings, lists, code blocks and quotes come from slack-go-util unchanged.
// Its paragraphs, however, are mrkdwn section blocks, and Slack collapses long
// section text behind "Show more" — messages typed in the composer never do,
// because they are rich_text. So every top-level paragraph is rebuilt here as
// a rich_text section with real user, channel, link and emoji elements, and
// consecutive paragraphs share one block, separated by a blank line.
func markdownToBlocks(markdown string) ([]slack.Block, error) {
	blocks, err := slackGoUtil.ConvertMarkdownTextToBlocks(markdown)
	if err != nil {
		return nil, err
	}

	source := []byte(markdown)
	doc := paragraphMarkdown.Parser().Parse(text.NewReader(source))
	var paragraphs []ast.Node
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		if n.Kind() == ast.KindParagraph {
			paragraphs = append(paragraphs, n)
		}
	}

	// slack-go-util emits exactly one section block per top-level paragraph and
	// no other section blocks; if a future version breaks that pairing, keep its
	// output as is rather than risk reordering the message.
	sections := 0
	for _, b := range blocks {
		if _, ok := b.(*slack.SectionBlock); ok {
			sections++
		}
	}
	if sections != len(paragraphs) {
		return blocks, nil
	}

	out := make([]slack.Block, 0, len(blocks))
	var prev *slack.RichTextSection // paragraph section of the previous block, if it was one
	for _, b := range blocks {
		if _, ok := b.(*slack.SectionBlock); !ok {
			out = append(out, b)
			prev = nil
			continue
		}
		elements := paragraphElements(paragraphs[0], source)
		paragraphs = paragraphs[1:]
		if len(elements) == 0 {
			continue
		}
		if prev != nil {
			prev.Elements = appendText(prev.Elements, "\n\n", slack.RichTextSectionTextStyle{})
			prev.Elements = appendElements(prev.Elements, elements)
			continue
		}
		prev = slack.NewRichTextSection(elements...)
		out = append(out, slack.NewRichTextBlock("", prev))
	}
	return out, nil
}

// richTextBuilder turns the inlines of one paragraph into rich_text elements.
// Plain text is buffered until the style changes so Slack tokens split across
// goldmark text nodes are still recognised.
type richTextBuilder struct {
	source       []byte
	elements     []slack.RichTextSectionElement
	pending      strings.Builder
	pendingStyle slack.RichTextSectionTextStyle
}

func paragraphElements(paragraph ast.Node, source []byte) []slack.RichTextSectionElement {
	b := &richTextBuilder{source: source}
	b.walk(paragraph, slack.RichTextSectionTextStyle{})
	b.flush()
	return b.elements
}

func (b *richTextBuilder) walk(parent ast.Node, style slack.RichTextSectionTextStyle) {
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		switch n := n.(type) {
		case *ast.Text:
			b.text(string(n.Value(b.source)), style)
			if n.SoftLineBreak() || n.HardLineBreak() {
				b.text("\n", style)
			}
		case *ast.String:
			if n.IsCode() {
				code := style
				code.Code = true
				b.literal(string(n.Value), code)
			} else {
				b.text(string(n.Value), style)
			}
		case *ast.CodeSpan:
			code := style
			code.Code = true
			b.literal(b.plainText(n, true), code)
		case *ast.Emphasis:
			inner := style
			if n.Level >= 2 {
				inner.Bold = true
			} else {
				inner.Italic = true
			}
			b.walk(n, inner)
		case *east.Strikethrough:
			inner := style
			inner.Strike = true
			b.walk(n, inner)
		case *ast.Link:
			b.link(unescape(string(n.Destination)), b.plainText(n, false), style)
		case *ast.Image:
			b.link(unescape(string(n.Destination)), b.plainText(n, false), style)
		case *ast.AutoLink:
			label := string(n.Label(b.source))
			if strings.ContainsRune(label, '|') {
				// CommonMark reads Slack's <url|label> and <!subteam^S…|@team>
				// as autolinks; hand them back to the Slack token parser.
				b.text("<"+label+">", style)
				continue
			}
			url := string(n.URL(b.source))
			if n.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(url), "mailto:") {
				url = "mailto:" + url
			}
			b.link(url, label, style)
		case *ast.RawHTML:
			// Includes Slack references CommonMark takes for HTML; anything
			// else stays literal text rather than being dropped.
			b.text(string(n.Segments.Value(b.source)), style)
		default:
			b.walk(n, style)
		}
	}
}

// text buffers raw (still backslash-escaped) paragraph text.
func (b *richTextBuilder) text(raw string, style slack.RichTextSectionTextStyle) {
	if style != b.pendingStyle {
		b.flush()
		b.pendingStyle = style
	}
	b.pending.WriteString(raw)
}

// literal appends final text that must not be scanned for Slack tokens.
func (b *richTextBuilder) literal(s string, style slack.RichTextSectionTextStyle) {
	b.flush()
	b.elements = appendText(b.elements, s, style)
}

func (b *richTextBuilder) link(url, label string, style slack.RichTextSectionTextStyle) {
	b.flush()
	if strings.HasPrefix(strings.ToLower(url), "www.") {
		url = "http://" + url
	}
	if !linkSchemeRe.MatchString(url) {
		switch {
		case label == "":
			label = url
		case url != "" && url != label:
			label += " (" + url + ")"
		}
		b.elements = appendText(b.elements, label, style)
		return
	}
	b.elements = append(b.elements, &slack.RichTextSectionLinkElement{
		Type:  slack.RTSELink,
		URL:   url,
		Text:  label,
		Style: stylePtr(style),
	})
}

// flush converts buffered raw text to text, user, channel, usergroup,
// broadcast, link and emoji elements.
func (b *richTextBuilder) flush() {
	if b.pending.Len() == 0 {
		return
	}
	raw := b.pending.String()
	b.pending.Reset()
	style := b.pendingStyle

	plainStart, pos := 0, 0
	for pos < len(raw) {
		loc := slackTokenRe.FindStringSubmatchIndex(raw[pos:])
		if loc == nil {
			break
		}
		for i := range loc {
			if loc[i] >= 0 {
				loc[i] += pos
			}
		}
		start, end := loc[0], loc[1]
		element := slackTokenElement(raw, loc, style)
		if element == nil || (start > 0 && raw[start-1] == '\\') {
			pos = start + 1
			continue
		}
		if emoji, ok := element.(*slack.RichTextSectionEmojiElement); ok {
			if m := skinToneRe.FindStringSubmatch(raw[end:]); m != nil {
				emoji.SkinTone, _ = strconv.Atoi(m[1])
				end += len(m[0])
			}
		}
		b.elements = appendText(b.elements, unescape(raw[plainStart:start]), style)
		b.elements = append(b.elements, element)
		plainStart, pos = end, end
	}
	b.elements = appendText(b.elements, unescape(raw[plainStart:]), style)
}

// slackTokenElement builds the element for one slackTokenRe match, or returns
// nil when the match is not a token in context (e.g. the ":30:" in "10:30:45").
func slackTokenElement(raw string, loc []int, style slack.RichTextSectionTextStyle) slack.RichTextSectionElement {
	group := func(i int) string {
		if loc[2*i] < 0 {
			return ""
		}
		return raw[loc[2*i]:loc[2*i+1]]
	}
	if name := group(3); name != "" {
		if (loc[0] > 0 && isASCIIAlnum(raw[loc[0]-1])) || (loc[1] < len(raw) && isASCIIAlnum(raw[loc[1]])) {
			return nil
		}
		return &slack.RichTextSectionEmojiElement{Type: slack.RTSEEmoji, Name: name}
	}

	target, label := group(1), group(2)
	switch {
	case strings.HasPrefix(target, "@"):
		return slack.NewRichTextSectionUserElement(target[1:], stylePtr(style))
	case strings.HasPrefix(target, "#"):
		return slack.NewRichTextSectionChannelElement(target[1:], stylePtr(style))
	case strings.HasPrefix(target, "!subteam^"):
		return slack.NewRichTextSectionUserGroupElement(strings.TrimPrefix(target, "!subteam^"))
	case strings.HasPrefix(target, "!"):
		return slack.NewRichTextSectionBroadcastElement(target[1:])
	default:
		return &slack.RichTextSectionLinkElement{
			Type:  slack.RTSELink,
			URL:   target,
			Text:  resolveReferences(label),
			Style: stylePtr(style),
		}
	}
}

// plainText concatenates the text below n; raw keeps code span content as is.
func (b *richTextBuilder) plainText(n ast.Node, raw bool) string {
	var sb strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			if raw {
				sb.Write(c.Value(b.source))
			} else {
				sb.WriteString(unescape(string(c.Value(b.source))))
			}
			if c.SoftLineBreak() || c.HardLineBreak() {
				sb.WriteByte(' ')
			}
		case *ast.String:
			sb.Write(c.Value)
		case *ast.AutoLink:
			sb.Write(c.Label(b.source))
		}
		return ast.WalkContinue, nil
	})
	if raw {
		// CommonMark turns line endings inside a code span into spaces.
		return strings.ReplaceAll(sb.String(), "\n", " ")
	}
	return sb.String()
}

// appendText appends s, merging it into the last element when that is text of
// the same style. Slack rejects empty text elements, so "" is dropped.
func appendText(elements []slack.RichTextSectionElement, s string, style slack.RichTextSectionTextStyle) []slack.RichTextSectionElement {
	if s == "" {
		return elements
	}
	if n := len(elements); n > 0 {
		if last, ok := elements[n-1].(*slack.RichTextSectionTextElement); ok && styleOf(last.Style) == style {
			last.Text += s
			return elements
		}
	}
	return append(elements, slack.NewRichTextSectionTextElement(s, stylePtr(style)))
}

func appendElements(elements, more []slack.RichTextSectionElement) []slack.RichTextSectionElement {
	for _, e := range more {
		if t, ok := e.(*slack.RichTextSectionTextElement); ok {
			elements = appendText(elements, t.Text, styleOf(t.Style))
			continue
		}
		elements = append(elements, e)
	}
	return elements
}

func stylePtr(style slack.RichTextSectionTextStyle) *slack.RichTextSectionTextStyle {
	if style == (slack.RichTextSectionTextStyle{}) {
		return nil
	}
	return &style
}

func styleOf(style *slack.RichTextSectionTextStyle) slack.RichTextSectionTextStyle {
	if style == nil {
		return slack.RichTextSectionTextStyle{}
	}
	return *style
}

// unescape applies CommonMark backslash escapes and character references.
func unescape(s string) string {
	return resolveReferences(string(util.UnescapePunctuations([]byte(s))))
}

func resolveReferences(s string) string {
	return string(util.ResolveEntityNames(util.ResolveNumericReferences([]byte(s))))
}

func isASCIIAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
