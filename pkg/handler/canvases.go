package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// canvasEditOperations are the operations accepted by canvases.edit.
// insert_before and insert_after additionally require a section_id.
var canvasEditOperations = []string{
	"insert_after",
	"insert_at_end",
	"insert_at_start",
	"insert_before",
	"replace",
	"delete",
}

type CanvasSection struct {
	ID string `json:"id"`
}

type CanvasReadResponse struct {
	CanvasID string          `json:"canvas_id"`
	Title    string          `json:"title,omitempty"`
	Content  string          `json:"content"`
	Sections []CanvasSection `json:"sections,omitempty"`
}

type CanvasCreateResponse struct {
	CanvasID string `json:"canvas_id"`
	Title    string `json:"title,omitempty"`
}

type CanvasesHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
}

func NewCanvasesHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *CanvasesHandler {
	return &CanvasesHandler{
		apiProvider: apiProvider,
		logger:      logger,
	}
}

// CanvasesReadHandler returns a canvas's content along with the section IDs
// needed to target an edit. Slack serves canvas content as HTML, not markdown.
func (h *CanvasesHandler) CanvasesReadHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasesReadHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	canvasID := request.GetString("canvas_id", "")
	if canvasID == "" {
		return nil, errors.New("canvas_id must be provided")
	}

	includeSections := request.GetBool("include_sections", true)
	containsText := request.GetString("contains_text", "")

	api := h.apiProvider.Slack()

	// Canvases are files, so the metadata and the download URL come from files.info.
	file, _, _, err := api.GetFileInfoContext(ctx, canvasID, 0, 0)
	if err != nil {
		h.logger.Error("Failed to get canvas file info",
			zap.String("canvas_id", canvasID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to read canvas %s: %w", canvasID, err)
	}

	content, err := h.downloadCanvas(ctx, file.URLPrivateDownload)
	if err != nil {
		h.logger.Error("Failed to download canvas content",
			zap.String("canvas_id", canvasID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to download canvas %s: %w", canvasID, err)
	}

	response := CanvasReadResponse{
		CanvasID: canvasID,
		Title:    file.Title,
		Content:  content,
	}

	// Section IDs are embedded in the content as `id` attributes, so they are
	// parsed from there rather than fetched. canvases.sections.lookup requires a
	// criteria filter and cannot enumerate every section, so it is only used
	// when the caller is actually filtering.
	if includeSections {
		if containsText != "" {
			sections, err := api.LookupCanvasSectionsContext(ctx, slack.LookupCanvasSectionsParams{
				CanvasID: canvasID,
				Criteria: slack.LookupCanvasSectionsCriteria{
					ContainsText: containsText,
				},
			})
			if err != nil {
				h.logger.Error("Failed to look up canvas sections",
					zap.String("canvas_id", canvasID),
					zap.Error(err),
				)
				return nil, fmt.Errorf("failed to look up sections in canvas %s: %w", canvasID, err)
			}
			for _, section := range sections {
				response.Sections = append(response.Sections, CanvasSection{ID: section.ID})
			}
		} else {
			response.Sections = parseSectionIDs(content)
		}
	}

	out, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(out)), nil
}

// CanvasesCreateHandler creates a standalone canvas from markdown.
func (h *CanvasesHandler) CanvasesCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasesCreateHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	title := request.GetString("title", "")
	markdown := request.GetString("markdown", "")
	if markdown == "" {
		return nil, errors.New("markdown must be provided")
	}

	h.logger.Debug("Request parameters",
		zap.String("title", title),
		zap.Int("markdown_length", len(markdown)),
	)

	canvasID, err := h.apiProvider.Slack().CreateCanvasContext(ctx, title, slack.DocumentContent{
		Type:     "markdown",
		Markdown: markdown,
	})
	if err != nil {
		h.logger.Error("CreateCanvasContext failed", zap.Error(err))
		return nil, err
	}

	h.logger.Debug("Created canvas", zap.String("canvas_id", canvasID), zap.String("title", title))

	result := CanvasCreateResponse{
		CanvasID: canvasID,
		Title:    title,
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		h.logger.Error("Failed to marshal created canvas to JSON", zap.Error(err))
		return nil, err
	}

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// CanvasesEditHandler applies a single change to an existing canvas.
func (h *CanvasesHandler) CanvasesEditHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasesEditHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	canvasID := request.GetString("canvas_id", "")
	if canvasID == "" {
		return nil, errors.New("canvas_id must be provided")
	}

	operation := request.GetString("operation", "")
	if operation == "" {
		return nil, errors.New("operation must be provided")
	}
	if !slices.Contains(canvasEditOperations, operation) {
		return nil, fmt.Errorf("operation must be one of %s, got %q",
			strings.Join(canvasEditOperations, ", "), operation)
	}

	sectionID := request.GetString("section_id", "")
	if sectionID == "" && requiresSection(operation) {
		return nil, fmt.Errorf("section_id is required for operation %q; "+
			"use canvases_read to look up section IDs", operation)
	}

	markdown := request.GetString("markdown", "")
	if markdown == "" && operation != "delete" {
		return nil, errors.New("markdown must be provided for operations other than delete")
	}

	change := slack.CanvasChange{
		Operation: operation,
		SectionID: sectionID,
		DocumentContent: slack.DocumentContent{
			Type:     "markdown",
			Markdown: markdown,
		},
	}

	err := h.apiProvider.Slack().EditCanvasContext(ctx, slack.EditCanvasParams{
		CanvasID: canvasID,
		Changes:  []slack.CanvasChange{change},
	})
	if err != nil {
		h.logger.Error("Failed to edit canvas",
			zap.String("canvas_id", canvasID),
			zap.String("operation", operation),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to edit canvas %s: %w", canvasID, err)
	}

	h.logger.Debug("Canvas edited",
		zap.String("canvas_id", canvasID),
		zap.String("operation", operation),
	)

	return mcp.NewToolResultText(
		fmt.Sprintf("Applied %q to canvas %s.", operation, canvasID),
	), nil
}

// downloadCanvas fetches canvas content from its private download URL. It goes
// through the Slack client so the request carries the same auth as the API,
// which works for both xoxp and xoxc/xoxd tokens.
func (h *CanvasesHandler) downloadCanvas(ctx context.Context, url string) (string, error) {
	if url == "" {
		return "", errors.New("canvas has no downloadable content URL")
	}

	var buf bytes.Buffer
	if err := h.apiProvider.Slack().GetFileContext(ctx, url, &buf); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// sectionIDPattern matches the `id` attribute Slack embeds on each canvas
// block, e.g. `<h2 id="temp:C:AQB...">`.
var sectionIDPattern = regexp.MustCompile(`id="(temp:[^"]+)"`)

// parseSectionIDs extracts section IDs from canvas content in document order.
// canvases.sections.lookup cannot enumerate sections without a filter, so the
// IDs are read from the content itself.
func parseSectionIDs(content string) []CanvasSection {
	matches := sectionIDPattern.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(matches))
	sections := make([]CanvasSection, 0, len(matches))
	for _, match := range matches {
		id := match[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		sections = append(sections, CanvasSection{ID: id})
	}
	return sections
}

func requiresSection(operation string) bool {
	return operation == "insert_before" ||
		operation == "insert_after" ||
		operation == "replace" ||
		operation == "delete"
}
