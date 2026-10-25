package provider

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// stubSlackAPI is a minimal SlackAPI stub used only by users-cache tests.
// All methods not under test panic so unexpected calls are immediately visible.
type stubSlackAPI struct {
	getUsersFunc   func(ctx context.Context, options ...slack.GetUsersOption) ([]slack.User, error)
	clientUserBoot func(ctx context.Context) (*edge.ClientUserBootResponse, error)
	getUsersInfo   func(users ...string) (*[]slack.User, error)
}

func (s *stubSlackAPI) GetUsersContext(ctx context.Context, options ...slack.GetUsersOption) ([]slack.User, error) {
	return s.getUsersFunc(ctx, options...)
}

func (s *stubSlackAPI) ClientUserBoot(ctx context.Context) (*edge.ClientUserBootResponse, error) {
	return s.clientUserBoot(ctx)
}

func (s *stubSlackAPI) GetUsersInfo(users ...string) (*[]slack.User, error) {
	if s.getUsersInfo != nil {
		return s.getUsersInfo(users...)
	}
	panic("GetUsersInfo not expected in this test")
}

// The remaining methods satisfy the SlackAPI interface but are not exercised
// by users-cache tests.
func (s *stubSlackAPI) AuthTest() (*slack.AuthTestResponse, error) {
	panic("not expected")
}
func (s *stubSlackAPI) AuthTestContext(_ context.Context) (*slack.AuthTestResponse, error) {
	panic("not expected")
}
func (s *stubSlackAPI) PostMessageContext(_ context.Context, _ string, _ ...slack.MsgOption) (string, string, error) {
	panic("not expected")
}
func (s *stubSlackAPI) MarkConversationContext(_ context.Context, _, _ string) error {
	panic("not expected")
}
func (s *stubSlackAPI) AddReactionContext(_ context.Context, _ string, _ slack.ItemRef) error {
	panic("not expected")
}
func (s *stubSlackAPI) RemoveReactionContext(_ context.Context, _ string, _ slack.ItemRef) error {
	panic("not expected")
}
func (s *stubSlackAPI) GetConversationHistoryContext(_ context.Context, _ *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
	panic("not expected")
}
func (s *stubSlackAPI) GetConversationRepliesContext(_ context.Context, _ *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error) {
	panic("not expected")
}
func (s *stubSlackAPI) SearchContext(_ context.Context, _ string, _ slack.SearchParameters) (*slack.SearchMessages, *slack.SearchFiles, error) {
	panic("not expected")
}
func (s *stubSlackAPI) GetFileInfoContext(_ context.Context, _ string, _, _ int) (*slack.File, []slack.Comment, *slack.Paging, error) {
	panic("not expected")
}
func (s *stubSlackAPI) GetFileContext(_ context.Context, _ string, _ io.Writer) error {
	panic("not expected")
}
func (s *stubSlackAPI) GetConversationInfoContext(_ context.Context, _ *slack.GetConversationInfoInput) (*slack.Channel, error) {
	panic("not expected")
}
func (s *stubSlackAPI) GetConversationsContext(_ context.Context, _ *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
	panic("not expected")
}
func (s *stubSlackAPI) GetConversationsForUserContext(_ context.Context, _ *slack.GetConversationsForUserParameters) ([]slack.Channel, string, error) {
	panic("not expected")
}
func (s *stubSlackAPI) UsersSearch(_ context.Context, _ string, _ int) ([]slack.User, error) {
	panic("not expected")
}
func (s *stubSlackAPI) ClientCounts(_ context.Context) (edge.ClientCountsResponse, error) {
	panic("not expected")
}
func (s *stubSlackAPI) GetMutedChannels(_ context.Context) (map[string]bool, error) {
	panic("not expected")
}
func (s *stubSlackAPI) GetUserGroupsContext(_ context.Context, _ ...slack.GetUserGroupsOption) ([]slack.UserGroup, error) {
	panic("not expected")
}
func (s *stubSlackAPI) GetUserGroupMembersContext(_ context.Context, _ string, _ ...slack.GetUserGroupMembersOption) ([]string, error) {
	panic("not expected")
}
func (s *stubSlackAPI) CreateUserGroupContext(_ context.Context, _ slack.UserGroup, _ ...slack.CreateUserGroupOption) (slack.UserGroup, error) {
	panic("not expected")
}
func (s *stubSlackAPI) UpdateUserGroupContext(_ context.Context, _ string, _ ...slack.UpdateUserGroupsOption) (slack.UserGroup, error) {
	panic("not expected")
}
func (s *stubSlackAPI) UpdateUserGroupMembersContext(_ context.Context, _ string, _ string, _ ...slack.UpdateUserGroupMembersOption) (slack.UserGroup, error) {
	panic("not expected")
}

