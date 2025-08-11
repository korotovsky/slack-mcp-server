package handler

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

type Canvas struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ChannelID string `json:"channelId"`
	OwnerID   string `json:"ownerId"`
	Permalink string `json:"permalink"`
	Updated   int64  `json:"updated"`
	Content   string `json:"content,omitempty"`
}

type CanvasesHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
}

func NewCanvasesHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *CanvasesHandler {
	return &CanvasesHandler{apiProvider: apiProvider, logger: logger}
}

func (h *CanvasesHandler) CanvasesHistoryHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasesHistoryHandler called", zap.Any("params", req.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	canvasID := strings.TrimSpace(req.GetString("canvas_id", ""))

	var rows []Canvas
	if canvasID == "" {
		params := slack.NewGetFilesParameters()
		params.Types = "spaces,posts"
		params.Count = 100
		files, _, err := h.apiProvider.Slack().GetFilesContext(ctx, params)
		if err != nil {
			h.logger.Error("GetFilesContext failed", zap.Error(err))
			return nil, err
		}
		rows = make([]Canvas, 0, len(files))
		for _, f := range files {
			ch := ""
			if len(f.Channels) > 0 {
				ch = f.Channels[0]
			}
			rows = append(rows, Canvas{
				ID:        f.ID,
				Title:     f.Title,
				ChannelID: ch,
				OwnerID:   f.User,
				Permalink: f.Permalink,
				Updated:   int64(f.Timestamp),
			})
		}
	} else {
		f, _, _, err := h.apiProvider.Slack().GetFileInfoContext(ctx, canvasID, 0, 1)
		if err != nil {
			h.logger.Error("GetFileInfoContext failed", zap.String("canvas_id", canvasID), zap.Error(err))
			return nil, err
		}
		var content string
		dl := f.URLPrivateDownload
		if dl == "" {
			dl = f.URLPrivate
		}
		if dl != "" {
			var buf bytes.Buffer
			if err := h.apiProvider.Slack().GetFileContext(ctx, dl, &buf); err == nil {
				content = buf.String()
			}
		}
		ch := ""
		if len(f.Channels) > 0 {
			ch = f.Channels[0]
		}
		rows = []Canvas{{
			ID:        f.ID,
			Title:     f.Title,
			ChannelID: ch,
			OwnerID:   f.User,
			Permalink: f.Permalink,
			Updated:   int64(f.Timestamp),
			Content:   content,
		}}
	}

	csvBytes, err := gocsv.MarshalBytes(&rows)
	if err != nil {
		h.logger.Error("Failed to marshal canvases to CSV", zap.Error(err))
		return nil, err
	}

	return mcp.NewToolResultText(string(csvBytes)), nil
}

func (h *CanvasesHandler) CanvasesUpsertHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasesUpsertHandler called", zap.Any("params", req.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	id := strings.TrimSpace(req.GetString("canvas_id", ""))
	title := strings.TrimSpace(req.GetString("title", ""))
	channelID := strings.TrimSpace(req.GetString("channel_id", ""))
	content := req.GetString("content_markdown", "")
	if content == "" {
		return nil, fmt.Errorf("content_markdown is required")
	}
	// Create or Edit via Canvas API
	if id == "" {
		canvasID, err := h.apiProvider.Slack().CreateCanvas(title, slack.DocumentContent{Type: "markdown", Markdown: content})
		if err != nil {
			h.logger.Error("CreateCanvas failed", zap.Error(err))
			return nil, err
		}
		id = canvasID
		if channelID != "" {
			// Best-effort: grant read access to the channel
			_ = h.apiProvider.Slack().SetCanvasAccess(slack.SetCanvasAccessParams{
				CanvasID:    id,
				AccessLevel: "read",
				ChannelIDs:  []string{channelID},
			})
		}
	} else {
		// Try to update first available section; if none, attempt a replace without section ID
		changes := []slack.CanvasChange{}
		if secs, err := h.apiProvider.Slack().LookupCanvasSections(slack.LookupCanvasSectionsParams{CanvasID: id}); err == nil && len(secs) > 0 {
			changes = append(changes, slack.CanvasChange{
				Operation:       "update",
				SectionID:       secs[0].ID,
				DocumentContent: slack.DocumentContent{Type: "markdown", Markdown: content},
			})
		} else {
			changes = append(changes, slack.CanvasChange{
				Operation:       "update",
				DocumentContent: slack.DocumentContent{Type: "markdown", Markdown: content},
			})
		}
		if err := h.apiProvider.Slack().EditCanvas(slack.EditCanvasParams{CanvasID: id, Changes: changes}); err != nil {
			h.logger.Error("EditCanvas failed", zap.Error(err))
			return nil, err
		}
	}

	// Fetch details for CSV via files.info
	f, _, _, err := h.apiProvider.Slack().GetFileInfoContext(ctx, id, 0, 1)
	if err != nil {
		h.logger.Warn("GetFileInfoContext failed; returning minimal CSV", zap.String("canvas_id", id), zap.Error(err))
		rows := []Canvas{{ID: id, Title: title}}
		csvBytes, _ := gocsv.MarshalBytes(&rows)
		return mcp.NewToolResultText(string(csvBytes)), nil
	}
	ch := ""
	if len(f.Channels) > 0 {
		ch = f.Channels[0]
	}
	rows := []Canvas{{
		ID:        f.ID,
		Title:     f.Title,
		ChannelID: ch,
		OwnerID:   f.User,
		Permalink: f.Permalink,
		Updated:   int64(f.Timestamp),
	}}
	csvBytes, err := gocsv.MarshalBytes(&rows)
	if err != nil {
		h.logger.Error("Failed to marshal canvas to CSV", zap.Error(err))
		return nil, err
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

func (h *CanvasesHandler) CanvasesRemoveHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasesRemoveHandler called", zap.Any("params", req.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	id := strings.TrimSpace(req.GetString("canvas_id", ""))
	if id == "" {
		return nil, fmt.Errorf("canvas_id is required")
	}
	if err := h.apiProvider.Slack().DeleteCanvas(id); err != nil {
		h.logger.Error("DeleteCanvas failed", zap.String("canvas_id", id), zap.Error(err))
		return nil, err
	}
	return mcp.NewToolResultText("status,canvas_id\nok," + id + "\n"), nil
}
