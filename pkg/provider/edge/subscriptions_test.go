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

// recordingHTTPClient captures the last request's form body and returns a
// canned JSON response.
type recordingHTTPClient struct {
	lastReq  *http.Request
	lastForm url.Values
	body     string
	status   int
}

func (f *recordingHTTPClient) Do(req *http.Request) (*http.Response, error) {
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

func newTestSubscriptionsClient(body string) (*Client, *recordingHTTPClient) {
	f := &recordingHTTPClient{body: body}
	cl := &Client{
		cl:           f,
		token:        "xoxc-test-token",
		teamID:       "T0TEST",
		webclientAPI: "https://example.slack.com/api/",
		tape:         nopTape{},
	}
	return cl, f
}

func TestUnitSubscriptionsThreadMark(t *testing.T) {
	t.Run("posts channel/thread_ts/ts to subscriptions.thread.mark", func(t *testing.T) {
		cl, f := newTestSubscriptionsClient(`{"ok":true}`)

		err := cl.SubscriptionsThreadMark(context.Background(), "C0123456789", "1700000000.000100", "1700000500.000200")
		require.NoError(t, err)

		require.NotNil(t, f.lastReq)
		assert.Equal(t, http.MethodPost, f.lastReq.Method)
		assert.Equal(t, "https://example.slack.com/api/subscriptions.thread.mark", f.lastReq.URL.String())
		assert.Equal(t, "application/x-www-form-urlencoded", f.lastReq.Header.Get("Content-Type"))

		assert.Equal(t, "xoxc-test-token", f.lastForm.Get("token"))
		assert.Equal(t, "C0123456789", f.lastForm.Get("channel"))
		assert.Equal(t, "1700000000.000100", f.lastForm.Get("thread_ts"))
		assert.Equal(t, "1700000500.000200", f.lastForm.Get("ts"))
		assert.Equal(t, "subscriptions-thread-api/markThreadRead", f.lastForm.Get("_x_reason"))
		// Never send the "read" flag: read=false would mark the thread UNREAD.
		assert.False(t, f.lastForm.Has("read"))
	})

	t.Run("returns APIError with endpoint on ok=false", func(t *testing.T) {
		cl, _ := newTestSubscriptionsClient(`{"ok":false,"error":"invalid_arguments","response_metadata":{"messages":["[ERROR] missing required field: ts"]}}`)

		err := cl.SubscriptionsThreadMark(context.Background(), "C0123456789", "1700000000.000100", "")
		require.Error(t, err)

		apiErr, ok := err.(*APIError)
		require.True(t, ok, "expected *APIError, got %T", err)
		assert.Equal(t, "subscriptions.thread.mark", apiErr.Endpoint)
		assert.Equal(t, "invalid_arguments", apiErr.Err)
		assert.Contains(t, err.Error(), "missing required field: ts")
	})

	t.Run("returns error for non-2xx status", func(t *testing.T) {
		cl, f := newTestSubscriptionsClient(`gateway error`)
		f.status = http.StatusBadGateway

		err := cl.SubscriptionsThreadMark(context.Background(), "C0123456789", "1700000000.000100", "1700000500.000200")
		require.Error(t, err)
	})
}
