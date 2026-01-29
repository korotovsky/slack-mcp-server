package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/limiter"
	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"github.com/korotovsky/slack-mcp-server/pkg/transport"
	"github.com/rusq/slackdump/v3/auth"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

const usersNotReadyMsg = "users cache is not ready yet, sync process is still running... please wait"
const channelsNotReadyMsg = "channels cache is not ready yet, sync process is still running... please wait"
const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36"

var AllChanTypes = []string{"mpim", "im", "public_channel", "private_channel"}
var PrivateChanType = "private_channel"
var PubChanType = "public_channel"

var ErrUsersNotReady = errors.New(usersNotReadyMsg)
var ErrChannelsNotReady = errors.New(channelsNotReadyMsg)

// getCacheDir returns the appropriate cache directory for slack-mcp-server
func getCacheDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		// Fallback to current directory if we can't get user cache dir
		return "."
	}

	dir := filepath.Join(cacheDir, "slack-mcp-server")
	if err := os.MkdirAll(dir, 0755); err != nil {
		// Fallback to current directory if we can't create cache dir
		return "."
	}
	return dir
}

type UsersCache struct {
	Users    map[string]slack.User `json:"users"`
	UsersInv map[string]string     `json:"users_inv"`
}

type ChannelsCache struct {
	Channels    map[string]Channel `json:"channels"`
	ChannelsInv map[string]string  `json:"channels_inv"`
}

type Channel struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Topic       string   `json:"topic"`
	Purpose     string   `json:"purpose"`
	MemberCount int      `json:"memberCount"`
	IsMpIM      bool     `json:"mpim"`
	IsIM        bool     `json:"im"`
	IsPrivate   bool     `json:"private"`
	User        string   `json:"user,omitempty"`    // User ID for IM channels
	Members     []string `json:"members,omitempty"` // Member IDs for the channel
}

type SlackAPI interface {
	// Standard slack-go API methods
	AuthTest() (*slack.AuthTestResponse, error)
	AuthTestContext(ctx context.Context) (*slack.AuthTestResponse, error)
	GetUsersContext(ctx context.Context, options ...slack.GetUsersOption) ([]slack.User, error)
	GetUsersInfo(users ...string) (*[]slack.User, error)
	PostMessageContext(ctx context.Context, channel string, options ...slack.MsgOption) (string, string, error)
	MarkConversationContext(ctx context.Context, channel, ts string) error
	AddReactionContext(ctx context.Context, name string, item slack.ItemRef) error
	RemoveReactionContext(ctx context.Context, name string, item slack.ItemRef) error

	// Used to get messages
	GetConversationHistoryContext(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error)
	GetConversationRepliesContext(ctx context.Context, params *slack.GetConversationRepliesParameters) (msgs []slack.Message, hasMore bool, nextCursor string, err error)
	SearchContext(ctx context.Context, query string, params slack.SearchParameters) (*slack.SearchMessages, *slack.SearchFiles, error)

	// Used to get files
	GetFileInfoContext(ctx context.Context, fileID string, count, page int) (*slack.File, []slack.Comment, *slack.Paging, error)
	GetFileContext(ctx context.Context, downloadURL string, writer io.Writer) error

	// Used to get channels list from both Slack and Enterprise Grid versions
	GetConversationsContext(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error)

	// Edge API methods
	ClientUserBoot(ctx context.Context) (*edge.ClientUserBootResponse, error)
}

type MCPSlackClient struct {
	slackClient *slack.Client
	edgeClient  *edge.Client

	authResponse *slack.AuthTestResponse
	authProvider auth.Provider

	isEnterprise bool
	isOAuth      bool
	isBotToken   bool
	teamEndpoint string
}

type ApiProvider struct {
	transport string
	client    SlackAPI
	logger    *zap.Logger

	rateLimiter *rate.Limiter

	users      map[string]slack.User
	usersInv   map[string]string
	usersCache string
	usersReady bool

	channels      map[string]Channel
	channelsInv   map[string]string
	channelsCache string
	channelsReady bool
}

