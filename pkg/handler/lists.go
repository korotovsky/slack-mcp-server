package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

type ListItemCSV struct {
	ID        string `json:"id" csv:"ID"`
	ListID    string `json:"list_id" csv:"ListID"`
	CreatedBy string `json:"created_by" csv:"CreatedBy"`
	UpdatedBy string `json:"updated_by" csv:"UpdatedBy"`
	Archived  bool   `json:"archived" csv:"Archived"`
	ParentID  string `json:"parent_id" csv:"ParentID"`
	Fields    string `json:"fields" csv:"Fields"`
	Cursor    string `json:"cursor" csv:"Cursor"`
}

type ListsHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
}

func NewListsHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *ListsHandler {
	return &ListsHandler{
		apiProvider: apiProvider,
		logger:      logger,
	}
}

func (lh *ListsHandler) ListsItemsListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	lh.logger.Debug("ListsItemsListHandler called")

	if ready, err := lh.apiProvider.IsReady(); !ready {
		lh.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	listID := request.GetString("list_id", "")
	if listID == "" {
		lh.logger.Error("list_id is required")
		return nil, fmt.Errorf("list_id is required")
	}

	limit := request.GetInt("limit", 100)
	cursor := request.GetString("cursor", "")
	archived := request.GetBool("archived", false)

	lh.logger.Debug("Request parameters",
		zap.String("list_id", listID),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
		zap.Bool("archived", archived),
	)

	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		lh.logger.Warn("Limit exceeds maximum, capping to 1000", zap.Int("requested", limit))
		limit = 1000
	}

	resp, err := lh.apiProvider.Slack().ListsItemsList(ctx, listID, limit, cursor, archived)
	if err != nil {
		lh.logger.Error("Failed to list items", zap.Error(err))
		return nil, err
	}

	var itemList []ListItemCSV
	for _, item := range resp.Items {
		fieldsJSON, err := json.Marshal(item.Fields)
		if err != nil {
			fieldsJSON = []byte("[]")
		}

		itemList = append(itemList, ListItemCSV{
			ID:        item.ID,
			ListID:    item.ListID,
			CreatedBy: item.CreatedBy,
			UpdatedBy: item.UpdatedBy,
			Archived:  item.Archived,
			ParentID:  item.ParentID,
			Fields:    string(fieldsJSON),
		})
	}

	if len(itemList) > 0 && resp.ResponseMetadata.NextCursor != "" {
		itemList[len(itemList)-1].Cursor = resp.ResponseMetadata.NextCursor
		lh.logger.Debug("Added cursor to last item", zap.String("cursor", resp.ResponseMetadata.NextCursor))
	}

	csvBytes, err := gocsv.MarshalBytes(&itemList)
	if err != nil {
		lh.logger.Error("Failed to marshal items to CSV", zap.Error(err))
		return nil, err
	}

	return mcp.NewToolResultText(string(csvBytes)), nil
}

func (lh *ListsHandler) ListsItemsInfoHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	lh.logger.Debug("ListsItemsInfoHandler called")

	if ready, err := lh.apiProvider.IsReady(); !ready {
		lh.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	listID := request.GetString("list_id", "")
	if listID == "" {
		lh.logger.Error("list_id is required")
		return nil, fmt.Errorf("list_id is required")
	}

	itemID := request.GetString("item_id", "")
	if itemID == "" {
		lh.logger.Error("item_id is required")
		return nil, fmt.Errorf("item_id is required")
	}

	lh.logger.Debug("Request parameters",
		zap.String("list_id", listID),
		zap.String("item_id", itemID),
	)

	resp, err := lh.apiProvider.Slack().ListsItemsInfo(ctx, listID, itemID)
	if err != nil {
		lh.logger.Error("Failed to get item info", zap.Error(err))
		return nil, err
	}

	fieldsJSON, err := json.Marshal(resp.Item.Fields)
	if err != nil {
		fieldsJSON = []byte("[]")
	}

	itemList := []ListItemCSV{{
		ID:        resp.Item.ID,
		ListID:    resp.Item.ListID,
		CreatedBy: resp.Item.CreatedBy,
		UpdatedBy: resp.Item.UpdatedBy,
		Archived:  resp.Item.Archived,
		ParentID:  resp.Item.ParentID,
		Fields:    string(fieldsJSON),
	}}

	csvBytes, err := gocsv.MarshalBytes(&itemList)
	if err != nil {
		lh.logger.Error("Failed to marshal item to CSV", zap.Error(err))
		return nil, err
	}

	return mcp.NewToolResultText(string(csvBytes)), nil
}

