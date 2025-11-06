package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/server/auth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

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

// CanvasesCreateHandler creates a new canvas
func (ch *CanvasesHandler) CanvasesCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("CanvasesCreateHandler called", zap.Any("params", request.Params.Arguments))

	// authentication
	if authenticated, err := auth.IsAuthenticated(ctx, ch.apiProvider.ServerTransport(), ch.logger); !authenticated {
		ch.logger.Error("Authentication failed for canvases_create", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	// provider readiness
	if ready, err := ch.apiProvider.IsReady(); !ready {
		ch.logger.Error("API provider not ready", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Parse parameters
	title := request.GetString("title", "")
	content := request.GetString("content", "")

	if content == "" {
		return mcp.NewToolResultError("content parameter is required"), nil
	}

	// Create document content
	documentContent := slack.DocumentContent{
		Type:     "markdown",
		Markdown: content,
	}

	// Create canvas
	canvasID, err := ch.apiProvider.Slack().CreateCanvasContext(ctx, title, documentContent)
	if err != nil {
		ch.logger.Error("Failed to create canvas",
			zap.String("title", title),
			zap.Error(err),
		)
		return mcp.NewToolResultError(fmt.Sprintf("Failed to create canvas: %v", err)), nil
	}

	ch.logger.Info("Canvas created successfully",
		zap.String("canvas_id", canvasID),
		zap.String("title", title),
	)

	// Return result
	result := map[string]interface{}{
		"canvas_id": canvasID,
		"title":     title,
		"message":   "Canvas created successfully",
	}

	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

// CanvasesEditHandler edits an existing canvas
func (ch *CanvasesHandler) CanvasesEditHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("CanvasesEditHandler called", zap.Any("params", request.Params.Arguments))

	// authentication
	if authenticated, err := auth.IsAuthenticated(ctx, ch.apiProvider.ServerTransport(), ch.logger); !authenticated {
		ch.logger.Error("Authentication failed for canvases_edit", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	// provider readiness
	if ready, err := ch.apiProvider.IsReady(); !ready {
		ch.logger.Error("API provider not ready", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Parse parameters
	canvasID := request.GetString("canvas_id", "")
	operation := request.GetString("operation", "")
	content := request.GetString("content", "")
	sectionID := request.GetString("section_id", "")

	if canvasID == "" {
		return mcp.NewToolResultError("canvas_id parameter is required"), nil
	}

	if operation == "" {
		operation = "insert_at_end" // default operation
	}

	if content == "" {
		return mcp.NewToolResultError("content parameter is required"), nil
	}

	// Validate operation
	validOperations := map[string]bool{
		"insert_at_start": true,
		"insert_at_end":   true,
		"insert_before":   true,
		"insert_after":    true,
		"replace":         true,
		"delete":          true,
	}

	if !validOperations[operation] {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid operation: %s. Must be one of: insert_at_start, insert_at_end, insert_before, insert_after, replace, delete", operation)), nil
	}

	// For operations that require section_id
	if (operation == "insert_before" || operation == "insert_after" || operation == "delete") && sectionID == "" {
		return mcp.NewToolResultError(fmt.Sprintf("section_id is required for operation: %s", operation)), nil
	}

	// Create document content
	documentContent := slack.DocumentContent{
		Type:     "markdown",
		Markdown: content,
	}

	// Create canvas change
	change := slack.CanvasChange{
		Operation:       operation,
		DocumentContent: documentContent,
	}

	if sectionID != "" {
		change.SectionID = sectionID
	}

	// Create edit params
	params := slack.EditCanvasParams{
		CanvasID: canvasID,
		Changes:  []slack.CanvasChange{change},
	}

	// Edit canvas
	err := ch.apiProvider.Slack().EditCanvasContext(ctx, params)
	if err != nil {
		ch.logger.Error("Failed to edit canvas",
			zap.String("canvas_id", canvasID),
			zap.String("operation", operation),
			zap.Error(err),
		)
		return mcp.NewToolResultError(fmt.Sprintf("Failed to edit canvas: %v", err)), nil
	}

	ch.logger.Info("Canvas edited successfully",
		zap.String("canvas_id", canvasID),
		zap.String("operation", operation),
	)

	// Return result
	result := map[string]interface{}{
		"canvas_id": canvasID,
		"operation": operation,
		"message":   "Canvas edited successfully",
	}

	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

// CanvasesSectionsLookupHandler looks up sections in a canvas
func (ch *CanvasesHandler) CanvasesSectionsLookupHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("CanvasesSectionsLookupHandler called", zap.Any("params", request.Params.Arguments))

	// authentication
	if authenticated, err := auth.IsAuthenticated(ctx, ch.apiProvider.ServerTransport(), ch.logger); !authenticated {
		ch.logger.Error("Authentication failed for canvases_sections_lookup", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	// provider readiness
	if ready, err := ch.apiProvider.IsReady(); !ready {
		ch.logger.Error("API provider not ready", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Parse parameters
	canvasID := request.GetString("canvas_id", "")
	containsText := request.GetString("contains_text", "")

	if canvasID == "" {
		return mcp.NewToolResultError("canvas_id parameter is required"), nil
	}

	// Create lookup params
	params := slack.LookupCanvasSectionsParams{
		CanvasID: canvasID,
		Criteria: slack.LookupCanvasSectionsCriteria{},
	}

	if containsText != "" {
		params.Criteria.ContainsText = containsText
	}

	// Lookup sections
	sections, err := ch.apiProvider.Slack().LookupCanvasSectionsContext(ctx, params)
	if err != nil {
		ch.logger.Error("Failed to lookup canvas sections",
			zap.String("canvas_id", canvasID),
			zap.Error(err),
		)
		return mcp.NewToolResultError(fmt.Sprintf("Failed to lookup canvas sections: %v", err)), nil
	}

	ch.logger.Info("Canvas sections lookup completed",
		zap.String("canvas_id", canvasID),
		zap.Int("sections_found", len(sections)),
	)

	// Convert sections to simple format
	type Section struct {
		ID string `json:"id"`
	}

	simpleSections := make([]Section, len(sections))
	for i, section := range sections {
		simpleSections[i] = Section{ID: section.ID}
	}

	// Return result
	result := map[string]interface{}{
		"canvas_id": canvasID,
		"sections":  simpleSections,
		"count":     len(sections),
	}

	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

// CanvasesReadHandler reads canvas content
func (ch *CanvasesHandler) CanvasesReadHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("CanvasesReadHandler called", zap.Any("params", request.Params.Arguments))

	// authentication
	if authenticated, err := auth.IsAuthenticated(ctx, ch.apiProvider.ServerTransport(), ch.logger); !authenticated {
		ch.logger.Error("Authentication failed for canvases_read", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	// provider readiness
	if ready, err := ch.apiProvider.IsReady(); !ready {
		ch.logger.Error("API provider not ready", zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Parse parameters
	canvasID := request.GetString("canvas_id", "")

	if canvasID == "" {
		return mcp.NewToolResultError("canvas_id parameter is required"), nil
	}

	// Get file info (canvases are files in Slack)
	file, _, _, err := ch.apiProvider.Slack().GetFileInfoContext(ctx, canvasID, 0, 0)
	if err != nil {
		ch.logger.Error("Failed to read canvas",
			zap.String("canvas_id", canvasID),
			zap.Error(err),
		)
		return mcp.NewToolResultError(fmt.Sprintf("Failed to read canvas: %v", err)), nil
	}

	ch.logger.Info("Canvas read successfully",
		zap.String("canvas_id", canvasID),
		zap.String("title", file.Title),
	)

	// Return result with canvas metadata and content
	result := map[string]interface{}{
		"canvas_id":   canvasID,
		"title":       file.Title,
		"name":        file.Name,
		"created":     file.Created,
		"timestamp":   file.Timestamp,
		"mimetype":    file.Mimetype,
		"filetype":    file.Filetype,
		"pretty_type": file.PrettyType,
		"size":        file.Size,
		"url":         file.URLPrivate,
		"permalink":   file.Permalink,
		"user":        file.User,
		"is_public":   file.IsPublic,
		"is_external": file.IsExternal,
		"editable":    file.Editable,
	}

	// Add preview/content if available
	if file.Preview != "" {
		result["preview"] = file.Preview
	}
	if file.PreviewHighlight != "" {
		result["preview_highlight"] = file.PreviewHighlight
	}

	// Download full canvas content if URLPrivateDownload is available
	if file.URLPrivateDownload != "" {
		content, err := ch.downloadCanvasContent(ctx, file.URLPrivateDownload)
		if err != nil {
			ch.logger.Warn("Failed to download canvas content",
				zap.String("canvas_id", canvasID),
				zap.Error(err),
			)
			result["content_error"] = fmt.Sprintf("Failed to download content: %v", err)
		} else {
			result["content"] = content
			ch.logger.Debug("Canvas content downloaded",
				zap.String("canvas_id", canvasID),
				zap.Int("content_length", len(content)),
			)
		}
	}

	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}

// downloadCanvasContent downloads the full canvas markdown content
func (ch *CanvasesHandler) downloadCanvasContent(ctx context.Context, downloadURL string) (string, error) {
	// Get client - it already has cookies configured for xoxc/xoxd or will use token for xoxp
	slackClient := ch.apiProvider.Slack().(*provider.MCPSlackClient)
	token := slackClient.Token()
	httpClient := slackClient.HTTPClient()

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// For OAuth tokens (xoxp), add Bearer authentication header
	// For browser tokens (xoxc/xoxd), the HTTP client's cookies will be used automatically
	if token != "" && len(token) > 5 && token[:5] == "xoxp-" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Execute request using the configured HTTP client (with cookies for xoxc/xoxd)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read content: %w", err)
	}

	return string(body), nil
}
