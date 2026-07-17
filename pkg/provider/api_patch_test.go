package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rusq/slackdump/v3/auth"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockSlackClient implements just enough of SlackAPI for PatchUser tests.
type mockSlackClient struct {
	SlackAPI // embed interface to satisfy all methods; only override what we need

	usersInfoResult *[]slack.User
	usersInfoErr    error
}

func (m *mockSlackClient) GetUsersInfo(users ...string) (*[]slack.User, error) {
	return m.usersInfoResult, m.usersInfoErr
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestApiProvider(client SlackAPI, snapshot *UsersCache) *ApiProvider {
	ap := &ApiProvider{
		client: client,
		logger: zap.NewNop(),
	}
	ap.usersSnapshot.Store(snapshot)
	return ap
}

func TestUnitAssistantSearchContextRequestSerializesSearchFields(t *testing.T) {
	body, err := json.Marshal(AssistantSearchContextRequest{
		Query:               "launch plan in:general from:<@U123ABC> after:2026-01-01",
		Cursor:              "next-page",
		IncludeDeletedUsers: true,
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"query":"launch plan in:general from:<@U123ABC> after:2026-01-01",
		"cursor":"next-page",
		"include_deleted_users":true
	}`, string(body))
}

func TestUnitAssistantSearchContextReturnsSlackRateLimitedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	authProvider, err := auth.NewValueAuth("xoxp-test", "")
	require.NoError(t, err)
	client := &MCPSlackClient{
		httpClient:   server.Client(),
		authProvider: authProvider,
		teamEndpoint: server.URL + "/",
	}

	_, err = client.AssistantSearchContext(context.Background(), AssistantSearchContextRequest{Query: "test"})
	require.Error(t, err)

	var rateLimited *slack.RateLimitedError
	require.ErrorAs(t, err, &rateLimited)
	assert.Equal(t, 7*time.Second, rateLimited.RetryAfter)
}

func TestUnitAssistantSearchContextDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"results":{"messages":[]}}`))
	}))
	t.Cleanup(redirectTarget.Close)

	redirectSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+"/capture", http.StatusFound)
	}))
	t.Cleanup(redirectSource.Close)

	authProvider, err := auth.NewValueAuth("xoxp-test-token", "")
	require.NoError(t, err)
	client := &MCPSlackClient{
		httpClient:   redirectSource.Client(),
		authProvider: authProvider,
		teamEndpoint: redirectSource.URL + "/",
	}

	_, err = client.AssistantSearchContext(context.Background(), AssistantSearchContextRequest{Query: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "302 Found")
	assert.Zero(t, redirectedRequests.Load(), "assistant search must not follow redirects")
}