func (lh *ListsHandler) ListsItemsCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	lh.logger.Debug("ListsItemsCreateHandler called")

	if ready, err := lh.apiProvider.IsReady(); !ready {
		lh.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	listID := request.GetString("list_id", "")
	if listID == "" {
		lh.logger.Error("list_id is required")
		return nil, fmt.Errorf("list_id is required")
	}

	fieldsJSON := request.GetString("fields", "")
	parentItemID := request.GetString("parent_item_id", "")

	lh.logger.Debug("Request parameters",
		zap.String("list_id", listID),
		zap.String("fields", fieldsJSON),
		zap.String("parent_item_id", parentItemID),
	)

	var fields []edge.ListItemField
	if fieldsJSON != "" {
		if err := json.Unmarshal([]byte(fieldsJSON), &fields); err != nil {
			lh.logger.Error("Failed to parse fields JSON", zap.Error(err))
			return nil, fmt.Errorf("invalid fields JSON: %v", err)
		}
	}

	resp, err := lh.apiProvider.Slack().ListsItemsCreate(ctx, listID, fields, parentItemID)
	if err != nil {
		lh.logger.Error("Failed to create item", zap.Error(err))
		return nil, err
	}

	respFieldsJSON, err := json.Marshal(resp.Item.Fields)
	if err != nil {
		respFieldsJSON = []byte("[]")
	}

	itemList := []ListItemCSV{{
		ID:        resp.Item.ID,
		ListID:    resp.Item.ListID,
		CreatedBy: resp.Item.CreatedBy,
		UpdatedBy: resp.Item.UpdatedBy,
		Archived:  resp.Item.Archived,
		ParentID:  resp.Item.ParentID,
		Fields:    string(respFieldsJSON),
	}}

	csvBytes, err := gocsv.MarshalBytes(&itemList)
	if err != nil {
		lh.logger.Error("Failed to marshal item to CSV", zap.Error(err))
		return nil, err
	}

	return mcp.NewToolResultText(string(csvBytes)), nil
}

func (lh *ListsHandler) ListsItemsUpdateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	lh.logger.Debug("ListsItemsUpdateHandler called")

	if ready, err := lh.apiProvider.IsReady(); !ready {
		lh.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	listID := request.GetString("list_id", "")
	if listID == "" {
		lh.logger.Error("list_id is required")
		return nil, fmt.Errorf("list_id is required")
	}

	itemID := request.GetString("item_id", "")
	if itemID == "" {
		lh.logger.Error("item_id is required")
		return nil, fmt.Errorf("item_id is required")
	}

	fieldsJSON := request.GetString("fields", "")

	lh.logger.Debug("Request parameters",
		zap.String("list_id", listID),
		zap.String("item_id", itemID),
		zap.String("fields", fieldsJSON),
	)

	var fields []edge.ListItemField
	if fieldsJSON != "" {
		if err := json.Unmarshal([]byte(fieldsJSON), &fields); err != nil {
			lh.logger.Error("Failed to parse fields JSON", zap.Error(err))
			return nil, fmt.Errorf("invalid fields JSON: %v", err)
		}
	}

	resp, err := lh.apiProvider.Slack().ListsItemsUpdate(ctx, listID, itemID, fields)
	if err != nil {
		lh.logger.Error("Failed to update item", zap.Error(err))
		return nil, err
	}

	respFieldsJSON, err := json.Marshal(resp.Item.Fields)
	if err != nil {
		respFieldsJSON = []byte("[]")
	}

	itemList := []ListItemCSV{{
		ID:        resp.Item.ID,
		ListID:    resp.Item.ListID,
		CreatedBy: resp.Item.CreatedBy,
		UpdatedBy: resp.Item.UpdatedBy,
		Archived:  resp.Item.Archived,
		ParentID:  resp.Item.ParentID,
		Fields:    string(respFieldsJSON),
	}}

	csvBytes, err := gocsv.MarshalBytes(&itemList)
	if err != nil {
		lh.logger.Error("Failed to marshal item to CSV", zap.Error(err))
		return nil, err
	}

	return mcp.NewToolResultText(string(csvBytes)), nil
}

func (lh *ListsHandler) ListsItemsDeleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	lh.logger.Debug("ListsItemsDeleteHandler called")

	if ready, err := lh.apiProvider.IsReady(); !ready {
		lh.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	listID := request.GetString("list_id", "")
	if listID == "" {
		lh.logger.Error("list_id is required")
		return nil, fmt.Errorf("list_id is required")
	}

	itemID := request.GetString("item_id", "")
	if itemID == "" {
		lh.logger.Error("item_id is required")
		return nil, fmt.Errorf("item_id is required")
	}

	lh.logger.Debug("Request parameters",
		zap.String("list_id", listID),
		zap.String("item_id", itemID),
	)

	_, err := lh.apiProvider.Slack().ListsItemsDelete(ctx, listID, itemID)
	if err != nil {
		lh.logger.Error("Failed to delete item", zap.Error(err))
		return nil, err
	}

	return mcp.NewToolResultText("Item deleted successfully"), nil
}
