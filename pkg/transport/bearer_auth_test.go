package transport

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestUnitUserAgentTransport_RoundTrip_movesOAuthTokenToBearerHeader(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{name: "user token", token: "xoxp-test-token"},
		{name: "bot token", token: "xoxb-test-token"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			request, err := http.NewRequest(http.MethodPost, "https://slack.com/api/auth.test", strings.NewReader("token="+test.token+"&team=T123"))
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			var authorization string
			var body string
			transport := NewUserAgentTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				authorization = req.Header.Get("Authorization")
				requestBody, readErr := io.ReadAll(req.Body)
				if readErr != nil {
					t.Fatalf("read request body: %v", readErr)
				}
				body = string(requestBody)

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}), "test-agent", nil, zap.NewNop())

			// When
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatalf("round trip: %v", err)
			}
			defer response.Body.Close()

			// Then
			if authorization != "Bearer "+test.token {
				t.Fatalf("authorization = %q, want bearer token", authorization)
			}
			form, err := url.ParseQuery(body)
			if err != nil {
				t.Fatalf("parse request body: %v", err)
			}
			if form.Get("token") != "" {
				t.Fatalf("form token = %q, want empty", form.Get("token"))
			}
			if form.Get("team") != "T123" {
				t.Fatalf("team = %q, want T123", form.Get("team"))
			}
		})
	}
}

type closeTrackingBody struct {
	io.Reader
	closed bool
}

func (b *closeTrackingBody) Close() error {
	b.closed = true
	return nil
}

func TestUnitUserAgentTransport_RoundTrip_keepsRewrittenBodyReplayable(t *testing.T) {
	// Given
	request, err := http.NewRequest(http.MethodPost, "https://slack.com/api/conversations.history", strings.NewReader("channel=C123&token=xoxp-test-token"))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	originalBody := &closeTrackingBody{Reader: request.Body}
	request.Body = originalBody

	var sent *http.Request
	transport := NewUserAgentTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		sent = req
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}), "test-agent", nil, zap.NewNop())

	// When
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	defer response.Body.Close()

	// Then
	if !originalBody.closed {
		t.Errorf("original request body was not closed")
	}
	if sent.GetBody == nil {
		t.Fatalf("GetBody is nil, want a replayable body")
	}
	replay, err := sent.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	replayed, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replayed body: %v", err)
	}
	if string(replayed) != "channel=C123" {
		t.Errorf("replayed body = %q, want %q", replayed, "channel=C123")
	}
	if sent.ContentLength != int64(len(replayed)) {
		t.Errorf("ContentLength = %d, replayed body length = %d", sent.ContentLength, len(replayed))
	}
}
