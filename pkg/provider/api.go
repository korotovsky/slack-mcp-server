package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/limiter"
	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"github.com/korotovsky/slack-mcp-server/pkg/transport"
	"github.com/rusq/slackdump/v3/auth"
	"github.com/slack-go/slack"
)

const usersNotReadyMsg = "users cache is not ready yet, sync process is still running... please wait"
const channelsNotReadyMsg = "channels cache is not ready yet, sync process is still running... please wait"
const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36"

var AllChanTypes = []string{"mpim", "im", "public_channel", "private_channel"}
var PrivateChanType = "private_channel"
var PubChanType = "public_channel"

var ErrUsersNotReady = errors.New(usersNotReadyMsg)
var ErrChannelsNotReady = errors.New(channelsNotReadyMsg)

type UsersCache struct {
	Users    map[string]slack.User `json:"users"`
	UsersInv map[string]string     `json:"users_inv"`
}

type ChannelsCache struct {
	Channels    map[string]Channel `json:"channels"`
	ChannelsInv map[string]string  `json:"channels_inv"`
}

type Channel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Topic       string `json:"topic"`
	Purpose     string `json:"purpose"`
	MemberCount int    `json:"memberCount"`
	IsMpIM      bool   `json:"mpim"`
	IsIM        bool   `json:"im"`
	IsPrivate   bool   `json:"private"`
}

type SlackAPI interface {
	// Standard slack-go API methods
	AuthTest() (*slack.AuthTestResponse, error)
	AuthTestContext(ctx context.Context) (*slack.AuthTestResponse, error)
	GetUsersContext(ctx context.Context, options ...slack.GetUsersOption) ([]slack.User, error)
	GetUsersInfo(users ...string) (*[]slack.User, error)
	PostMessageContext(ctx context.Context, channel string, options ...slack.MsgOption) (string, string, error)

	// Useed to get messages
	GetConversationHistoryContext(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error)
	GetConversationRepliesContext(ctx context.Context, params *slack.GetConversationRepliesParameters) (msgs []slack.Message, hasMore bool, nextCursor string, err error)
	SearchContext(ctx context.Context, query string, params slack.SearchParameters) (*slack.SearchMessages, *slack.SearchFiles, error)

	// Useed to get channels list from both Slack and Enterprise Grid versions
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
	teamEndpoint string
}

type ApiProvider struct {
	transport string
	client    SlackAPI

	users      map[string]slack.User
	usersInv   map[string]string
	usersCache string
	usersReady bool

	channels      map[string]Channel
	channelsInv   map[string]string
	channelsCache string
	channelsReady bool
}

func NewMCPSlackClient(authProvider auth.Provider) (*MCPSlackClient, error) {
	httpClient := provideHTTPClient(authProvider.Cookies())

	slackClient := slack.New(authProvider.SlackToken(),
		slack.OptionHTTPClient(httpClient),
	)

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

	return &MCPSlackClient{
		slackClient:  slackClient,
		edgeClient:   edgeClient,
		authResponse: authResponse,
		authProvider: authProvider,
		isEnterprise: isEnterprise,
		teamEndpoint: authResp.URL,
	}, nil
}

