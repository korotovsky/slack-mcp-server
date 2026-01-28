package handler

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/oauth"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/server/auth"
	"github.com/korotovsky/slack-mcp-server/pkg/text"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

type Channel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Topic       string `json:"topic"`
	Purpose     string `json:"purpose"`
	MemberCount int    `json:"memberCount"`
	Cursor      string `json:"cursor"`
}

type ChannelsHandler struct {
	apiProvider  *provider.ApiProvider // Legacy mode
	tokenStorage oauth.TokenStorage    // OAuth mode
	oauthEnabled bool
	validTypes   map[string]bool
	logger       *zap.Logger
}

// NewChannelsHandler creates handler for legacy mode
func NewChannelsHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *ChannelsHandler {
	validTypes := make(map[string]bool, len(provider.AllChanTypes))
	for _, v := range provider.AllChanTypes {
		validTypes[v] = true
	}

	return &ChannelsHandler{
		apiProvider:  apiProvider,
		oauthEnabled: false,
		validTypes:   validTypes,
		logger:       logger,
	}
}

// NewChannelsHandlerWithOAuth creates handler for OAuth mode
func NewChannelsHandlerWithOAuth(tokenStorage oauth.TokenStorage, logger *zap.Logger) *ChannelsHandler {
	validTypes := make(map[string]bool, len(provider.AllChanTypes))
	for _, v := range provider.AllChanTypes {
		validTypes[v] = true
	}

	return &ChannelsHandler{
		tokenStorage: tokenStorage,
		oauthEnabled: true,
		validTypes:   validTypes,
		logger:       logger,
	}
}

// getSlackClient creates a Slack client for the current request (OAuth mode)
func (ch *ChannelsHandler) getSlackClient(ctx context.Context) (*slack.Client, error) {
	if !ch.oauthEnabled {
		return nil, fmt.Errorf("OAuth not enabled")
	}

	userCtx, ok := auth.FromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("user context not found")
	}

	// Use token directly from context (already validated by middleware)
	// Set API URL from auth.test response to support external tokens and GovSlack
	opts := []slack.Option{}
	if userCtx.URL != "" {
		opts = append(opts, slack.OptionAPIURL(userCtx.URL+"api/"))
	}
	return slack.New(userCtx.AccessToken, opts...), nil
}

func (ch *ChannelsHandler) ChannelsResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	ch.logger.Debug("ChannelsResource called", zap.Any("params", request.Params))

	// mark3labs/mcp-go does not support middlewares for resources.
	if authenticated, err := auth.IsAuthenticated(ctx, ch.apiProvider.ServerTransport(), ch.logger); !authenticated {
		ch.logger.Error("Authentication failed for channels resource", zap.Error(err))
		return nil, err
	}

	var channelList []Channel

	if ready, err := ch.apiProvider.IsReady(); !ready {
		ch.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	ar, err := ch.apiProvider.Slack().AuthTest()
	if err != nil {
		ch.logger.Error("Auth test failed", zap.Error(err))
		return nil, err
	}

	ws, err := text.Workspace(ar.URL)
	if err != nil {
		ch.logger.Error("Failed to parse workspace from URL",
			zap.String("url", ar.URL),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to parse workspace from URL: %v", err)
	}

	channels := ch.apiProvider.ProvideChannelsMaps().Channels
	ch.logger.Debug("Retrieved channels from provider", zap.Int("count", len(channels)))

	for _, channel := range channels {
		channelList = append(channelList, Channel{
			ID:          channel.ID,
			Name:        channel.Name,
			Topic:       channel.Topic,
			Purpose:     channel.Purpose,
			MemberCount: channel.MemberCount,
		})
	}

	csvBytes, err := gocsv.MarshalBytes(&channelList)
	if err != nil {
		ch.logger.Error("Failed to marshal channels to CSV", zap.Error(err))
		return nil, err
	}

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      "slack://" + ws + "/channels",
			MIMEType: "text/csv",
			Text:     string(csvBytes),
		},
	}, nil
}

