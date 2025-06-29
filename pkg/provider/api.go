package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/limiter"
	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"github.com/korotovsky/slack-mcp-server/pkg/transport"
	slack2 "github.com/rusq/slack"
	"github.com/rusq/slackdump/v3/auth"
	"github.com/slack-go/slack"
)

var defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36"
var AllChanTypes = []string{"mpim", "im", "public_channel", "private_channel"}
var PubChanType = "public_channel"

type ChannelsCache struct {
	Channels    map[string]Channel `json:"channels"`
	ChannelsInv map[string]string  `json:"channels_inv"`
}

type ApiProvider struct {
	boot func(ap *ApiProvider) *slack.Client

	authProvider *auth.ValueAuth
	authResponse *slack2.AuthTestResponse

	clientGeneric    *slack.Client
	clientEnterprise *edge.Client

	users      map[string]slack.User
	usersCache string

	channels      map[string]Channel
	channelsInv   map[string]string
	channelsCache string
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

func New() *ApiProvider {
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

		return newWithXOXP(authProvider)
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

	return newWithXOXC(authProvider)
}

func newWithXOXP(authProvider auth.ValueAuth) *ApiProvider {
	usersCache := os.Getenv("SLACK_MCP_USERS_CACHE")
	if usersCache == "" {
		usersCache = ".users_cache.json"
	}

	channelsCache := os.Getenv("SLACK_MCP_CHANNELS_CACHE")
	if channelsCache == "" {
		channelsCache = ".channels_cache.json"
	}

	return &ApiProvider{
		boot: func(ap *ApiProvider) *slack.Client {
			api := slack.New(authProvider.SlackToken())
			res, err := api.AuthTest()
			if err != nil {
				panic(err)
			} else {
				ap.authResponse = &slack2.AuthTestResponse{
					URL:          res.URL,
					Team:         res.Team,
					User:         res.User,
					TeamID:       res.TeamID,
					UserID:       res.UserID,
					EnterpriseID: res.EnterpriseID,
					BotID:        res.BotID,
				}
				log.Printf("Authenticated as: %s\n", res)
			}

			return api
		},

		users:      make(map[string]slack.User),
		usersCache: usersCache,

		channels:      make(map[string]Channel),
		channelsInv:   map[string]string{},
		channelsCache: channelsCache,
	}
}

func newWithXOXC(authProvider auth.ValueAuth) *ApiProvider {
	usersCache := os.Getenv("SLACK_MCP_USERS_CACHE")
	if usersCache == "" {
		usersCache = ".users_cache.json"
	}

	channelsCache := os.Getenv("SLACK_MCP_CHANNELS_CACHE")
	if channelsCache == "" {
		channelsCache = ".channels_cache.json"
	}

	return &ApiProvider{
		boot: func(ap *ApiProvider) *slack.Client {
			api := slack.New(authProvider.SlackToken(),
				withHTTPClientOption(authProvider.Cookies()),
			)
			res, err := api.AuthTest()
			if err != nil {
				panic(err)
			} else {
				ap.authProvider = &authProvider
				ap.authResponse = &slack2.AuthTestResponse{
					URL:          res.URL,
					Team:         res.Team,
					User:         res.User,
					TeamID:       res.TeamID,
					UserID:       res.UserID,
					EnterpriseID: res.EnterpriseID,
					BotID:        res.BotID,
				}
				log.Printf("Authenticated as: %s\n", res)
			}

			api = slack.New(authProvider.SlackToken(),
				withHTTPClientOption(authProvider.Cookies()),
				withTeamEndpointOption(res.URL),
			)

			return api
		},

		users:      make(map[string]slack.User),
		usersCache: usersCache,

		channels:      make(map[string]Channel),
		channelsInv:   map[string]string{},
		channelsCache: channelsCache,
	}
}

func (ap *ApiProvider) ProvideGeneric() (*slack.Client, error) {
	if ap.clientGeneric == nil {
		ap.clientGeneric = ap.boot(ap)
	}

	return ap.clientGeneric, nil
}

func (ap *ApiProvider) ProvideEnterprise() (*edge.Client, error) {
	if ap.clientEnterprise == nil {
		ap.clientEnterprise, _ = edge.NewWithInfo(ap.authResponse, ap.authProvider,
			withHTTPClientEdgeOption(ap.authProvider.Cookies()),
		)
	}

	return ap.clientEnterprise, nil
}

func (ap *ApiProvider) RefreshUsers(ctx context.Context) error {
	if data, err := ioutil.ReadFile(ap.usersCache); err == nil {
		var cachedUsers []slack.User
		if err := json.Unmarshal(data, &cachedUsers); err != nil {
			log.Printf("Failed to unmarshal %s: %v; will refetch", ap.usersCache, err)
		} else {
			for _, u := range cachedUsers {
				ap.users[u.ID] = u
			}
			log.Printf("Loaded %d users from cache %q", len(cachedUsers), ap.usersCache)
			return nil
		}
	}

	optionLimit := slack.GetUsersOptionLimit(1000)

	client, err := ap.ProvideGeneric()
	if err != nil {
		return err
	}

	users, err := client.GetUsersContext(ctx,
		optionLimit,
	)
	if err != nil {
		log.Printf("Failed to fetch users: %v", err)
		return err
	}

	for _, user := range users {
		ap.users[user.ID] = user
	}

	if data, err := json.MarshalIndent(users, "", "  "); err != nil {
		log.Printf("Failed to marshal users for cache: %v", err)
	} else {
		if err := ioutil.WriteFile(ap.usersCache, data, 0644); err != nil {
			log.Printf("Failed to write cache file %q: %v", ap.usersCache, err)
		} else {
			log.Printf("Wrote %d users to cache %q", len(users), ap.usersCache)
		}
	}

	return nil
}

func (ap *ApiProvider) RefreshChannels(ctx context.Context) error {
	if data, err := ioutil.ReadFile(ap.channelsCache); err == nil {
		var cachedChannels []Channel
		if err := json.Unmarshal(data, &cachedChannels); err != nil {
			log.Printf("Failed to unmarshal %s: %v; will refetch", cachedChannels, err)
		} else {
			for _, c := range cachedChannels {
				ap.channels[c.ID] = c
			}
			log.Printf("Loaded %d channels from cache %q", len(cachedChannels), ap.channelsCache)
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

	return nil
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
		chans1 []slack.Channel
		chans2 []slack2.Channel
		chans  []Channel

		nextcur string
	)

	clientGeneric, err := ap.ProvideGeneric()
	if err != nil {
		return nil
	}

	clientE, err := ap.ProvideEnterprise()
	if err != nil {
		return nil
	}

	lim := limiter.Tier2boost.Limiter()
	for {
		if ap.authResponse.EnterpriseID == "" {
			chans1, nextcur, err = clientGeneric.GetConversationsContext(ctx, params)
			if err != nil {
				break
			}
			for _, channel := range chans1 {
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
					ap.ProvideUsersMap(),
				)
				chans = append(chans, ch)
			}
			if err := lim.Wait(ctx); err != nil {
				return nil
			}
		} else {
			chans2, _, err = clientE.GetConversationsContext(ctx, nil)
			if err != nil {
				break
			}
			for _, channel := range chans2 {
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
					ap.ProvideUsersMap(),
				)
				chans = append(chans, ch)
			}
			if err := lim.Wait(ctx); err != nil {
				return nil
			}
		}

		for _, ch := range chans {
			ap.channels[ch.ID] = ch
			ap.channelsInv["#"+ch.Name] = ch.ID
		}

		if nextcur == "" {
			log.Printf("channels fetch exhausted")
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

func (ap *ApiProvider) ProvideUsersMap() map[string]slack.User {
	return ap.users
}

func (ap *ApiProvider) ProvideChannelsMaps() *ChannelsCache {
	return &ChannelsCache{
		Channels:    ap.channels,
		ChannelsInv: ap.channelsInv,
	}
}

func withHTTPClientOption(cookies []*http.Cookie) func(c *slack.Client) {
	return func(c *slack.Client) {
		slack.OptionHTTPClient(provideHTTPClient(cookies))(c)
	}
}

func withHTTPClientEdgeOption(cookies []*http.Cookie) func(c *edge.Client) {
	return func(c *edge.Client) {
		edge.OptionHTTPClient(provideHTTPClient(cookies))(c)
	}
}

func withTeamEndpointOption(url string) slack.Option {
	return func(c *slack.Client) {
		slack.OptionAPIURL(url + "api/")(c)
	}
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
	channelName := "#" + name
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
