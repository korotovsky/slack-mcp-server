package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/server/auth"
	"github.com/korotovsky/slack-mcp-server/pkg/text"
	"github.com/mark3labs/mcp-go/mcp"
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

type ChannelMember struct {
	UserID   string `json:"userID"`
	UserName string `json:"userName"`
	RealName string `json:"realName"`
	IsBot    bool   `json:"isBot"`
	Cursor   string `json:"cursor"`
}

type ChannelsHandler struct {
	apiProvider *provider.ApiProvider
	validTypes  map[string]bool
	logger      *zap.Logger
}

func NewChannelsHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *ChannelsHandler {
	validTypes := make(map[string]bool, len(provider.AllChanTypes))
	for _, v := range provider.AllChanTypes {
		validTypes[v] = true
	}

	return &ChannelsHandler{
		apiProvider: apiProvider,
		validTypes:  validTypes,
		logger:      logger,
	}
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

// ChannelMembersHandler handles the channel_members_list tool
func (ch *ChannelsHandler) ChannelMembersHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ChannelMembersHandler called")

	// Skip cache readiness check for debugging - just check if we can get basic API access
	// if ready, err := ch.apiProvider.IsReady(); !ready {
	//	ch.logger.Error("API provider not ready", zap.Error(err))
	//	return nil, err
	// }

	channelIDOrName := request.GetString("channel_id", "")
	if channelIDOrName == "" {
		ch.logger.Error("channel_id missing in channel members request")
		return nil, errors.New("channel_id must be a string")
	}

	includeBots := request.GetBool("include_bots", false)
	limit := request.GetInt("limit", 100)
	cursor := request.GetString("cursor", "")

	ch.logger.Debug("Request parameters",
		zap.String("channel_id", channelIDOrName),
		zap.Bool("include_bots", includeBots),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)

	// Resolve channel name to ID if needed
	channelID, err := resolveChannelID(channelIDOrName, ch.apiProvider, ch.logger)
	if err != nil {
		ch.logger.Error("Failed to resolve channel ID", zap.String("channel", channelIDOrName), zap.Error(err))
		return nil, err
	}

	// Validate limit
	if limit < 1 || limit > 1000 {
		ch.logger.Warn("Invalid limit, using default", zap.Int("requested", limit))
		limit = 100
	}

	// Get channel members from Slack API
	ch.logger.Info("Calling GetUsersInConversationContext", zap.String("channel_id", channelID))
	memberIDs, err := ch.apiProvider.Slack().GetUsersInConversationContext(ctx, channelID)
	if err != nil {
		ch.logger.Error("Failed to get channel members", zap.String("channel_id", channelID), zap.Error(err))
		return nil, fmt.Errorf("failed to get members for channel %q: %v", channelIDOrName, err)
	}

	ch.logger.Info("Retrieved channel members",
		zap.String("channel_id", channelID),
		zap.Int("count", len(memberIDs)),
		zap.Strings("member_ids", memberIDs))

	// Enrich user information and filter bots if needed
	members, err := ch.enrichUserInfo(memberIDs, includeBots)
	if err != nil {
		ch.logger.Error("Failed to enrich user info", zap.Error(err))
		return nil, err
	}

	// Apply pagination
	pagedMembers, nextCursor := paginateMembers(members, cursor, limit)

	ch.logger.Debug("Pagination results",
		zap.Int("total_members", len(members)),
		zap.Int("returned_count", len(pagedMembers)),
		zap.Bool("has_next_page", nextCursor != ""),
	)

	// Add cursor to the last member if there's a next page
	if len(pagedMembers) > 0 && nextCursor != "" {
		pagedMembers[len(pagedMembers)-1].Cursor = nextCursor
	}

	// Convert to CSV
	csvBytes, err := gocsv.MarshalBytes(&pagedMembers)
	if err != nil {
		ch.logger.Error("Failed to marshal members to CSV", zap.Error(err))
		return nil, err
	}

	return mcp.NewToolResultText(string(csvBytes)), nil
}

// enrichUserInfo enriches user IDs with user information from cache
func (ch *ChannelsHandler) enrichUserInfo(userIDs []string, includeBots bool) ([]ChannelMember, error) {
	ch.logger.Info("Enriching user info",
		zap.Int("input_user_count", len(userIDs)),
		zap.Bool("include_bots", includeBots))

	usersMaps := ch.apiProvider.ProvideUsersMap()
	ch.logger.Info("User cache info", zap.Int("total_users_in_cache", len(usersMaps.Users)))

	var members []ChannelMember

	for _, userID := range userIDs {
		user, exists := usersMaps.Users[userID]
		if !exists {
			ch.logger.Warn("User not found in cache", zap.String("user_id", userID))
			// For debugging, include the user with the ID as username
			members = append(members, ChannelMember{
				UserID:   userID,
				UserName: userID, // Use ID as username if cache is not available
				RealName: userID,
				IsBot:    false,
			})
			continue
		}

		// Filter bots if not requested
		if user.IsBot && !includeBots {
			ch.logger.Debug("Skipping bot user", zap.String("user_id", userID), zap.String("name", user.Name))
			continue
		}

		members = append(members, ChannelMember{
			UserID:   user.ID,
			UserName: user.Name,
			RealName: user.RealName,
			IsBot:    user.IsBot,
		})
	}

	ch.logger.Debug("User enrichment complete",
		zap.Int("input_users", len(userIDs)),
		zap.Int("output_members", len(members)),
		zap.Bool("include_bots", includeBots),
	)

	return members, nil
}

// paginateMembers applies pagination to the members list
func paginateMembers(members []ChannelMember, cursor string, limit int) ([]ChannelMember, string) {
	logger := zap.L()

	// Sort members by UserID for consistent pagination
	sort.Slice(members, func(i, j int) bool {
		return members[i].UserID < members[j].UserID
	})

	startIndex := 0
	if cursor != "" {
		if decoded, err := base64.StdEncoding.DecodeString(cursor); err == nil {
			lastUserID := string(decoded)
			for i, member := range members {
				if member.UserID > lastUserID {
					startIndex = i
					break
				}
			}
			logger.Debug("Decoded cursor",
				zap.String("cursor", cursor),
				zap.String("decoded_user_id", lastUserID),
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
	if endIndex > len(members) {
		endIndex = len(members)
	}

	paged := members[startIndex:endIndex]

	var nextCursor string
	if endIndex < len(members) {
		nextCursor = base64.StdEncoding.EncodeToString([]byte(members[endIndex-1].UserID))
		logger.Debug("Generated next cursor",
			zap.String("last_user_id", members[endIndex-1].UserID),
			zap.String("next_cursor", nextCursor),
		)
	}

	logger.Debug("Member pagination complete",
		zap.Int("total_members", len(members)),
		zap.Int("start_index", startIndex),
		zap.Int("end_index", endIndex),
		zap.Int("page_size", len(paged)),
		zap.Bool("has_more", nextCursor != ""),
	)

	return paged, nextCursor
}