func NewMCPSlackClient(authProvider auth.Provider, logger *zap.Logger) (*MCPSlackClient, error) {
	httpClient := transport.ProvideHTTPClient(authProvider.Cookies(), logger)

	slackOpts := []slack.Option{slack.OptionHTTPClient(httpClient)}
	if os.Getenv("SLACK_MCP_GOVSLACK") == "true" {
		slackOpts = append(slackOpts, slack.OptionAPIURL("https://slack-gov.com/api/"))
	}
	slackClient := slack.New(authProvider.SlackToken(), slackOpts...)

	authResp, err := slackClient.AuthTest()
	if err != nil {
		return nil, err
	}

	authResponse := &slack.AuthTestResponse{
		URL:          authResp.URL,
		Team:         authResp.Team,
		User:         authResp.User,
		TeamID:       authResp.TeamID,
		UserID:       authResp.UserID,
		EnterpriseID: authResp.EnterpriseID,
		BotID:        authResp.BotID,
	}

	slackClient = slack.New(authProvider.SlackToken(),
		slack.OptionHTTPClient(httpClient),
		slack.OptionAPIURL(authResp.URL+"api/"),
	)

	edgeClient, err := edge.NewWithInfo(authResponse, authProvider,
		edge.OptionHTTPClient(httpClient),
	)
	if err != nil {
		return nil, err
	}

	isEnterprise := authResp.EnterpriseID != ""
	token := authProvider.SlackToken()

	// Token type detection
	// isOAuth: Official OAuth tokens (xoxp or xoxb) - uses Standard API
	// isBotToken: Bot token - determines feature availability (e.g., search)
	isOAuth := strings.HasPrefix(token, "xoxp-") || strings.HasPrefix(token, "xoxb-")
	isBotToken := strings.HasPrefix(token, "xoxb-")

	return &MCPSlackClient{
		slackClient:  slackClient,
		edgeClient:   edgeClient,
		authResponse: authResponse,
		authProvider: authProvider,
		isEnterprise: isEnterprise,
		isOAuth:      isOAuth,
		isBotToken:   isBotToken,
		teamEndpoint: authResp.URL,
	}, nil
}

func (c *MCPSlackClient) AuthTest() (*slack.AuthTestResponse, error) {
	if os.Getenv("SLACK_MCP_XOXP_TOKEN") == "demo" || (os.Getenv("SLACK_MCP_XOXC_TOKEN") == "demo" && os.Getenv("SLACK_MCP_XOXD_TOKEN") == "demo") {
		return &slack.AuthTestResponse{
			URL:          "https://_.slack.com",
			Team:         "Demo Team",
			User:         "Username",
			TeamID:       "TEAM123456",
			UserID:       "U1234567890",
			EnterpriseID: "",
			BotID:        "",
		}, nil
	}

	if c.authResponse != nil {
		return c.authResponse, nil
	}

	return c.slackClient.AuthTest()
}

func (c *MCPSlackClient) AuthTestContext(ctx context.Context) (*slack.AuthTestResponse, error) {
	return c.slackClient.AuthTestContext(ctx)
}

func (c *MCPSlackClient) GetUsersContext(ctx context.Context, options ...slack.GetUsersOption) ([]slack.User, error) {
	return c.slackClient.GetUsersContext(ctx, options...)
}

func (c *MCPSlackClient) GetUsersInfo(users ...string) (*[]slack.User, error) {
	return c.slackClient.GetUsersInfo(users...)
}

func (c *MCPSlackClient) MarkConversationContext(ctx context.Context, channel, ts string) error {
	return c.slackClient.MarkConversationContext(ctx, channel, ts)
}

