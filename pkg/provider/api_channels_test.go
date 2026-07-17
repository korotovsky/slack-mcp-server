package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/korotovsky/slack-mcp-server/pkg/limiter"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type channelListSlack struct {
	SlackAPI
	channels []slack.Channel
}

func (f *channelListSlack) GetConversationsContext(context.Context, *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
	return f.channels, "", nil
}

func TestUnitFilterChannelsByTypes(t *testing.T) {
	channels := []Channel{
		{ID: "C1", Name: "#general"},
		{ID: "C2", Name: "#secret", IsPrivate: true},
		{ID: "D1", Name: "@alice", IsPrivate: true, IsIM: true},
		{ID: "G1", Name: "mpdm-a-b-c", IsPrivate: true, IsMpIM: true},
	}

	tests := []struct {
		name  string
		types []string
		ids   []string
	}{
		{name: "public channel only", types: []string{"public_channel"}, ids: []string{"C1"}},
		{name: "private channel only", types: []string{"private_channel"}, ids: []string{"C2"}},
		{name: "IM only", types: []string{"im"}, ids: []string{"D1"}},
		{name: "MPIM only", types: []string{"mpim"}, ids: []string{"G1"}},
		{name: "all types", types: AllChanTypes, ids: []string{"C1", "C2", "D1", "G1"}},
		{name: "user OAuth startup excludes only IM", types: UserOAuthStartupChanTypes, ids: []string{"C1", "C2", "G1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := filterChannelsByTypes(channels, test.types)
			ids := make([]string, 0, len(got))
			for _, channel := range got {
				ids = append(ids, channel.ID)
			}
			assert.ElementsMatch(t, test.ids, ids)
		})
	}
}

func TestUnitGetChannelsFiltersEnterpriseLikeExtraIMBeforeSnapshot(t *testing.T) {
	client := &channelListSlack{channels: []slack.Channel{
		{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "C1"}, Name: "general"}},
		{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "C2", IsPrivate: true}, Name: "secret"}},
		{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "G1", IsPrivate: true, IsMpIM: true}, Name: "mpdm-a-b-c"}},
		{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "D1", IsPrivate: true, IsIM: true}}},
	}}
	ap := &ApiProvider{client: client, logger: zap.NewNop(), rateLimiter: limiter.Tier2.Limiter()}
	ap.usersSnapshot.Store(&UsersCache{Users: map[string]slack.User{}, UsersInv: map[string]string{}})

	got := ap.GetChannels(context.Background(), UserOAuthStartupChanTypes)
	require.Len(t, got, 3)
	for _, channel := range got {
		assert.False(t, channel.IsIM)
	}
	snapshot := ap.ProvideChannelsMaps()
	require.Len(t, snapshot.Channels, 3)
	_, foundIM := snapshot.Channels["D1"]
	assert.False(t, foundIM, "unsupported IM must not enter the startup snapshot")
}

func TestUnitRefreshChannelsDropsLegacyCachedIM(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "channels.json")
	cached := []Channel{
		{ID: "C1", Name: "#general"},
		{ID: "C2", Name: "#secret", IsPrivate: true},
		{ID: "G1", Name: "@mpdm-a-b-c", IsPrivate: true, IsMpIM: true},
		{ID: "D1", Name: "@alice_dm", IsPrivate: true, IsIM: true, User: "U1"},
	}
	data, err := json.Marshal(cached)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cachePath, data, 0o600))

	ap := &ApiProvider{logger: zap.NewNop(), channelsCachePath: cachePath, cacheTTL: time.Hour, isOAuth: true}
	ap.usersSnapshot.Store(&UsersCache{Users: map[string]slack.User{}, UsersInv: map[string]string{}})
	require.NoError(t, ap.refreshChannelsInternal(context.Background(), false))

	snapshot := ap.ProvideChannelsMaps()
	require.Len(t, snapshot.Channels, 3)
	_, foundIM := snapshot.Channels["D1"]
	assert.False(t, foundIM, "legacy cached IM must be discarded before snapshot use")
	assert.Contains(t, snapshot.Channels, "C1")
	assert.Contains(t, snapshot.Channels, "C2")
	assert.Contains(t, snapshot.Channels, "G1")
}

func TestUnitRefreshChannelsRetainsCachedIMForSessionAuth(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "channels.json")
	cached := []Channel{
		{ID: "C1", Name: "#general"},
		{ID: "D1", Name: "@alice_dm", IsPrivate: true, IsIM: true, User: "U1"},
	}
	data, err := json.Marshal(cached)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cachePath, data, 0o600))

	ap := &ApiProvider{logger: zap.NewNop(), channelsCachePath: cachePath, cacheTTL: time.Hour}
	ap.usersSnapshot.Store(&UsersCache{
		Users:    map[string]slack.User{"U1": {ID: "U1", Name: "alice"}},
		UsersInv: map[string]string{"alice": "U1"},
	})
	require.NoError(t, ap.refreshChannelsInternal(context.Background(), false))

	snapshot := ap.ProvideChannelsMaps()
	require.Len(t, snapshot.Channels, 2)
	assert.Contains(t, snapshot.Channels, "D1", "session auth must preserve available direct messages")
}

func TestUnitStartupChannelTypesAreAuthSpecific(t *testing.T) {
	tests := []struct {
		name      string
		provider  *ApiProvider
		wantTypes []string
	}{
		{name: "user OAuth omits IM", provider: &ApiProvider{isOAuth: true}, wantTypes: UserOAuthStartupChanTypes},
		{name: "bot OAuth preserves IM", provider: &ApiProvider{isOAuth: true, isBotToken: true}, wantTypes: AllChanTypes},
		{name: "session auth preserves IM", provider: &ApiProvider{}, wantTypes: AllChanTypes},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.ElementsMatch(t, test.wantTypes, test.provider.startupChannelTypes())
		})
	}
}

func TestUnitOAuthChannelCacheFilenamesPreventUserCacheContamination(t *testing.T) {
	assert.Equal(t, "channels_cache_v2_user_oauth.json", oauthChannelsCacheFilename(false))
	assert.Equal(t, "channels_cache_v2.json", oauthChannelsCacheFilename(true))
}

func TestUnitAllChanTypesConstant(t *testing.T) {
	assert.ElementsMatch(t, []string{"public_channel", "private_channel", "im", "mpim"}, AllChanTypes)
}

func TestUnitUserOAuthStartupChanTypesExcludeIM(t *testing.T) {
	assert.ElementsMatch(t, []string{"public_channel", "private_channel", "mpim"}, UserOAuthStartupChanTypes)
	assert.NotContains(t, UserOAuthStartupChanTypes, "im")
}