func (ch *ChannelsHandler) ChannelsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ChannelsHandler called")

	// In OAuth mode, we don't have apiProvider - fetch directly
	if ch.oauthEnabled {
		return ch.channelsHandlerOAuth(ctx, request)
	}

	if ready, err := ch.apiProvider.IsReady(); !ready {
		ch.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	sortType := request.GetString("sort", "popularity")
	types := request.GetString("channel_types", provider.PubChanType)
	cursor := request.GetString("cursor", "")
	limit := request.GetInt("limit", 0)

	ch.logger.Debug("Request parameters",
		zap.String("sort", sortType),
		zap.String("channel_types", types),
		zap.String("cursor", cursor),
		zap.Int("limit", limit),
	)

	// MCP Inspector v0.14.0 has issues with Slice type
	// introspection, so some type simplification makes sense here
	channelTypes := []string{}
	for _, t := range strings.Split(types, ",") {
		t = strings.TrimSpace(t)
		if ch.validTypes[t] {
			channelTypes = append(channelTypes, t)
		} else if t != "" {
			ch.logger.Warn("Invalid channel type ignored", zap.String("type", t))
		}
	}

	if len(channelTypes) == 0 {
		ch.logger.Debug("No valid channel types provided, using defaults")
		channelTypes = append(channelTypes, provider.PubChanType)
		channelTypes = append(channelTypes, provider.PrivateChanType)
	}

	ch.logger.Debug("Validated channel types", zap.Strings("types", channelTypes))

	if limit == 0 {
		limit = 100
		ch.logger.Debug("Limit not provided, using default", zap.Int("limit", limit))
	}
	if limit > 999 {
		ch.logger.Warn("Limit exceeds maximum, capping to 999", zap.Int("requested", limit))
		limit = 999
	}

	var (
		nextcur     string
		channelList []Channel
	)

	allChannels := ch.apiProvider.ProvideChannelsMaps().Channels
	ch.logger.Debug("Total channels available", zap.Int("count", len(allChannels)))

	channels := filterChannelsByTypes(allChannels, channelTypes)
	ch.logger.Debug("Channels after filtering by type", zap.Int("count", len(channels)))

	var chans []provider.Channel

	chans, nextcur = paginateChannels(
		channels,
		cursor,
		limit,
	)

	ch.logger.Debug("Pagination results",
		zap.Int("returned_count", len(chans)),
		zap.Bool("has_next_page", nextcur != ""),
	)

	for _, channel := range chans {
		channelList = append(channelList, Channel{
			ID:          channel.ID,
			Name:        channel.Name,
			Topic:       channel.Topic,
			Purpose:     channel.Purpose,
			MemberCount: channel.MemberCount,
		})
	}

	switch sortType {
	case "popularity":
		ch.logger.Debug("Sorting channels by popularity (member count)")
		sort.Slice(channelList, func(i, j int) bool {
			return channelList[i].MemberCount > channelList[j].MemberCount
		})
	default:
		ch.logger.Debug("No sorting applied", zap.String("sort_type", sortType))
	}

	if len(channelList) > 0 && nextcur != "" {
		channelList[len(channelList)-1].Cursor = nextcur
		ch.logger.Debug("Added cursor to last channel", zap.String("cursor", nextcur))
	}

	csvBytes, err := gocsv.MarshalBytes(&channelList)
	if err != nil {
		ch.logger.Error("Failed to marshal channels to CSV", zap.Error(err))
		return nil, err
	}

	return mcp.NewToolResultText(string(csvBytes)), nil
}

func filterChannelsByTypes(channels map[string]provider.Channel, types []string) []provider.Channel {
	logger := zap.L()

	var result []provider.Channel
	typeSet := make(map[string]bool)

	for _, t := range types {
		typeSet[t] = true
	}

	publicCount := 0
	privateCount := 0
	imCount := 0
	mpimCount := 0

	for _, ch := range channels {
		if typeSet["public_channel"] && !ch.IsPrivate && !ch.IsIM && !ch.IsMpIM {
			result = append(result, ch)
			publicCount++
		}
		if typeSet["private_channel"] && ch.IsPrivate && !ch.IsIM && !ch.IsMpIM {
			result = append(result, ch)
			privateCount++
		}
		if typeSet["im"] && ch.IsIM {
			result = append(result, ch)
			imCount++
		}
		if typeSet["mpim"] && ch.IsMpIM {
			result = append(result, ch)
			mpimCount++
		}
	}

	logger.Debug("Channel filtering complete",
		zap.Int("total_input", len(channels)),
		zap.Int("total_output", len(result)),
		zap.Int("public_channels", publicCount),
		zap.Int("private_channels", privateCount),
		zap.Int("ims", imCount),
		zap.Int("mpims", mpimCount),
	)

	return result
}