func (c *MCPSlackClient) GetConversationsContext(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
	// Please see https://github.com/korotovsky/slack-mcp-server/issues/73
	// It seems that `conversations.list` works with `xoxp` tokens within Enterprise Grid setups
	// and if `xoxc`/`xoxd` defined we fallback to edge client.
	// In non Enterprise Grid setups we always use `conversations.list` api as it accepts both token types wtf.
	if c.isEnterprise {
		if c.isOAuth {
			return c.slackClient.GetConversationsContext(ctx, params)
		} else {
			edgeChannels, _, err := c.edgeClient.GetConversationsContext(ctx, nil)
			if err != nil {
				return nil, "", err
			}

			var channels []slack.Channel
			for _, ec := range edgeChannels {
				if params != nil && params.ExcludeArchived && ec.IsArchived {
					continue
				}

				channels = append(channels, slack.Channel{
					IsGeneral: ec.IsGeneral,
					GroupConversation: slack.GroupConversation{
						Conversation: slack.Conversation{
							ID:                 ec.ID,
							IsIM:               ec.IsIM,
							IsMpIM:             ec.IsMpIM,
							IsPrivate:          ec.IsPrivate,
							Created:            slack.JSONTime(ec.Created.Time().UnixMilli()),
							Unlinked:           ec.Unlinked,
							NameNormalized:     ec.NameNormalized,
							IsShared:           ec.IsShared,
							IsExtShared:        ec.IsExtShared,
							IsOrgShared:        ec.IsOrgShared,
							IsPendingExtShared: ec.IsPendingExtShared,
							NumMembers:         ec.NumMembers,
						},
						Name:       ec.Name,
						IsArchived: ec.IsArchived,
						Members:    ec.Members,
						Topic: slack.Topic{
							Value: ec.Topic.Value,
						},
						Purpose: slack.Purpose{
							Value: ec.Purpose.Value,
						},
					},
				})
			}

			return channels, "", nil
		}
	}

	return c.slackClient.GetConversationsContext(ctx, params)
}

func (c *MCPSlackClient) GetConversationHistoryContext(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
	return c.slackClient.GetConversationHistoryContext(ctx, params)
}

func (c *MCPSlackClient) GetConversationRepliesContext(ctx context.Context, params *slack.GetConversationRepliesParameters) (msgs []slack.Message, hasMore bool, nextCursor string, err error) {
	return c.slackClient.GetConversationRepliesContext(ctx, params)
}

func (c *MCPSlackClient) SearchContext(ctx context.Context, query string, params slack.SearchParameters) (*slack.SearchMessages, *slack.SearchFiles, error) {
	return c.slackClient.SearchContext(ctx, query, params)
}

func (c *MCPSlackClient) PostMessageContext(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
	return c.slackClient.PostMessageContext(ctx, channelID, options...)
}

func (c *MCPSlackClient) AddReactionContext(ctx context.Context, name string, item slack.ItemRef) error {
	return c.slackClient.AddReactionContext(ctx, name, item)
}

func (c *MCPSlackClient) RemoveReactionContext(ctx context.Context, name string, item slack.ItemRef) error {
	return c.slackClient.RemoveReactionContext(ctx, name, item)
}

func (c *MCPSlackClient) GetFileInfoContext(ctx context.Context, fileID string, count, page int) (*slack.File, []slack.Comment, *slack.Paging, error) {
	return c.slackClient.GetFileInfoContext(ctx, fileID, count, page)
}

func (c *MCPSlackClient) GetFileContext(ctx context.Context, downloadURL string, writer io.Writer) error {
	return c.slackClient.GetFileContext(ctx, downloadURL, writer)
}

func (c *MCPSlackClient) ClientUserBoot(ctx context.Context) (*edge.ClientUserBootResponse, error) {
	return c.edgeClient.ClientUserBoot(ctx)
}

func (c *MCPSlackClient) IsEnterprise() bool {
	return c.isEnterprise
}

func (c *MCPSlackClient) AuthResponse() *slack.AuthTestResponse {
	return c.authResponse
}

func (c *MCPSlackClient) IsBotToken() bool {
	return c.isBotToken
}

func (c *MCPSlackClient) Raw() struct {
	Slack *slack.Client
	Edge  *edge.Client
} {
	return struct {
		Slack *slack.Client
		Edge  *edge.Client
	}{
		Slack: c.slackClient,
		Edge:  c.edgeClient,
	}
}

