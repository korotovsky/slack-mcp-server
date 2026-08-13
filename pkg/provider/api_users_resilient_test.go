package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// fakeUsersAPI satisfies SlackAPI but only implements what the users cache walk
// touches; every other method is unused here and left nil via embedding.
type fakeUsersAPI struct {
	SlackAPI
	c *slack.Client
}

func (f *fakeUsersAPI) GetUsersPaginated(options ...slack.GetUsersOption) slack.UserPagination {
	return f.c.GetUsersPaginated(options...)
}

// ClientUserBoot is reached via GetSlackConnect on the full fetchAndStoreUsers
// path. An empty response means no Slack Connect users to merge.
func (f *fakeUsersAPI) ClientUserBoot(ctx context.Context) (*edge.ClientUserBootResponse, error) {
	return &edge.ClientUserBootResponse{}, nil
}

// usersServer mimics Slack's users.list. It serves totalPages pages of
// usersPerPage members each, and returns HTTP 500 on the page in fail500OnPage
// for the first fail500Times requests to that page.
type usersServer struct {
	totalPages    int
	usersPerPage  int
	fail500OnPage int
	fail500Times  int

	seen500  int32
	requests int32
}

func (s *usersServer) handler(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&s.requests, 1)
	_ = r.ParseForm()
	cursor := r.Form.Get("cursor")

	page := 0
	if cursor != "" {
		fmt.Sscanf(cursor, "page%d", &page)
	}

	if page == s.fail500OnPage && atomic.LoadInt32(&s.seen500) < int32(s.fail500Times) {
		atomic.AddInt32(&s.seen500, 1)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	members := make([]slack.User, 0, s.usersPerPage)
	for i := 0; i < s.usersPerPage; i++ {
		id := fmt.Sprintf("U%03d%03d", page, i)
		members = append(members, slack.User{ID: id, Name: "user-" + id})
	}

	next := ""
	if page+1 < s.totalPages {
		next = fmt.Sprintf("page%d", page+1)
	}

	resp := map[string]any{
		"ok":                true,
		"members":           members,
		"response_metadata": map[string]string{"next_cursor": next},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func newTestProvider(t *testing.T, srv *usersServer) (*ApiProvider, string, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))

	client := slack.New("xoxc-test", slack.OptionAPIURL(ts.URL+"/"))
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "users_cache.json")

	ap := &ApiProvider{
		client:         &fakeUsersAPI{c: client},
		logger:         zap.NewNop(),
		usersCachePath: cachePath,
	}
	return ap, cachePath, ts
}

// A transient 500 mid-walk must be retried, not abort the enumeration.
// This is the exact failure that killed a 14-minute walk in the field.
func TestFetchUsersResilient_RetriesTransient500(t *testing.T) {
	t.Setenv("SLACK_MCP_USERS_CHECKPOINT_PAGES", "2")
	t.Setenv("SLACK_MCP_USERS_MAX_RETRIES", "5")

	srv := &usersServer{totalPages: 5, usersPerPage: 10, fail500OnPage: 3, fail500Times: 1}
	ap, _, ts := newTestProvider(t, srv)
	defer ts.Close()

	users, partial, err := ap.fetchUsersResilient(context.Background(), slack.GetUsersOptionLimit(10))
	if err != nil {
		t.Fatalf("expected transient 500 to be retried, got error: %v", err)
	}
	if partial {
		t.Fatal("a fully-walked roster must not be reported as partial")
	}
	if got, want := len(users), 50; got != want {
		t.Fatalf("expected %d users across all pages, got %d", want, got)
	}
	if atomic.LoadInt32(&srv.seen500) != 1 {
		t.Fatalf("expected the 500 to be served once, got %d", srv.seen500)
	}
	// 5 pages + 1 failed attempt that was retried
	if got := atomic.LoadInt32(&srv.requests); got != 6 {
		t.Fatalf("expected 6 requests (5 pages + 1 retry), got %d", got)
	}
}

// Baseline: upstream's GetUsersContext aborts the whole walk on the same 500.
// This documents the behaviour the patch changes.
func TestUpstreamGetUsersContext_AbortsOn500(t *testing.T) {
	srv := &usersServer{totalPages: 5, usersPerPage: 10, fail500OnPage: 3, fail500Times: 1}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	client := slack.New("xoxc-test", slack.OptionAPIURL(ts.URL+"/"))
	users, err := client.GetUsersContext(context.Background(), slack.GetUsersOptionLimit(10))
	if err == nil {
		t.Fatalf("expected upstream to fail on a 500, it did not")
	}
	// Upstream hands back the partial results alongside the error, and the
	// production caller discards them. 30 users = 3 successful pages.
	if len(users) != 30 {
		t.Logf("upstream returned %d partial users with the error", len(users))
	}
	t.Logf("upstream error (discarded by caller): %v", err)
}