func TestUnitAssistantSearchContextSendsExpectedHTTPRequest(t *testing.T) {
	type capturedRequest struct {
		method      string
		path        string
		authorize   string
		contentType string
		body        []byte
	}
	captured := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured <- capturedRequest{
			method:      r.Method,
			path:        r.URL.Path,
			authorize:   r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type"),
			body:        body,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"results":{"messages":[]}}`))
	}))
	t.Cleanup(server.Close)

	authProvider, err := auth.NewValueAuth("xoxp-test-token", "")
	require.NoError(t, err)
	client := &MCPSlackClient{httpClient: server.Client(), authProvider: authProvider, teamEndpoint: server.URL + "/"}
	request := AssistantSearchContextRequest{
		Query:                  "launch plan in:general from:<@U12345678>",
		ChannelTypes:           []string{"public_channel", "private_channel"},
		ContentTypes:           []string{"messages"},
		ContextChannelID:       "C12345678",
		Cursor:                 "next-page",
		Limit:                  20,
		IncludeDeletedUsers:    true,
		IncludeContextMessages: true,
		DisableSemanticSearch:  true,
	}

	_, err = client.AssistantSearchContext(context.Background(), request)
	require.NoError(t, err)
	got := <-captured
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/api/assistant.search.context", got.path)
	assert.Equal(t, "Bearer xoxp-test-token", got.authorize)
	assert.Equal(t, "application/json", got.contentType)
	wantBody, err := json.Marshal(request)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantBody), string(got.body))
}

func TestUnitAssistantSearchContextKeepsMalformedRateLimitsTyped(t *testing.T) {
	for _, retryAfter := range []string{"", "nonsense", "-7", "0", "301", "9223372036", "9223372036854775807"} {
		t.Run("Retry-After="+retryAfter, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", retryAfter)
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			t.Cleanup(server.Close)
			authProvider, err := auth.NewValueAuth("xoxp-test", "")
			require.NoError(t, err)
			client := &MCPSlackClient{httpClient: server.Client(), authProvider: authProvider, teamEndpoint: server.URL + "/"}

			_, err = client.AssistantSearchContext(context.Background(), AssistantSearchContextRequest{Query: "test"})
			require.Error(t, err)
			var rateLimited *slack.RateLimitedError
			require.ErrorAs(t, err, &rateLimited)
			assert.Equal(t, time.Second, rateLimited.RetryAfter)
		})
	}
}

func TestUnitAssistantSearchContextSanitizesNonSuccessBody(t *testing.T) {
	t.Run("extracts only a valid Slack error code", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"ok":false,"error":"missing_scope","private_detail":"` + strings.Repeat("secret", 1000) + `"}`))
		}))
		t.Cleanup(server.Close)
		authProvider, err := auth.NewValueAuth("xoxp-test", "")
		require.NoError(t, err)
		client := &MCPSlackClient{httpClient: server.Client(), authProvider: authProvider, teamEndpoint: server.URL + "/"}

		_, err = client.AssistantSearchContext(context.Background(), AssistantSearchContextRequest{Query: "test"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing_scope")
		assert.NotContains(t, err.Error(), "private_detail")
		assert.NotContains(t, err.Error(), "secret")
		assert.LessOrEqual(t, len(err.Error()), 256)
	})

	t.Run("does not echo non-JSON upstream content", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream\nfailed\x00 " + strings.Repeat("x", 4000) + " secret-tail"))
		}))
		t.Cleanup(server.Close)
		authProvider, err := auth.NewValueAuth("xoxp-test", "")
		require.NoError(t, err)
		client := &MCPSlackClient{httpClient: server.Client(), authProvider: authProvider, teamEndpoint: server.URL + "/"}

		_, err = client.AssistantSearchContext(context.Background(), AssistantSearchContextRequest{Query: "test"})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "upstream")
		assert.NotContains(t, err.Error(), "secret-tail")
		assert.LessOrEqual(t, len(err.Error()), 256)
	})

	t.Run("does not echo an upstream-controlled HTTP reason phrase", func(t *testing.T) {
		secretStatus := "502 " + strings.Repeat("private-status-", 100)
		httpClient := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Status:     secretStatus,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"ok":false}`)),
			}, nil
		})}
		authProvider, err := auth.NewValueAuth("xoxp-test", "")
		require.NoError(t, err)
		client := &MCPSlackClient{httpClient: httpClient, authProvider: authProvider, teamEndpoint: "https://slack.invalid/"}

		_, err = client.AssistantSearchContext(context.Background(), AssistantSearchContextRequest{Query: "test"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "502 Bad Gateway")
		assert.NotContains(t, err.Error(), "private-status")
		assert.LessOrEqual(t, len(err.Error()), 256)
	})
}

func TestUnitDemoXOXPReportsUserOAuthWithoutLiveClient(t *testing.T) {
	t.Setenv("SLACK_MCP_XOXP_TOKEN", "demo")
	t.Setenv("SLACK_MCP_XOXB_TOKEN", "")
	t.Setenv("SLACK_MCP_XOXC_TOKEN", "")
	t.Setenv("SLACK_MCP_XOXD_TOKEN", "")

	provider := New("stdio", zap.NewNop())
	assert.True(t, provider.IsOAuth())
	assert.False(t, provider.IsBotToken())
}

// TestUnitPatchUser verifies the targeted single-user cache patch behavior.
func TestUnitPatchUser(t *testing.T) {
	t.Run("fetches and adds new user to snapshot", func(t *testing.T) {
		initial := &UsersCache{
			Users:    map[string]slack.User{"U001": {ID: "U001", Name: "alice"}},
			UsersInv: map[string]string{"alice": "U001"},
		}

		newUser := slack.User{ID: "U002", Name: "bob"}
		ap := newTestApiProvider(
			&mockSlackClient{usersInfoResult: &[]slack.User{newUser}},
			initial,
		)

		result, err := ap.PatchUser(context.Background(), "U002")
		require.NoError(t, err)
		assert.Equal(t, "U002", result.ID)
		assert.Equal(t, "bob", result.Name)

		snapshot := ap.usersSnapshot.Load()
		assert.Len(t, snapshot.Users, 2)
		assert.Equal(t, "bob", snapshot.Users["U002"].Name)
		assert.Equal(t, "U002", snapshot.UsersInv["bob"])
		assert.Equal(t, "alice", snapshot.Users["U001"].Name)
	})

	t.Run("API error leaves snapshot unchanged", func(t *testing.T) {
		initial := &UsersCache{
			Users:    map[string]slack.User{"U001": {ID: "U001", Name: "alice"}},
			UsersInv: map[string]string{"alice": "U001"},
		}

		ap := newTestApiProvider(
			&mockSlackClient{usersInfoErr: errors.New("slack API error")},
			initial,
		)

		result, err := ap.PatchUser(context.Background(), "U999")
		assert.Error(t, err)
		assert.Nil(t, result)

		snapshot := ap.usersSnapshot.Load()
		assert.Len(t, snapshot.Users, 1)
	})

	t.Run("empty API result returns not found", func(t *testing.T) {
		initial := &UsersCache{
			Users:    map[string]slack.User{},
			UsersInv: map[string]string{},
		}

		ap := newTestApiProvider(
			&mockSlackClient{usersInfoResult: &[]slack.User{}},
			initial,
		)

		result, err := ap.PatchUser(context.Background(), "U999")
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("nil API result returns not found", func(t *testing.T) {
		initial := &UsersCache{
			Users:    map[string]slack.User{},
			UsersInv: map[string]string{},
		}

		ap := newTestApiProvider(
			&mockSlackClient{usersInfoResult: nil},
			initial,
		)

		result, err := ap.PatchUser(context.Background(), "U999")
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("does not mutate original snapshot", func(t *testing.T) {
		initial := &UsersCache{
			Users:    map[string]slack.User{"U001": {ID: "U001", Name: "alice"}},
			UsersInv: map[string]string{"alice": "U001"},
		}

		var snapshotRef atomic.Pointer[UsersCache]
		snapshotRef.Store(initial)

		newUser := slack.User{ID: "U002", Name: "bob"}
		ap := newTestApiProvider(
			&mockSlackClient{usersInfoResult: &[]slack.User{newUser}},
			initial,
		)

		_, err := ap.PatchUser(context.Background(), "U002")
		require.NoError(t, err)

		orig := snapshotRef.Load()
		_, hasNew := orig.Users["U002"]
		assert.False(t, hasNew, "original snapshot should not be mutated")
		assert.Len(t, orig.Users, 1)
	})

	t.Run("overwrites existing user with fresh data", func(t *testing.T) {
		initial := &UsersCache{
			Users:    map[string]slack.User{"U001": {ID: "U001", Name: "alice_old"}},
			UsersInv: map[string]string{"alice_old": "U001"},
		}

		updatedUser := slack.User{ID: "U001", Name: "alice_new"}
		ap := newTestApiProvider(
			&mockSlackClient{usersInfoResult: &[]slack.User{updatedUser}},
			initial,
		)

		result, err := ap.PatchUser(context.Background(), "U001")
		require.NoError(t, err)
		assert.Equal(t, "alice_new", result.Name)

		snapshot := ap.usersSnapshot.Load()
		assert.Equal(t, "alice_new", snapshot.Users["U001"].Name)
		assert.Equal(t, "U001", snapshot.UsersInv["alice_new"])
	})
}