func New(transport string, logger *zap.Logger) *ApiProvider {
	var (
		authProvider auth.ValueAuth
		err          error
	)

	// Read all environment variables
	xoxpToken := os.Getenv("SLACK_MCP_XOXP_TOKEN")
	xoxbToken := os.Getenv("SLACK_MCP_XOXB_TOKEN")
	xoxcToken := os.Getenv("SLACK_MCP_XOXC_TOKEN")
	xoxdToken := os.Getenv("SLACK_MCP_XOXD_TOKEN")

	// Warn if both user and bot tokens are set
	if xoxpToken != "" && xoxbToken != "" {
		logger.Warn(
			"Both SLACK_MCP_XOXP_TOKEN and SLACK_MCP_XOXB_TOKEN are set. "+
				"Using User token (xoxp) for full features. "+
				"Bot token will be ignored.",
			zap.String("context", "console"),
		)
	}

	// Priority 1: XOXP token (User OAuth)
	if xoxpToken != "" {
		authProvider, err = auth.NewValueAuth(xoxpToken, "")
		if err != nil {
			logger.Fatal("Failed to create auth provider with XOXP token", zap.Error(err))
		}

		return newWithXOXP(transport, authProvider, logger)
	}

	// Priority 2: XOXB token (Bot)
	if xoxbToken != "" {
		authProvider, err = auth.NewValueAuth(xoxbToken, "")
		if err != nil {
			logger.Fatal("Failed to create auth provider with XOXB token", zap.Error(err))
		}

		logger.Info("Using Bot token authentication",
			zap.String("context", "console"),
			zap.String("token_type", "xoxb"),
		)

		return newWithXOXB(transport, authProvider, logger)
	}

	// Priority 3: XOXC/XOXD tokens (session-based)
	if xoxcToken == "" || xoxdToken == "" {
		logger.Fatal("Authentication required: Either SLACK_MCP_XOXP_TOKEN, SLACK_MCP_XOXB_TOKEN, or both SLACK_MCP_XOXC_TOKEN and SLACK_MCP_XOXD_TOKEN must be provided")
	}

	authProvider, err = auth.NewValueAuth(xoxcToken, xoxdToken)
	if err != nil {
		logger.Fatal("Failed to create auth provider with XOXC/XOXD tokens", zap.Error(err))
	}

	return newWithXOXC(transport, authProvider, logger)
}

func newWithXOXP(transport string, authProvider auth.ValueAuth, logger *zap.Logger) *ApiProvider {
	var (
		client *MCPSlackClient
		err    error
	)

	usersCache := os.Getenv("SLACK_MCP_USERS_CACHE")
	if usersCache == "" {
		cacheDir := getCacheDir()
		usersCache = filepath.Join(cacheDir, "users_cache.json")
	}

	channelsCache := os.Getenv("SLACK_MCP_CHANNELS_CACHE")
	if channelsCache == "" {
		cacheDir := getCacheDir()
		channelsCache = filepath.Join(cacheDir, "channels_cache_v2.json")
	}

	if os.Getenv("SLACK_MCP_XOXP_TOKEN") == "demo" || (os.Getenv("SLACK_MCP_XOXC_TOKEN") == "demo" && os.Getenv("SLACK_MCP_XOXD_TOKEN") == "demo") {
		logger.Info("Demo credentials are set, skip.")
	} else {
		client, err = NewMCPSlackClient(authProvider, logger)
		if err != nil {
			logger.Fatal("Failed to create MCP Slack client", zap.Error(err))
		}
	}

	return &ApiProvider{
		transport: transport,
		client:    client,
		logger:    logger,

		rateLimiter: limiter.Tier2.Limiter(),

		users:      make(map[string]slack.User),
		usersInv:   map[string]string{},
		usersCache: usersCache,

		channels:      make(map[string]Channel),
		channelsInv:   map[string]string{},
		channelsCache: channelsCache,
	}
}

func newWithXOXB(transport string, authProvider auth.ValueAuth, logger *zap.Logger) *ApiProvider {
	// Bot tokens do not support demo mode, but otherwise share the same
	// initialization logic as user OAuth tokens.
	return newWithXOXP(transport, authProvider, logger)
}