func paginateChannels(channels []provider.Channel, cursor string, limit int) ([]provider.Channel, string) {
	logger := zap.L()

	sort.Slice(channels, func(i, j int) bool {
		return channels[i].ID < channels[j].ID
	})

	startIndex := 0
	if cursor != "" {
		if decoded, err := base64.StdEncoding.DecodeString(cursor); err == nil {
			lastID := string(decoded)
			for i, ch := range channels {
				if ch.ID > lastID {
					startIndex = i
					break
				}
			}
			logger.Debug("Decoded cursor",
				zap.String("cursor", cursor),
				zap.String("decoded_id", lastID),
				zap.Int("start_index", startIndex),
			)
		} else {
			logger.Warn("Failed to decode cursor",
				zap.String("cursor", cursor),
				zap.Error(err),
			)
		}
	}

	endIndex := startIndex + limit
	if endIndex > len(channels) {
		endIndex = len(channels)
	}

	paged := channels[startIndex:endIndex]

	var nextCursor string
	if endIndex < len(channels) {
		nextCursor = base64.StdEncoding.EncodeToString([]byte(channels[endIndex-1].ID))
		logger.Debug("Generated next cursor",
			zap.String("last_id", channels[endIndex-1].ID),
			zap.String("next_cursor", nextCursor),
		)
	}

	logger.Debug("Pagination complete",
		zap.Int("total_channels", len(channels)),
		zap.Int("start_index", startIndex),
		zap.Int("end_index", endIndex),
		zap.Int("page_size", len(paged)),
		zap.Bool("has_more", nextCursor != ""),
	)

	return paged, nextCursor
}

// channelsHandlerOAuth handles channel listing in OAuth mode
func (ch *ChannelsHandler) channelsHandlerOAuth(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Get Slack client for this user
	client, err := ch.getSlackClient(ctx)
	if err != nil {
		ch.logger.Error("Failed to get Slack client", zap.Error(err))
		return nil, fmt.Errorf("authentication error: %w", err)
	}

	types := request.GetString("channel_types", "public_channel")
	limit := request.GetInt("limit", 100)

	ch.logger.Debug("OAuth mode: fetching channels",
		zap.String("types", types),
		zap.Int("limit", limit),
	)

	// Parse channel types
	channelTypes := []string{}
	for _, t := range strings.Split(types, ",") {
		t = strings.TrimSpace(t)
		if ch.validTypes[t] {
			channelTypes = append(channelTypes, t)
		}
	}

	if len(channelTypes) == 0 {
		channelTypes = []string{"public_channel", "private_channel"}
	}

	// Fetch channels from Slack API
	var allChannels []Channel
	for _, chanType := range channelTypes {
		params := &slack.GetConversationsParameters{
			Types:           []string{chanType},
			Limit:           limit,
			ExcludeArchived: true,
		}

		channels, _, err := client.GetConversations(params)
		if err != nil {
			ch.logger.Error("Failed to get conversations", zap.Error(err))
			return nil, fmt.Errorf("failed to get channels: %w", err)
		}

		for _, c := range channels {
			name := "#" + c.Name
			memberCount := c.NumMembers

			// Handle DM channels - they have empty names but have a User field
			if c.IsIM && c.User != "" {
				// Fetch user info to get their display name
				user, err := client.GetUserInfo(c.User)
				if err != nil {
					ch.logger.Debug("Failed to get user info for DM", zap.String("userID", c.User), zap.Error(err))
					name = "@" + c.User // Fallback to user ID
				} else if user != nil {
					// Priority: RealName (full name) → DisplayName → Name (username)
					// This provides more consistent formatting across users
					if user.RealName != "" {
						name = "@" + user.RealName
					} else if user.Profile.DisplayName != "" {
						name = "@" + user.Profile.DisplayName
					} else {
						name = "@" + user.Name
					}
				}
				memberCount = 2 // 1:1 DMs always have exactly 2 members
			} else if c.IsMpIM {
				// Group DMs - use the purpose or a placeholder
				if c.Purpose.Value != "" {
					name = c.Purpose.Value
				} else {
					name = "Group DM"
				}
				// Get actual member count for group DMs
				members, _, err := client.GetUsersInConversation(&slack.GetUsersInConversationParameters{
					ChannelID: c.ID,
					Limit:     1000,
				})
				if err != nil {
					ch.logger.Debug("Failed to get members for MPIM", zap.String("channelID", c.ID), zap.Error(err))
				} else {
					memberCount = len(members)
				}
			}

			allChannels = append(allChannels, Channel{
				ID:          c.ID,
				Name:        name,
				Topic:       c.Topic.Value,
				Purpose:     c.Purpose.Value,
				MemberCount: memberCount,
			})
		}
	}

	// Sort by popularity if requested
	sortType := request.GetString("sort", "")
	if sortType == "popularity" {
		sort.Slice(allChannels, func(i, j int) bool {
			return allChannels[i].MemberCount > allChannels[j].MemberCount
		})
	}

	// Marshal to CSV
	csvBytes, err := gocsv.MarshalBytes(&allChannels)
	if err != nil {
		ch.logger.Error("Failed to marshal to CSV", zap.Error(err))
		return nil, err
	}

	ch.logger.Debug("Returning channels", zap.Int("count", len(allChannels)))
	return mcp.NewToolResultText(string(csvBytes)), nil
}