func (c *MCPSlackClient) AuthTest() (*slack.AuthTestResponse, error) {
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

func (c *MCPSlackClient) GetConversationsContext(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
	if c.isEnterprise {
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

func (c *MCPSlackClient) ClientUserBoot(ctx context.Context) (*edge.ClientUserBootResponse, error) {
	return c.edgeClient.ClientUserBoot(ctx)
}

func (c *MCPSlackClient) IsEnterprise() bool {
	return c.isEnterprise
}

func (c *MCPSlackClient) AuthResponse() *slack.AuthTestResponse {
	return c.authResponse
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

func New(transport string) *ApiProvider {
	var (
		authProvider auth.ValueAuth
		err          error
	)

	// Check for XOXP token first (User OAuth)
	xoxpToken := os.Getenv("SLACK_MCP_XOXP_TOKEN")
	if xoxpToken != "" {
		authProvider, err = auth.NewValueAuth(xoxpToken, "")
		if err != nil {
			panic(err)
		}

		return newWithXOXP(transport, authProvider)
	}

	// Fall back to XOXC/XOXD tokens (session-based)
	xoxcToken := os.Getenv("SLACK_MCP_XOXC_TOKEN")
	xoxdToken := os.Getenv("SLACK_MCP_XOXD_TOKEN")

	if xoxcToken == "" || xoxdToken == "" {
		panic("Authentication required: Either SLACK_MCP_XOXP_TOKEN (User OAuth) or both SLACK_MCP_XOXC_TOKEN and SLACK_MCP_XOXD_TOKEN (session-based) environment variables must be provided")
	}

	authProvider, err = auth.NewValueAuth(xoxcToken, xoxdToken)
	if err != nil {
		panic(err)
	}

	return newWithXOXC(transport, authProvider)
}

func newWithXOXP(transport string, authProvider auth.ValueAuth) *ApiProvider {
	usersCache := os.Getenv("SLACK_MCP_USERS_CACHE")
	if usersCache == "" {
		usersCache = ".users_cache.json"
	}

	channelsCache := os.Getenv("SLACK_MCP_CHANNELS_CACHE")
	if channelsCache == "" {
		channelsCache = ".channels_cache.json"
	}

	client, err := NewMCPSlackClient(authProvider)
	if err != nil {
		panic(err)
	}

	return &ApiProvider{
		transport: transport,
		client:    client,

		users:      make(map[string]slack.User),
		usersInv:   map[string]string{},
		usersCache: usersCache,

		channels:      make(map[string]Channel),
		channelsInv:   map[string]string{},
		channelsCache: channelsCache,
	}
}

func newWithXOXC(transport string, authProvider auth.ValueAuth) *ApiProvider {
	usersCache := os.Getenv("SLACK_MCP_USERS_CACHE")
	if usersCache == "" {
		usersCache = ".users_cache.json"
	}

	channelsCache := os.Getenv("SLACK_MCP_CHANNELS_CACHE")
	if channelsCache == "" {
		channelsCache = ".channels_cache_v2.json"
	}

	client, err := NewMCPSlackClient(authProvider)
	if err != nil {
		panic(err)
	}

	return &ApiProvider{
		transport: transport,
		client:    client,

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
			log.Printf("Failed to unmarshal %s: %v; will refetch", ap.usersCache, err)
		} else {
			for _, u := range cachedUsers {
				ap.users[u.ID] = u
				ap.usersInv[u.Name] = u.ID
			}
			log.Printf("Loaded %d users from cache %q", len(cachedUsers), ap.usersCache)
			ap.usersReady = true
			return nil
		}
	}

	users, err := ap.client.GetUsersContext(ctx,
		optionLimit,
	)
	if err != nil {
		log.Printf("Failed to fetch users: %v", err)
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
		log.Printf("Failed to fetch users from Slack Connect: %v", err)
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
		log.Printf("Failed to marshal users for cache: %v", err)
	} else {
		if err := ioutil.WriteFile(ap.usersCache, data, 0644); err != nil {
			log.Printf("Failed to write cache file %q: %v", ap.usersCache, err)
		} else {
			log.Printf("Wrote %d users to cache %q", usersCounter, ap.usersCache)
		}
	}

	log.Printf("Cached %d users into %q", usersCounter, ap.usersCache)
	ap.usersReady = true

	return nil
}

func (ap *ApiProvider) RefreshChannels(ctx context.Context) error {
	if data, err := ioutil.ReadFile(ap.channelsCache); err == nil {
		var cachedChannels []Channel
		if err := json.Unmarshal(data, &cachedChannels); err != nil {
			log.Printf("Failed to unmarshal %+v: %v; will refetch", cachedChannels, err)
		} else {
			for _, c := range cachedChannels {
				ap.channels[c.ID] = c
				ap.channelsInv[c.Name] = c.ID
			}
			log.Printf("Loaded %d channels from cache %q", len(cachedChannels), ap.channelsCache)
			ap.channelsReady = true
			return nil
		}
	}

	channels := ap.GetChannels(ctx, AllChanTypes)

	if data, err := json.MarshalIndent(channels, "", "  "); err != nil {
		log.Printf("Failed to marshal channels for cache: %v", err)
	} else {
		if err := ioutil.WriteFile(ap.channelsCache, data, 0644); err != nil {
			log.Printf("Failed to write cache file %q: %v", ap.channelsCache, err)
		} else {
			log.Printf("Wrote %d channels to cache %q", len(channels), ap.channelsCache)
		}
	}

	log.Printf("Cached %d channels into %q", len(channels), ap.channelsCache)
	ap.channelsReady = true

	return nil
}

func (ap *ApiProvider) GetSlackConnect(ctx context.Context) ([]slack.User, error) {
	boot, err := ap.client.ClientUserBoot(ctx)
	if err != nil {
		log.Printf("Failed to fetch client user boot: %v", err)
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
			log.Printf("Failed to fetch users info for shared IMs: %v", err)
			return nil, err
		}

		for _, u := range *usersInfo {
			res = append(res, u)
		}
	}

	return res, nil
}

func (ap *ApiProvider) GetChannels(ctx context.Context, channelTypes []string) []Channel {
	if len(channelTypes) == 0 {
		channelTypes = AllChanTypes
	}

	params := &slack.GetConversationsParameters{
		Types:           AllChanTypes,
		Limit:           999,
		ExcludeArchived: true,
	}

	var (
		channels []slack.Channel
		chans    []Channel

		nextcur string
		err     error
	)

	lim := limiter.Tier2boost.Limiter()
	for {
		channels, nextcur, err = ap.client.GetConversationsContext(ctx, params)
		if err != nil {
			log.Printf("Failed to fetch channels: %v", err)
			break
		}

		chans = make([]Channel, 0, len(channels))
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

		if err := lim.Wait(ctx); err != nil {
			return nil
		}

		for _, ch := range chans {
			ap.channels[ch.ID] = ch
			ap.channelsInv[ch.Name] = ch.ID
		}

		if nextcur == "" {
			break
		}

		params.Cursor = nextcur
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

func provideHTTPClient(cookies []*http.Cookie) *http.Client {
	var proxy func(*http.Request) (*url.URL, error)
	if proxyURL := os.Getenv("SLACK_MCP_PROXY"); proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			log.Fatalf("Failed to parse proxy URL: %v", err)
		}

		proxy = http.ProxyURL(parsed)
	} else {
		proxy = nil
	}

	rootCAs, _ := x509.SystemCertPool()
	if rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}

	if localCertFile := os.Getenv("SLACK_MCP_SERVER_CA"); localCertFile != "" {
		certs, err := ioutil.ReadFile(localCertFile)
		if err != nil {
			log.Fatalf("Failed to append %q to RootCAs: %v", localCertFile, err)
		}

		if ok := rootCAs.AppendCertsFromPEM(certs); !ok {
			log.Println("No certs appended, using system certs only")
		}
	}

	insecure := false
	if os.Getenv("SLACK_MCP_SERVER_CA_INSECURE") != "" {
		if localCertFile := os.Getenv("SLACK_MCP_SERVER_CA"); localCertFile != "" {
			log.Fatalf("Variable SLACK_MCP_SERVER_CA is at the same time with SLACK_MCP_SERVER_CA_INSECURE")
		}
		insecure = true
	}

	customHTTPTransport := &http.Transport{
		Proxy: proxy,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecure,
			RootCAs:            rootCAs,
		},
	}

	userAgent := defaultUA
	if os.Getenv("SLACK_MCP_USER_AGENT") != "" {
		userAgent = os.Getenv("SLACK_MCP_USER_AGENT")
	}

	client := &http.Client{
		Transport: transport.New(
			customHTTPTransport,
			userAgent,
			cookies,
		),
	}

	return client
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

	if isIM {
		finalMemberCount = 2
		if u, ok := usersMap[user]; ok {
			channelName = "@" + u.Name
			finalPurpose = "DM with " + u.RealName
		} else {
			channelName = "@" + user
			finalPurpose = "DM with " + user
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
	}
}