func newWithXOXC(transport string, authProvider auth.ValueAuth, logger *zap.Logger) *ApiProvider {
	var (
		client *MCPSlackClient
		err    error
	)

	usersCache := os.Getenv("SLACK_MCP_USERS_CACHE")
	if usersCache == "" {
		cacheDir := getCacheDir()
		usersCache = filepath.Join(cacheDir, "users_cache.json")
	}

	channelsCache := os.Getenv("SLACK_MCP_CHANNELS_CACHE")
	if channelsCache == "" {
		cacheDir := getCacheDir()
		channelsCache = filepath.Join(cacheDir, "channels_cache_v2.json")
	}

	if os.Getenv("SLACK_MCP_XOXP_TOKEN") == "demo" || (os.Getenv("SLACK_MCP_XOXC_TOKEN") == "demo" && os.Getenv("SLACK_MCP_XOXD_TOKEN") == "demo") {
		logger.Info("Demo credentials are set, skip.")
	} else {
		client, err = NewMCPSlackClient(authProvider, logger)
		if err != nil {
			logger.Fatal("Failed to create MCP Slack client", zap.Error(err))
		}
	}

	return &ApiProvider{
		transport: transport,
		client:    client,
		logger:    logger,

		rateLimiter: limiter.Tier2.Limiter(),

		users:      make(map[string]slack.User),
		usersInv:   map[string]string{},
		usersCache: usersCache,

		channels:      make(map[string]Channel),
		channelsInv:   map[string]string{},
		channelsCache: channelsCache,
	}
}

func (ap *ApiProvider) RefreshUsers(ctx context.Context) error {
	var (
		list         []slack.User
		usersCounter = 0
		optionLimit  = slack.GetUsersOptionLimit(1000)
	)

	if data, err := ioutil.ReadFile(ap.usersCache); err == nil {
		var cachedUsers []slack.User
		if err := json.Unmarshal(data, &cachedUsers); err != nil {
			ap.logger.Warn("Failed to unmarshal users cache, will refetch",
				zap.String("cache_file", ap.usersCache),
				zap.Error(err))
		} else {
			for _, u := range cachedUsers {
				ap.users[u.ID] = u
				ap.usersInv[u.Name] = u.ID
			}
			ap.logger.Info("Loaded users from cache",
				zap.Int("count", len(cachedUsers)),
				zap.String("cache_file", ap.usersCache))
			ap.usersReady = true
			return nil
		}
	}

	users, err := ap.client.GetUsersContext(ctx,
		optionLimit,
	)
	if err != nil {
		ap.logger.Error("Failed to fetch users", zap.Error(err))
		return err
	} else {
		list = append(list, users...)
	}

	for _, user := range users {
		ap.users[user.ID] = user
		ap.usersInv[user.Name] = user.ID
		usersCounter++
	}

	users, err = ap.GetSlackConnect(ctx)
	if err != nil {
		ap.logger.Error("Failed to fetch users from Slack Connect", zap.Error(err))
		return err
	} else {
		list = append(list, users...)
	}

	for _, user := range users {
		ap.users[user.ID] = user
		ap.usersInv[user.Name] = user.ID
		usersCounter++
	}

	if data, err := json.MarshalIndent(list, "", "  "); err != nil {
		ap.logger.Error("Failed to marshal users for cache", zap.Error(err))
	} else {
		if err := ioutil.WriteFile(ap.usersCache, data, 0644); err != nil {
			ap.logger.Error("Failed to write cache file",
				zap.String("cache_file", ap.usersCache),
				zap.Error(err))
		} else {
			ap.logger.Info("Wrote users to cache",
				zap.Int("count", usersCounter),
				zap.String("cache_file", ap.usersCache))
		}
	}

	ap.usersReady = true

	return nil
}

