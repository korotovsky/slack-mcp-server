package edge

import (
	"context"
	"runtime/trace"
)

// Lists API types and methods

type ListItem struct {
	ID        string          `json:"id"`
	ListID    string          `json:"list_id"`
	CreatedBy string          `json:"created_by,omitempty"`
	Created   int64           `json:"date_created,omitempty"`
	Updated   int64           `json:"updated_timestamp,omitempty"`
	UpdatedBy string          `json:"updated_by,omitempty"`
	Archived  bool            `json:"archived,omitempty"`
	Fields    []ListItemField `json:"fields,omitempty"`
	ParentID  string          `json:"parent_record_id,omitempty"`
}

type ListItemField struct {
	ColumnID string      `json:"column_id"`
	Key      string      `json:"key,omitempty"`
	Value    interface{} `json:"value,omitempty"`
}

type ListsItemsListRequest struct {
	BaseRequest
	ListID   string `json:"list_id"`
	Limit    int    `json:"limit,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
	Archived bool   `json:"archived,omitempty"`
}

type ListsItemsListResponse struct {
	baseResponse
	Items            []ListItem       `json:"items"`
	ResponseMetadata ResponseMetadata `json:"response_metadata,omitempty"`
}

type ListsItemsInfoRequest struct {
	BaseRequest
	ListID string `json:"list_id"`
	ItemID string `json:"item_id"`
}

type ListsItemsInfoResponse struct {
	baseResponse
	Item ListItem `json:"item"`
}

type ListsItemsCreateRequest struct {
	BaseRequest
	ListID        string          `json:"list_id"`
	InitialFields []ListItemField `json:"initial_fields,omitempty"`
	ParentItemID  string          `json:"parent_item_id,omitempty"`
}

type ListsItemsCreateResponse struct {
	baseResponse
	Item ListItem `json:"item"`
}

type ListsItemsUpdateRequest struct {
	BaseRequest
	ListID string          `json:"list_id"`
	ItemID string          `json:"item_id"`
	Fields []ListItemField `json:"fields,omitempty"`
}

type ListsItemsUpdateResponse struct {
	baseResponse
	Item ListItem `json:"item"`
}

type ListsItemsDeleteRequest struct {
	BaseRequest
	ListID string `json:"list_id"`
	ItemID string `json:"item_id"`
}

type ListsItemsDeleteResponse struct {
	baseResponse
}

func (cl *Client) ListsItemsList(ctx context.Context, listID string, limit int, cursor string, archived bool) (*ListsItemsListResponse, error) {
	ctx, task := trace.NewTask(ctx, "ListsItemsList")
	defer task.End()

	form := ListsItemsListRequest{
		BaseRequest: BaseRequest{Token: cl.token},
		ListID:      listID,
		Limit:       limit,
		Cursor:      cursor,
		Archived:    archived,
	}

	resp, err := cl.PostForm(ctx, "slackLists.items.list", values(form, true))
	if err != nil {
		return nil, err
	}

	r := &ListsItemsListResponse{}
	if err := cl.ParseResponse(r, resp); err != nil {
		return nil, err
	}

	if err := r.validate("slackLists.items.list"); err != nil {
		return nil, err
	}

	return r, nil
}

func (cl *Client) ListsItemsInfo(ctx context.Context, listID, itemID string) (*ListsItemsInfoResponse, error) {
	ctx, task := trace.NewTask(ctx, "ListsItemsInfo")
	defer task.End()

	form := ListsItemsInfoRequest{
		BaseRequest: BaseRequest{Token: cl.token},
		ListID:      listID,
		ItemID:      itemID,
	}

	resp, err := cl.PostForm(ctx, "slackLists.items.info", values(form, true))
	if err != nil {
		return nil, err
	}

	r := &ListsItemsInfoResponse{}
	if err := cl.ParseResponse(r, resp); err != nil {
		return nil, err
	}

	if err := r.validate("slackLists.items.info"); err != nil {
		return nil, err
	}

	return r, nil
}

func (cl *Client) ListsItemsCreate(ctx context.Context, listID string, fields []ListItemField, parentItemID string) (*ListsItemsCreateResponse, error) {
	ctx, task := trace.NewTask(ctx, "ListsItemsCreate")
	defer task.End()

	form := ListsItemsCreateRequest{
		BaseRequest:   BaseRequest{Token: cl.token},
		ListID:        listID,
		InitialFields: fields,
		ParentItemID:  parentItemID,
	}

	resp, err := cl.PostForm(ctx, "slackLists.items.create", values(form, true))
	if err != nil {
		return nil, err
	}

	r := &ListsItemsCreateResponse{}
	if err := cl.ParseResponse(r, resp); err != nil {
		return nil, err
	}

	if err := r.validate("slackLists.items.create"); err != nil {
		return nil, err
	}

	return r, nil
}

func (cl *Client) ListsItemsUpdate(ctx context.Context, listID, itemID string, fields []ListItemField) (*ListsItemsUpdateResponse, error) {
	ctx, task := trace.NewTask(ctx, "ListsItemsUpdate")
	defer task.End()

	form := ListsItemsUpdateRequest{
		BaseRequest: BaseRequest{Token: cl.token},
		ListID:      listID,
		ItemID:      itemID,
		Fields:      fields,
	}

	resp, err := cl.PostForm(ctx, "slackLists.items.update", values(form, true))
	if err != nil {
		return nil, err
	}

	r := &ListsItemsUpdateResponse{}
	if err := cl.ParseResponse(r, resp); err != nil {
		return nil, err
	}

	if err := r.validate("slackLists.items.update"); err != nil {
		return nil, err
	}

	return r, nil
}

func (cl *Client) ListsItemsDelete(ctx context.Context, listID, itemID string) (*ListsItemsDeleteResponse, error) {
	ctx, task := trace.NewTask(ctx, "ListsItemsDelete")
	defer task.End()

	form := ListsItemsDeleteRequest{
		BaseRequest: BaseRequest{Token: cl.token},
		ListID:      listID,
		ItemID:      itemID,
	}

	resp, err := cl.PostForm(ctx, "slackLists.items.delete", values(form, true))
	if err != nil {
		return nil, err
	}

	r := &ListsItemsDeleteResponse{}
	if err := cl.ParseResponse(r, resp); err != nil {
		return nil, err
	}

	if err := r.validate("slackLists.items.delete"); err != nil {
		return nil, err
	}

	return r, nil
}
