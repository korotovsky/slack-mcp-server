package edge

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubHTTPClient records the last request's form body and returns a canned response.
type stubHTTPClient struct {
	lastReq  *http.Request
	lastForm url.Values
	body     string
	status   int
}

func (f *stubHTTPClient) Do(req *http.Request) (*http.Response, error) {
	f.lastReq = req
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if f.lastForm, err = url.ParseQuery(string(raw)); err != nil {
		return nil, err
	}
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Header:     make(http.Header),
	}, nil
}

func newTestThreadsClient(body string) (*Client, *stubHTTPClient) {
	f := &stubHTTPClient{body: body}
	cl := &Client{
		cl:           f,
		token:        "xoxc-test-token",
		teamID:       "T0TEST",
		webclientAPI: "https://example.slack.com/api/",
		tape:         nopTape{},
	}
	return cl, f
}

// threadsViewFixture mirrors the shape observed from the live endpoint.
const threadsViewFixture = `{
  "ok": true,
  "total_unread_replies": 57,
  "new_threads_count": 10,
  "has_more": true,
  "max_ts": "1787062266.000000",
  "threads": [
    {
      "root_msg": {
        "user": "U0ROOT", "type": "message", "ts": "1786021681.497329", "thread_ts": "1786021681.497329",
        "channel": "C0CHAN", "text": "root text", "reply_count": 3, "reply_users_count": 2,
        "latest_reply": "1787049849.098909", "last_read": "1787048594.264689", "subscribed": true,
        "reply_users": ["U0A", "U0B"]
      },
      "latest_replies": [],
      "unread_replies": [
        {"user": "U0A", "type": "message", "ts": "1787049849.098909", "thread_ts": "1786021681.497329", "text": "unread reply"}
      ]
    },
    {
      "root_msg": {
        "user": "U0ROOT2", "type": "message", "ts": "1785750757.443489", "thread_ts": "1785750757.443489",
        "channel": "C0OTHER", "text": "read thread", "reply_count": 7,
        "latest_reply": "1787049701.503059", "last_read": "1787049702.000000", "subscribed": true
      },
      "latest_replies": [
        {"user": "U0C", "type": "message", "ts": "1787049701.503059", "thread_ts": "1785750757.443489", "text": "latest reply"}
      ]
    }
  ]
}`

func TestUnitSubscriptionsThreadGetView(t *testing.T) {
	t.Run("posts limit/current_ts/channel_id and parses the view", func(t *testing.T) {
		cl, f := newTestThreadsClient(threadsViewFixture)

		resp, err := cl.SubscriptionsThreadGetView(context.Background(), "1787050000.000000", 10, "C0CHAN")
		require.NoError(t, err)

		require.NotNil(t, f.lastReq)
		assert.Equal(t, http.MethodPost, f.lastReq.Method)
		assert.Equal(t, "https://example.slack.com/api/subscriptions.thread.getView", f.lastReq.URL.String())
		assert.Equal(t, "xoxc-test-token", f.lastForm.Get("token"))
		assert.Equal(t, "10", f.lastForm.Get("limit"))
		assert.Equal(t, "1787050000.000000", f.lastForm.Get("current_ts"))
		assert.Equal(t, "C0CHAN", f.lastForm.Get("channel_id"))
		assert.Equal(t, "fetch-threads-view", f.lastForm.Get("_x_reason"))

		assert.True(t, resp.HasMore)
		assert.Equal(t, 57, resp.TotalUnreadReplies)
		assert.Equal(t, 10, resp.NewThreadsCount)
		assert.Equal(t, "1787062266.000000", resp.MaxTs)
		require.Len(t, resp.Threads, 2)

		first := resp.Threads[0]
		assert.Equal(t, "C0CHAN", first.RootMsg.Channel)
		assert.Equal(t, "1786021681.497329", first.RootMsg.Timestamp)
		assert.Equal(t, "1786021681.497329", first.RootMsg.ThreadTimestamp)
		assert.Equal(t, 3, first.RootMsg.ReplyCount)
		assert.Equal(t, "1787049849.098909", first.RootMsg.LatestReply)
		assert.Equal(t, "1787048594.264689", first.RootMsg.LastRead)
		assert.True(t, first.RootMsg.Subscribed)
		require.Len(t, first.UnreadReplies, 1)
		assert.Equal(t, "unread reply", first.UnreadReplies[0].Text)
		assert.Empty(t, first.LatestReplies)

		second := resp.Threads[1]
		assert.Empty(t, second.UnreadReplies)
		require.Len(t, second.LatestReplies, 1)
		assert.Equal(t, "latest reply", second.LatestReplies[0].Text)
	})

	t.Run("omits empty current_ts and channel_id", func(t *testing.T) {
		cl, f := newTestThreadsClient(`{"ok":true,"threads":[],"has_more":false}`)

		resp, err := cl.SubscriptionsThreadGetView(context.Background(), "", 10, "")
		require.NoError(t, err)
		assert.False(t, f.lastForm.Has("current_ts"))
		assert.False(t, f.lastForm.Has("channel_id"))
		assert.Empty(t, resp.Threads)
		assert.False(t, resp.HasMore)
	})

	t.Run("returns APIError on ok=false", func(t *testing.T) {
		cl, _ := newTestThreadsClient(`{"ok":false,"error":"not_allowed_token_type"}`)

		_, err := cl.SubscriptionsThreadGetView(context.Background(), "", 10, "")
		require.Error(t, err)
		apiErr, ok := err.(*APIError)
		require.True(t, ok, "expected *APIError, got %T", err)
		assert.Equal(t, "subscriptions.thread.getView", apiErr.Endpoint)
		assert.Equal(t, "not_allowed_token_type", apiErr.Err)
	})

	t.Run("returns error for non-2xx status", func(t *testing.T) {
		cl, f := newTestThreadsClient(`gateway error`)
		f.status = http.StatusBadGateway
		_, err := cl.SubscriptionsThreadGetView(context.Background(), "", 10, "")
		require.Error(t, err)
	})
}