func (ap *ApiProvider) RefreshChannels(ctx context.Context) error {
	if data, err := ioutil.ReadFile(ap.channelsCache); err == nil {
		var cachedChannels []Channel
		if err := json.Unmarshal(data, &cachedChannels); err != nil {
			ap.logger.Warn("Failed to unmarshal channels cache, will refetch",
				zap.String("cache_file", ap.channelsCache),
				zap.Error(err))
		} else {
			// Re-map channels with current users cache to ensure DM names are populated
			usersMap := ap.ProvideUsersMap().Users
			for _, c := range cachedChannels {
				// For IM channels, re-generate the name and purpose using current users cache
				if c.IsIM {
					// Re-map the channel to get updated user name if available
					remappedChannel := mapChannel(
						c.ID, "", "", c.Topic, c.Purpose,
						c.User, c.Members, c.MemberCount,
						c.IsIM, c.IsMpIM, c.IsPrivate,
						usersMap,
					)
					ap.channels[c.ID] = remappedChannel
					ap.channelsInv[remappedChannel.Name] = c.ID
				} else {
					ap.channels[c.ID] = c
					ap.channelsInv[c.Name] = c.ID
				}
			}
			ap.logger.Info("Loaded channels from cache and re-mapped DM names",
				zap.Int("count", len(cachedChannels)),
				zap.String("cache_file", ap.channelsCache))
			ap.channelsReady = true
			return nil
		}
	}

	channels := ap.GetChannels(ctx, AllChanTypes)

	if data, err := json.MarshalIndent(channels, "", "  "); err != nil {
		ap.logger.Error("Failed to marshal channels for cache", zap.Error(err))
	} else {
		if err := ioutil.WriteFile(ap.channelsCache, data, 0644); err != nil {
			ap.logger.Error("Failed to write cache file",
				zap.String("cache_file", ap.channelsCache),
				zap.Error(err))
		} else {
			ap.logger.Info("Wrote channels to cache",
				zap.Int("count", len(channels)),
				zap.String("cache_file", ap.channelsCache))
		}
	}

	ap.channelsReady = true

	return nil
}

func (ap *ApiProvider) GetSlackConnect(ctx context.Context) ([]slack.User, error) {
	boot, err := ap.client.ClientUserBoot(ctx)
	if err != nil {
		ap.logger.Error("Failed to fetch client user boot", zap.Error(err))
		return nil, err
	}

	var collectedIDs []string
	for _, im := range boot.IMs {
		if !im.IsShared && !im.IsExtShared {
			continue
		}

		_, ok := ap.users[im.User]
		if !ok {
			collectedIDs = append(collectedIDs, im.User)
		}
	}

	res := make([]slack.User, 0, len(collectedIDs))
	if len(collectedIDs) > 0 {
		usersInfo, err := ap.client.GetUsersInfo(strings.Join(collectedIDs, ","))
		if err != nil {
			ap.logger.Error("Failed to fetch users info for shared IMs", zap.Error(err))
			return nil, err
		}

		for _, u := range *usersInfo {
			res = append(res, u)
		}
	}

	return res, nil
}

func (ap *ApiProvider) GetChannelsType(ctx context.Context, channelType string) []Channel {
	params := &slack.GetConversationsParameters{
		Types:           []string{channelType},
		Limit:           999,
		ExcludeArchived: true,
	}

	var (
		channels []slack.Channel
		chans    []Channel

		nextcur string
		err     error
	)

	for {
		if err := ap.rateLimiter.Wait(ctx); err != nil {
			ap.logger.Error("Rate limiter wait failed", zap.Error(err))
			return nil
		}

		channels, nextcur, err = ap.client.GetConversationsContext(ctx, params)
		ap.logger.Debug("Fetched channels for ",
			zap.String("channelType", channelType),
			zap.Int("count", len(channels)),
		)
		if err != nil {
			ap.logger.Error("Failed to fetch channels", zap.Error(err))
			break
		}

		for _, channel := range channels {
			ch := mapChannel(
				channel.ID,
				channel.Name,
				channel.NameNormalized,
				channel.Topic.Value,
				channel.Purpose.Value,
				channel.User,
				channel.Members,
				channel.NumMembers,
				channel.IsIM,
				channel.IsMpIM,
				channel.IsPrivate,
				ap.ProvideUsersMap().Users,
			)
			chans = append(chans, ch)
		}

		if nextcur == "" {
			break
		}

		params.Cursor = nextcur
	}
	return chans
}

