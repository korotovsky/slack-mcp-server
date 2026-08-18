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

// fakeHTTPClient records the last request and returns a canned JSON response.
type fakeHTTPClient struct {
	lastReq  *http.Request
	lastForm url.Values
	body     string
	status   int
}

func (f *fakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	f.lastReq = req
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	f.lastForm, err = url.ParseQuery(string(raw))
	if err != nil {
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

func newTestSavedClient(body string) (*Client, *fakeHTTPClient) {
	f := &fakeHTTPClient{body: body}
	cl := &Client{
		cl:           f,
		token:        "xoxc-test-token",
		teamID:       "T0TEST",
		webclientAPI: "https://example.slack.com/api/",
		tape:         nopTape{},
	}
	return cl, f
}

func TestUnitSavedAdd(t *testing.T) {
	t.Run("posts item_type/item_id/ts to saved.add", func(t *testing.T) {
		cl, f := newTestSavedClient(`{"ok":true}`)

		err := cl.SavedAdd(context.Background(), "message", "C0123456789", "1700000000.123456")
		require.NoError(t, err)

		require.NotNil(t, f.lastReq)
		assert.Equal(t, http.MethodPost, f.lastReq.Method)
		assert.Equal(t, "https://example.slack.com/api/saved.add", f.lastReq.URL.String())
		assert.Equal(t, "application/x-www-form-urlencoded", f.lastReq.Header.Get("Content-Type"))

		assert.Equal(t, "xoxc-test-token", f.lastForm.Get("token"))
		assert.Equal(t, "message", f.lastForm.Get("item_type"))
		assert.Equal(t, "C0123456789", f.lastForm.Get("item_id"))
		assert.Equal(t, "1700000000.123456", f.lastForm.Get("ts"))
		assert.Equal(t, "saved-api/addSavedMessage", f.lastForm.Get("_x_reason"))
		assert.Equal(t, "online", f.lastForm.Get("_x_mode"))
		assert.Equal(t, "client", f.lastForm.Get("_x_app_name"))
		// saved.add never sends a due date; that is applied via saved.update.
		assert.False(t, f.lastForm.Has("date_due"))
	})

	t.Run("returns APIError with endpoint and metadata on ok=false", func(t *testing.T) {
		cl, _ := newTestSavedClient(`{"ok":false,"error":"invalid_arguments","response_metadata":{"messages":["[ERROR] missing required field: item_type"]}}`)

		err := cl.SavedAdd(context.Background(), "message", "C0123456789", "1700000000.123456")
		require.Error(t, err)

		apiErr, ok := err.(*APIError)
		require.True(t, ok, "expected *APIError, got %T", err)
		assert.Equal(t, "saved.add", apiErr.Endpoint)
		assert.Equal(t, "invalid_arguments", apiErr.Err)
		assert.Contains(t, err.Error(), "missing required field: item_type")
	})

	t.Run("returns error for non-2xx status", func(t *testing.T) {
		cl, f := newTestSavedClient(`server error`)
		f.status = http.StatusInternalServerError

		err := cl.SavedAdd(context.Background(), "message", "C0123456789", "1700000000.123456")
		require.Error(t, err)
	})
}

func TestUnitSavedDelete(t *testing.T) {
	t.Run("posts item_type/item_id/ts to saved.delete", func(t *testing.T) {
		cl, f := newTestSavedClient(`{"ok":true}`)

		err := cl.SavedDelete(context.Background(), "message", "D0123456789", "1700000000.654321")
		require.NoError(t, err)

		require.NotNil(t, f.lastReq)
		assert.Equal(t, http.MethodPost, f.lastReq.Method)
		assert.Equal(t, "https://example.slack.com/api/saved.delete", f.lastReq.URL.String())

		assert.Equal(t, "xoxc-test-token", f.lastForm.Get("token"))
		assert.Equal(t, "message", f.lastForm.Get("item_type"))
		assert.Equal(t, "D0123456789", f.lastForm.Get("item_id"))
		assert.Equal(t, "1700000000.654321", f.lastForm.Get("ts"))
		assert.Equal(t, "saved-api/deleteSavedMessage", f.lastForm.Get("_x_reason"))
	})

	t.Run("returns APIError on ok=false", func(t *testing.T) {
		cl, _ := newTestSavedClient(`{"ok":false,"error":"not_allowed_token_type"}`)

		err := cl.SavedDelete(context.Background(), "message", "D0123456789", "1700000000.654321")
		require.Error(t, err)

		apiErr, ok := err.(*APIError)
		require.True(t, ok, "expected *APIError, got %T", err)
		assert.Equal(t, "saved.delete", apiErr.Endpoint)
		assert.Equal(t, "not_allowed_token_type", apiErr.Err)
	})
}
