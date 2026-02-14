package edge

import (
	"context"
	"runtime/trace"

	"github.com/slack-go/slack"
)

// users/search API (edge cache endpoint)

// UsersSearchRequest is the request body for the users/search endpoint.
type UsersSearchRequest struct {
	Query string `json:"query"`
	Count int    `json:"count,omitempty"`
}

// UsersSearchResponse is the response from the users/search endpoint.
type UsersSearchResponse struct {
	Ok      bool         `json:"ok"`
	Error   string       `json:"error,omitempty"`
	Results []slack.User `json:"results,omitempty"`
}

// UsersSearch searches for users by name, email, or display name using the edge API.
// This only works with browser session tokens (xoxc/xoxd), not OAuth tokens (xoxp/xoxb).
func (cl *Client) UsersSearch(ctx context.Context, query string, count int) ([]slack.User, error) {
	ctx, task := trace.NewTask(ctx, "UsersSearch")
	defer task.End()

	if count <= 0 {
		count = 10
	}

	req := &usersSearchForm{
		Query: query,
		Count: count,
	}

	var resp UsersSearchResponse
	if err := cl.callEdgeAPI(ctx, &resp, "users/search", req); err != nil {
		return nil, err
	}

	if !resp.Ok {
		return nil, &APIError{Err: resp.Error, Endpoint: "users/search"}
	}

	return resp.Results, nil
}

// usersSearchForm implements PostRequest for the users/search endpoint.
type usersSearchForm struct {
	BaseRequest
	Query string `json:"query"`
	Count int    `json:"count,omitempty"`
}
