package handler

// Canvas reading follows the files.info -> authenticated download approach from
// shreyansqt's upstream PR #331. Unlike that PR, tool registration is independent,
// content is never logged, downloads are bounded, and IDs are parsed as HTML.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"golang.org/x/net/html"
)

const maxCanvasReadBytes = 5 * 1024 * 1024

type canvasReadResponse struct {
	CanvasID    string                `json:"canvas_id"`
	Title       string                `json:"title"`
	Content     string                `json:"content"`
	ContentType string                `json:"content_type"`
	Sections    []slack.CanvasSection `json:"sections,omitempty"`
}

type canvasBuffer struct{ buffer bytes.Buffer }

func (b *canvasBuffer) String() string { return b.buffer.String() }

func (b *canvasBuffer) Write(p []byte) (int, error) {
	if len(p) > maxCanvasReadBytes-b.buffer.Len() {
		return 0, fmt.Errorf("canvas exceeds 5 MiB read limit; no partial content returned")
	}
	return b.buffer.Write(p)
}

func parseCanvasSectionIDs(content string) []slack.CanvasSection {
	var sections []slack.CanvasSection
	seen := make(map[string]bool)
	tokens := html.NewTokenizer(strings.NewReader(content))
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			return sections
		case html.StartTagToken, html.SelfClosingTagToken:
			for _, attr := range tokens.Token().Attr {
				if attr.Key == "id" && strings.HasPrefix(attr.Val, "temp:") && !seen[attr.Val] {
					seen[attr.Val] = true
					sections = append(sections, slack.CanvasSection{ID: attr.Val})
				}
			}
		}
	}
}

// Never accept arbitrary caller-supplied URLs for authenticated downloads.
func validCanvasDownloadURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "files.slack.com" || host == "files.slack-gov.com"
}

func readCanvas(ctx context.Context, api provider.SlackAPI, request mcp.CallToolRequest) (*canvasReadResponse, error) {
	id := request.GetString("canvas_id", "")
	if !canvasIDPattern.MatchString(id) {
		return nil, fmt.Errorf("canvas_id must be a Slack file ID beginning with F")
	}
	include := request.GetBool("include_sections", true)
	query := request.GetString("contains_text", "")
	if query != "" && !include {
		return nil, fmt.Errorf("contains_text requires include_sections=true")
	}
	file, _, _, err := api.GetFileInfoContext(ctx, id, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot read canvas metadata: %w", err)
	}
	if file == nil || file.ID != id || file.Filetype != "quip" {
		return nil, fmt.Errorf("Slack did not return the requested canvas file")
	}
	if !validCanvasDownloadURL(file.URLPrivateDownload) {
		return nil, fmt.Errorf("canvas has no supported HTTPS Slack download URL")
	}
	var buf canvasBuffer
	// Hide download errors that may contain signed private URLs.
	if err := api.GetFileContext(ctx, file.URLPrivateDownload, &buf); err != nil {
		return nil, fmt.Errorf("canvas download failed or exceeded 5 MiB; no partial content returned")
	}
	result := &canvasReadResponse{CanvasID: id, Title: file.Title, Content: buf.String(), ContentType: "text/html"}
	if include {
		if query != "" {
			result.Sections, err = api.LookupCanvasSectionsContext(ctx, slack.LookupCanvasSectionsParams{CanvasID: id, Criteria: slack.LookupCanvasSectionsCriteria{ContainsText: query}})
			if err != nil {
				return nil, fmt.Errorf("cannot look up canvas sections: %w", err)
			}
		} else {
			result.Sections = parseCanvasSectionIDs(result.Content)
		}
	}
	return result, nil
}

func (ch *ConversationsHandler) CanvasesReadHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	result, err := readCanvas(ctx, ch.apiProvider.Slack(), request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(data)), nil
}

var _ io.Writer = (*canvasBuffer)(nil)
