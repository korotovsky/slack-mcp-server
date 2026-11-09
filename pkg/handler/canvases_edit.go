package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
)

var canvasIDPattern = regexp.MustCompile(`^F[A-Z0-9]+$`)

// Read channel metadata rather than guessing a canvas ID from the channel ID.
func (ch *ConversationsHandler) ConversationsCanvasInfoHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	channelID := request.GetString("channel_id", "")
	if !regexp.MustCompile(`^[CG][A-Z0-9]+$`).MatchString(channelID) {
		return mcp.NewToolResultError("channel_id must be an explicit Slack channel ID"), nil
	}
	channel, err := ch.apiProvider.Slack().GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: channelID})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Cannot read channel canvas: %v", err)), nil
	}
	if channel == nil {
		return mcp.NewToolResultError("Slack returned no channel"), nil
	}
	canvas := slack.Canvas{}
	if channel.Properties != nil {
		canvas = channel.Properties.Canvas
	}
	data, err := json.Marshal(map[string]any{"channel_id": channel.ID, "canvas": canvas})
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(data)), nil
}

func parseCanvasEdit(request mcp.CallToolRequest) (slack.EditCanvasParams, error) {
	params := slack.EditCanvasParams{CanvasID: request.GetString("canvas_id", "")}
	if !canvasIDPattern.MatchString(params.CanvasID) {
		return params, fmt.Errorf("canvas_id must be a Slack file ID beginning with F")
	}
	op := request.GetString("operation", "")
	section := request.GetString("section_id", "")
	if section != strings.TrimSpace(section) {
		return params, fmt.Errorf("section_id must not contain surrounding whitespace")
	}
	markdown := request.GetString("markdown", "")
	if utf8.RuneCountInString(markdown) > 1048576 {
		return params, fmt.Errorf("markdown exceeds Slack's 1 MiB character limit")
	}
	switch op {
	case "insert_before", "insert_after", "delete":
		if strings.TrimSpace(section) == "" {
			return params, fmt.Errorf("%s requires section_id", op)
		}
	case "insert_at_start", "insert_at_end":
		if section != "" {
			return params, fmt.Errorf("%s does not accept section_id", op)
		}
	case "replace":
		if section == "" && !request.GetBool("confirm_replace_entire_canvas", false) {
			return params, fmt.Errorf("whole-canvas replacement requires confirm_replace_entire_canvas=true; prefer a section_id")
		}
	default:
		return params, fmt.Errorf("unsupported canvas operation")
	}
	if op != "delete" && strings.TrimSpace(markdown) == "" {
		return params, fmt.Errorf("markdown is required for content edits")
	}
	if op == "delete" && markdown != "" {
		return params, fmt.Errorf("delete does not accept markdown")
	}
	params.Changes = []slack.CanvasChange{{Operation: op, SectionID: section, DocumentContent: slack.DocumentContent{Type: "markdown", Markdown: markdown}}}
	return params, nil
}

func (ch *ConversationsHandler) CanvasesEditHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params, err := parseCanvasEdit(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Do not retry mutations: an ambiguous network failure may follow a successful insert.
	if err := ch.apiProvider.Slack().EditCanvasContext(ctx, params); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Canvas edit failed: %v. Requires canvases:write and edit access. Do not blindly retry; verify the canvas first.", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Slack confirmed canvas %s was edited (%s).", params.CanvasID, params.Changes[0].Operation)), nil
}

func (ch *ConversationsHandler) CanvasesSectionsLookupHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := request.GetString("canvas_id", "")
	if !canvasIDPattern.MatchString(id) {
		return mcp.NewToolResultError("canvas_id must be a Slack file ID beginning with F"), nil
	}
	query := request.GetString("contains_text", "")
	if strings.TrimSpace(query) == "" {
		return mcp.NewToolResultError("contains_text is required"), nil
	}
	sections, err := ch.apiProvider.Slack().LookupCanvasSectionsContext(ctx, slack.LookupCanvasSectionsParams{CanvasID: id, Criteria: slack.LookupCanvasSectionsCriteria{ContainsText: query}})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Cannot look up canvas sections: %v. Requires canvases:read and canvas access.", err)), nil
	}
	data, err := json.Marshal(map[string]any{"canvas_id": id, "sections": sections})
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(data)), nil
}
