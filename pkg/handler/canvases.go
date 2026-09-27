package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

const (
	defaultCanvasesListLimit = 100
	maxCanvasesListLimit     = 1000
)

type CanvasSummary struct {
	FileID    string `json:"file_id"`
	Title     string `json:"title"`
	Permalink string `json:"permalink,omitempty"`
	UserID    string `json:"user_id,omitempty"`
}

type CanvasesListResult struct {
	ChannelID string          `json:"channel_id"`
	Page      int             `json:"page"`
	Pages     int             `json:"pages"`
	Total     int             `json:"total"`
	HasMore   bool            `json:"has_more"`
	NextPage  int             `json:"next_page,omitempty"`
	Canvases  []CanvasSummary `json:"canvases"`
}

func (ch *ConversationsHandler) CanvasesListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("CanvasesListHandler called", zap.Any("params", request.Params))

	if ready, err := ch.apiProvider.IsReady(); !ready {
		return nil, err
	}

	channel := request.GetString("channel_id", "")
	if channel == "" {
		return nil, fmt.Errorf("channel_id is required")
	}
	channelID, err := ch.resolveChannelID(ctx, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve channel: %w", err)
	}

	limit := request.GetInt("limit", defaultCanvasesListLimit)
	if limit < 1 || limit > maxCanvasesListLimit {
		return nil, fmt.Errorf("limit must be between 1 and %d", maxCanvasesListLimit)
	}
	page := request.GetInt("page", 1)
	if page < 1 {
		return nil, fmt.Errorf("page must be at least 1")
	}

	files, paging, err := ch.apiProvider.Slack().GetFilesContext(ctx, slack.GetFilesParameters{
		Channel: channelID,
		Types:   "canvas",
		Count:   limit,
		Page:    page,
	})
	if err != nil {
		ch.logger.Error("Slack GetFilesContext failed", zap.String("channel_id", channelID), zap.Error(err))
		return nil, fmt.Errorf("failed to list canvases for channel %s: %w", channelID, err)
	}

	result := CanvasesListResult{
		ChannelID: channelID,
		Page:      page,
		Canvases:  make([]CanvasSummary, 0, len(files)),
	}
	if paging != nil {
		result.Pages = paging.Pages
		result.Total = paging.Total
		result.HasMore = page < paging.Pages
	}
	if result.HasMore {
		result.NextPage = page + 1
	}

	for _, file := range files {
		// Slack's types=canvas filter should only return canvases, but retaining
		// this check prevents unrelated files from being surfaced if the API
		// response is broader than requested.
		if file.Filetype != "canvas" {
			continue
		}

		title := file.Title
		if title == "" {
			title = file.Name
		}
		result.Canvases = append(result.Canvases, CanvasSummary{
			FileID:    file.ID,
			Title:     title,
			Permalink: file.Permalink,
			UserID:    file.User,
		})
	}

	content, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to encode canvas list: %w", err)
	}
	return mcp.NewToolResultText(string(content)), nil
}