func (ap *ApiProvider) GetChannels(ctx context.Context, channelTypes []string) []Channel {
	if len(channelTypes) == 0 {
		channelTypes = AllChanTypes
	}

	var chans []Channel
	for _, t := range AllChanTypes {
		var typeChannels = ap.GetChannelsType(ctx, t)
		chans = append(chans, typeChannels...)
	}

	for _, ch := range chans {
		ap.channels[ch.ID] = ch
		ap.channelsInv[ch.Name] = ch.ID
	}

	var res []Channel
	for _, t := range channelTypes {
		for _, channel := range ap.channels {
			if t == "public_channel" && !channel.IsPrivate {
				res = append(res, channel)
			}
			if t == "private_channel" && channel.IsPrivate {
				res = append(res, channel)
			}
			if t == "im" && channel.IsIM {
				res = append(res, channel)
			}
			if t == "mpim" && channel.IsMpIM {
				res = append(res, channel)
			}
		}
	}

	return res
}

func (ap *ApiProvider) ProvideUsersMap() *UsersCache {
	return &UsersCache{
		Users:    ap.users,
		UsersInv: ap.usersInv,
	}
}

func (ap *ApiProvider) ProvideChannelsMaps() *ChannelsCache {
	return &ChannelsCache{
		Channels:    ap.channels,
		ChannelsInv: ap.channelsInv,
	}
}

func (ap *ApiProvider) IsReady() (bool, error) {
	if !ap.usersReady {
		return false, ErrUsersNotReady
	}
	if !ap.channelsReady {
		return false, ErrChannelsNotReady
	}
	return true, nil
}

func (ap *ApiProvider) ServerTransport() string {
	return ap.transport
}

func (ap *ApiProvider) Slack() SlackAPI {
	return ap.client
}

func (ap *ApiProvider) IsBotToken() bool {
	client, ok := ap.client.(*MCPSlackClient)
	return ok && client != nil && client.IsBotToken()
}

func mapChannel(
	id, name, nameNormalized, topic, purpose, user string,
	members []string,
	numMembers int,
	isIM, isMpIM, isPrivate bool,
	usersMap map[string]slack.User,
) Channel {
	channelName := name
	finalPurpose := purpose
	finalTopic := topic
	finalMemberCount := numMembers

	var userID string
	if isIM {
		finalMemberCount = 2
		userID = user // Store the user ID for later re-mapping

		// If user field is empty but we have members, try to extract from members
		if userID == "" && len(members) > 0 {
			// For IM channels, members should contain the other user's ID
			// Try each member to find a valid user in the users map
			for _, memberID := range members {
				if _, ok := usersMap[memberID]; ok {
					userID = memberID
					break
				}
			}
		}

		if u, ok := usersMap[userID]; ok {
			channelName = "@" + u.Name
			finalPurpose = "DM with " + u.RealName
		} else if userID != "" {
			channelName = "@" + userID
			finalPurpose = "DM with " + userID
		} else {
			channelName = "@"
			finalPurpose = "DM with "
		}
		finalTopic = ""
	} else if isMpIM {
		if len(members) > 0 {
			finalMemberCount = len(members)
			var userNames []string
			for _, uid := range members {
				if u, ok := usersMap[uid]; ok {
					userNames = append(userNames, u.RealName)
				} else {
					userNames = append(userNames, uid)
				}
			}
			channelName = "@" + nameNormalized
			finalPurpose = "Group DM with " + strings.Join(userNames, ", ")
			finalTopic = ""
		}
	} else {
		channelName = "#" + nameNormalized
	}

	return Channel{
		ID:          id,
		Name:        channelName,
		Topic:       finalTopic,
		Purpose:     finalPurpose,
		MemberCount: finalMemberCount,
		IsIM:        isIM,
		IsMpIM:      isMpIM,
		IsPrivate:   isPrivate,
		User:        userID,
		Members:     members,
	}
}