// newTestApiProvider creates an ApiProvider wired to a stub SlackAPI with a
// temporary directory for cache files.
func newTestApiProvider(t *testing.T, client SlackAPI) (*ApiProvider, string) {
	t.Helper()
	dir := t.TempDir()
	ap := &ApiProvider{
		client:         client,
		logger:         zap.NewNop(),
		usersCachePath: filepath.Join(dir, "users.json"),
		rateLimiter:    rate.NewLimiter(rate.Inf, 0),
	}
	return ap, dir
}

// TestRefreshUsersInternalSlackConnectErrorNonFatal verifies that when
// ClientUserBoot returns an error (e.g. missing_argument for bot tokens on
// Enterprise Grid), refreshUsersInternal does NOT propagate the error.
// The users fetched via users.list must still be cached and the provider must
// be marked ready.  Regression test for issue #350.
func TestRefreshUsersInternalSlackConnectErrorNonFatal(t *testing.T) {
	baseUsers := []slack.User{
		{ID: "U1", Name: "alice"},
		{ID: "U2", Name: "bob"},
	}

	client := &stubSlackAPI{
		getUsersFunc: func(_ context.Context, _ ...slack.GetUsersOption) ([]slack.User, error) {
			return baseUsers, nil
		},
		clientUserBoot: func(_ context.Context) (*edge.ClientUserBootResponse, error) {
			// Simulate the error returned by Enterprise Grid for bot tokens.
			return nil, errors.New("missing_argument")
		},
	}

	ap, dir := newTestApiProvider(t, client)

	err := ap.refreshUsersInternal(context.Background(), true)
	require.NoError(t, err, "refreshUsersInternal must not fail when GetSlackConnect returns an error")

	// Provider must be marked ready.
	assert.True(t, ap.usersReady, "usersReady must be true after successful warmup")

	// Users from users.list must be in the in-memory snapshot.
	snapshot := ap.usersSnapshot.Load()
	require.NotNil(t, snapshot)
	assert.Len(t, snapshot.Users, 2)
	assert.Contains(t, snapshot.Users, "U1")
	assert.Contains(t, snapshot.Users, "U2")

	// Users must also be persisted to the cache file.
	data, err := os.ReadFile(filepath.Join(dir, "users.json"))
	require.NoError(t, err, "cache file must be written even when GetSlackConnect fails")
	assert.Contains(t, string(data), "alice")
	assert.Contains(t, string(data), "bob")
}

// TestRefreshUsersInternalSlackConnectSuccessNoSharedIMs verifies the happy
// path when ClientUserBoot succeeds but returns no shared IMs: the base users
// are cached and the provider is ready.
func TestRefreshUsersInternalSlackConnectSuccessNoSharedIMs(t *testing.T) {
	baseUsers := []slack.User{
		{ID: "U1", Name: "alice"},
	}

	client := &stubSlackAPI{
		getUsersFunc: func(_ context.Context, _ ...slack.GetUsersOption) ([]slack.User, error) {
			return baseUsers, nil
		},
		clientUserBoot: func(_ context.Context) (*edge.ClientUserBootResponse, error) {
			// Success, but no shared / ext-shared IMs so GetUsersInfo is not called.
			return &edge.ClientUserBootResponse{}, nil
		},
	}

	ap, _ := newTestApiProvider(t, client)
	err := ap.refreshUsersInternal(context.Background(), true)
	require.NoError(t, err)
	assert.True(t, ap.usersReady)

	snapshot := ap.usersSnapshot.Load()
	require.NotNil(t, snapshot)
	assert.Contains(t, snapshot.Users, "U1")
}
