package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/korotovsky/slack-mcp-server/pkg/transport"
	"github.com/rusq/slackdump/v3/auth"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestUnitSearchPreservesWireFileMetadataAndPaging(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/search.messages", r.URL.Path)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "xoxc-test-only", r.Form.Get("token"))
		require.Equal(t, "report", r.Form.Get("query"))
		require.Equal(t, "2", r.Form.Get("page"))
		cookie, err := r.Cookie("d")
		require.NoError(t, err)
		require.Equal(t, "xoxd-test-only", cookie.Value)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"messages":{"total":3,"pagination":{"page":2,"page_count":3},"matches":[{"ts":"1234567891.123456","channel":{"id":"C123"},"files":[{"id":"F1","name":"report.pdf"}]},{"ts":"1234567892.123456","files":[]},{"ts":"1234567893.123456"}]}}`)
	}))
	defer server.Close()
	credentials, err := auth.NewValueCookiesAuth("xoxc-test-only", []*http.Cookie{{Name: "d", Value: "xoxd-test-only"}})
	require.NoError(t, err)
	client := &MCPSlackClient{teamEndpoint: server.URL + "/", authProvider: credentials,
		httpClient: &http.Client{Transport: transport.NewUserAgentTransport(http.DefaultTransport, "test-only", credentials.Cookies(), zap.NewNop())}}
	result, err := client.SearchMessagesWithFilesContext(context.Background(), "report", slack.SearchParameters{Page: 2, Count: 3, Sort: "timestamp", SortDirection: "desc"})
	require.NoError(t, err)
	require.Equal(t, 2, result.Pagination.Page)
	require.Equal(t, 3, result.Pagination.PageCount)
	require.Equal(t, "F1", result.Matches[0].Files[0].ID)
	require.NotNil(t, result.Matches[1].Files, "explicit zero differs from omitted metadata")
	require.Nil(t, result.Matches[2].Files)
}

func TestUnitSearchPreservesSlackErrors(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusForbidden, http.StatusOK} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(code)
				fmt.Fprint(w, `{"ok":false,"error":"missing_scope"}`)
			}))
			defer server.Close()
			credentials, err := auth.NewValueAuth("test-only", "")
			require.NoError(t, err)
			client := &MCPSlackClient{teamEndpoint: server.URL, authProvider: credentials, httpClient: server.Client()}
			_, err = client.SearchMessagesWithFilesContext(context.Background(), "report", slack.NewSearchParameters())
			require.Error(t, err)
			if code == http.StatusTooManyRequests {
				var limited *slack.RateLimitedError
				require.True(t, errors.As(err, &limited))
				require.Equal(t, 7*time.Second, limited.RetryAfter)
			} else if code == http.StatusOK {
				require.ErrorContains(t, err, "missing_scope")
			} else {
				var status slack.StatusCodeError
				require.True(t, errors.As(err, &status))
				require.Equal(t, code, status.Code)
			}
		})
	}
}

func TestUnitFileUploadUsesStreamingClientAndHonoursCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(5 * time.Millisecond)
		fmt.Fprint(w, "OK")
	}))
	defer server.Close()
	client := &MCPSlackClient{
		slackClient:    slack.New("test-only", slack.OptionHTTPClient(&http.Client{Timeout: time.Nanosecond})),
		downloadClient: slack.New("test-only", slack.OptionHTTPClient(server.Client())),
	}
	err := client.UploadToURL(context.Background(), slack.UploadToURLParameters{UploadURL: server.URL, Filename: "file.txt", Reader: strings.NewReader("payload")})
	require.NoError(t, err, "streaming uploads must not use the ordinary API timeout")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = client.UploadToURL(ctx, slack.UploadToURLParameters{UploadURL: server.URL, Filename: "file.txt", Reader: strings.NewReader("payload")})
	require.Error(t, err)
}