// When retries are exhausted, the collected roster must be kept and checkpointed
// rather than thrown away.
func TestFetchUsersResilient_KeepsPartialWhenRetriesExhausted(t *testing.T) {
	t.Setenv("SLACK_MCP_USERS_CHECKPOINT_PAGES", "1")
	t.Setenv("SLACK_MCP_USERS_MAX_RETRIES", "2")

	// Page 3 fails forever.
	srv := &usersServer{totalPages: 6, usersPerPage: 10, fail500OnPage: 3, fail500Times: 1 << 30}
	ap, cachePath, ts := newTestProvider(t, srv)
	defer ts.Close()

	users, partial, err := ap.fetchUsersResilient(context.Background(), slack.GetUsersOptionLimit(10))
	if err != nil {
		t.Fatalf("expected partial success, got error: %v", err)
	}
	if !partial {
		t.Fatal("an aborted walk must be reported as partial")
	}
	if got, want := len(users), 30; got != want {
		t.Fatalf("expected %d users from the 3 pages that succeeded, got %d", want, got)
	}

	data, readErr := os.ReadFile(cachePath)
	if readErr != nil {
		t.Fatalf("expected a checkpoint file at %s: %v", cachePath, readErr)
	}
	var cached []slack.User
	if err := json.Unmarshal(data, &cached); err != nil {
		t.Fatalf("checkpoint is not valid JSON: %v", err)
	}
	if len(cached) == 0 {
		t.Fatal("checkpoint file is empty; 14 minutes of work would still be lost")
	}
	t.Logf("checkpoint retained %d users", len(cached))
}

// A checkpoint must never overwrite an existing complete cache during a
// background refresh, or a failed refresh would downgrade good data.
func TestCheckpointUsers_DoesNotClobberReadyCache(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "users_cache.json")
	if err := os.WriteFile(cachePath, []byte(`[{"id":"UCOMPLETE"}]`), 0600); err != nil {
		t.Fatal(err)
	}

	ap := &ApiProvider{logger: zap.NewNop(), usersCachePath: cachePath}
	ap.usersReady.Store(true) // a usable cache already exists

	ap.checkpointUsers([]slack.User{{ID: "UPARTIAL", Name: "partial"}})

	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `[{"id":"UCOMPLETE"}]` {
		t.Fatalf("existing cache was clobbered by a partial checkpoint: %s", data)
	}
}

// Regression: the guard on checkpointUsers alone is not sufficient. The final
// write in fetchAndStoreUsers is unconditional, so a background refresh that
// walks only part of the roster would still overwrite a complete cache through
// that path. Exercises the whole function, not just the checkpoint helper.
func TestFetchAndStoreUsers_PartialDoesNotClobberCompleteCache(t *testing.T) {
	t.Setenv("SLACK_MCP_USERS_CHECKPOINT_PAGES", "1")
	t.Setenv("SLACK_MCP_USERS_MAX_RETRIES", "1")

	// Page 2 fails forever, so the walk can never complete.
	srv := &usersServer{totalPages: 6, usersPerPage: 10, fail500OnPage: 2, fail500Times: 1 << 30}
	ap, cachePath, ts := newTestProvider(t, srv)
	defer ts.Close()

	complete := `[{"id":"UCOMPLETE","name":"complete"}]`
	if err := os.WriteFile(cachePath, []byte(complete), 0600); err != nil {
		t.Fatal(err)
	}

	// A complete cache and snapshot already exist: this is a background refresh.
	ap.usersReady.Store(true)
	ap.usersSnapshot.Store(&UsersCache{
		Users:    map[string]slack.User{"UCOMPLETE": {ID: "UCOMPLETE", Name: "complete"}},
		UsersInv: map[string]string{"complete": "UCOMPLETE"},
	})

	if err := ap.fetchAndStoreUsers(context.Background()); err != nil {
		t.Fatalf("a partial background refresh should be a no-op, got error: %v", err)
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != complete {
		t.Fatalf("complete cache was downgraded to a partial roster:\n%s", data)
	}

	// The in-memory snapshot must not be downgraded either.
	snap := ap.usersSnapshot.Load()
	if _, ok := snap.Users["UCOMPLETE"]; !ok {
		t.Fatal("snapshot lost the complete roster during a partial refresh")
	}
}

// Cold start (no usable cache) must checkpoint, since partial beats nothing.
func TestCheckpointUsers_WritesWhenNotReady(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "users_cache.json")

	ap := &ApiProvider{logger: zap.NewNop(), usersCachePath: cachePath}
	ap.usersReady.Store(false)

	ap.checkpointUsers([]slack.User{{ID: "UPARTIAL", Name: "partial"}})

	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("expected checkpoint on cold start: %v", err)
	}
	var cached []slack.User
	if err := json.Unmarshal(data, &cached); err != nil || len(cached) != 1 {
		t.Fatalf("bad checkpoint contents: %s", data)
	}
}
