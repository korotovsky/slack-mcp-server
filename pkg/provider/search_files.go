package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// The SDK's SearchMessage drops the files field while decoding Slack JSON.
// Preserve it at the wire boundary rather than guessing from attachment text.
type SearchMessageWithFiles struct {
	slack.SearchMessage
	Files              []slack.File `json:"files"`
	ThreadTimestamp    string       `json:"thread_ts"`
	FileMetadataStatus string       `json:"-"`
}

type SearchMessagesWithFiles struct {
	Matches          []SearchMessageWithFiles `json:"matches"`
	slack.Paging     `json:"paging"`
	slack.Pagination `json:"pagination"`
	Total            int `json:"total"`
}

func (c *MCPSlackClient) SearchMessagesWithFilesContext(ctx context.Context, query string, params slack.SearchParameters) (*SearchMessagesWithFiles, error) {
	if c.httpClient == nil || c.authProvider == nil || c.teamEndpoint == "" {
		return nil, errors.New("file-aware search client is not initialized")
	}
	values := url.Values{
		"token": {c.authProvider.SlackToken()}, "query": {query},
		"sort": {params.Sort}, "sort_dir": {params.SortDirection},
		"highlight": {strconv.FormatBool(params.Highlight)},
		"count":     {strconv.Itoa(params.Count)}, "page": {strconv.Itoa(params.Page)},
	}
	if params.TeamID != "" {
		values.Set("team_id", params.TeamID)
	}
	endpoint := strings.TrimRight(c.teamEndpoint, "/") + "/api/search.messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		seconds, _ := strconv.Atoi(res.Header.Get("Retry-After"))
		if seconds < 1 {
			seconds = 1
		}
		return nil, &slack.RateLimitedError{RetryAfter: time.Duration(seconds) * time.Second}
	}
	if res.StatusCode != http.StatusOK {
		return nil, slack.StatusCodeError{Code: res.StatusCode, Status: res.Status}
	}
	var response struct {
		slack.SlackResponse
		Messages SearchMessagesWithFiles `json:"messages"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(&response); err != nil {
		return nil, err
	}
	if err := response.SlackResponse.Err(); err != nil {
		return nil, err
	}
	return &response.Messages, nil
}
