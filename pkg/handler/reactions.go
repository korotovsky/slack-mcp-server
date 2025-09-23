package handler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/server/auth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

type ReactionResponse struct {
	Channel   string `csv:"channel"`
	Timestamp string `csv:"timestamp"`
	Emoji     string `csv:"emoji"`
	Success   bool   `csv:"success"`
	Message   string `csv:"message"`
}

type MessageReactions struct {
	Channel   string `csv:"channel"`
	Timestamp string `csv:"timestamp"`
	Reactions string `csv:"reactions"`
}

type UserReaction struct {
	Channel   string `csv:"channel"`
	Timestamp string `csv:"timestamp"`
	Emoji     string `csv:"emoji"`
	Type      string `csv:"type"`
	Cursor    string `csv:"cursor"`
}

type addReactionParams struct {
	channel   string
	timestamp string
	emoji     string
}

type removeReactionParams struct {
	channel   string
	timestamp string
	emoji     string
}

type getReactionsParams struct {
	channel   string
	timestamp string
}

type listReactionsParams struct {
	user   string
	limit  int
	cursor string
}

type ReactionsHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
}

func NewReactionsHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *ReactionsHandler {
	return &ReactionsHandler{
		apiProvider: apiProvider,
		logger:      logger,
	}
}

func (rh *ReactionsHandler) ReactionsAddHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rh.logger.Debug("ReactionsAddHandler called", zap.Any("params", request.Params))

	if authenticated, err := auth.IsAuthenticated(ctx, rh.apiProvider.ServerTransport(), rh.logger); !authenticated {
		rh.logger.Error("Authentication failed for reactions add", zap.Error(err))
		return nil, err
	}

	if ready, err := rh.apiProvider.IsReady(); !ready {
		rh.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	params, err := rh.parseAddReactionParams(request)
	if err != nil {
		rh.logger.Error("Failed to parse add reaction params", zap.Error(err))
		return nil, err
	}

	err = rh.apiProvider.Slack().AddReactionContext(ctx, params.emoji, slack.ItemRef{
		Channel:   params.channel,
		Timestamp: params.timestamp,
	})

	response := ReactionResponse{
		Channel:   params.channel,
		Timestamp: params.timestamp,
		Emoji:     params.emoji,
		Success:   err == nil,
	}

	if err != nil {
		response.Message = err.Error()
		rh.logger.Error("Failed to add reaction", zap.Error(err))
	} else {
		response.Message = "Reaction added successfully"
		rh.logger.Debug("Reaction added successfully",
			zap.String("channel", params.channel),
			zap.String("timestamp", params.timestamp),
			zap.String("emoji", params.emoji),
		)
	}

	return marshalReactionResponseToCSV([]ReactionResponse{response})
}

func (rh *ReactionsHandler) ReactionsRemoveHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rh.logger.Debug("ReactionsRemoveHandler called", zap.Any("params", request.Params))

	if authenticated, err := auth.IsAuthenticated(ctx, rh.apiProvider.ServerTransport(), rh.logger); !authenticated {
		rh.logger.Error("Authentication failed for reactions remove", zap.Error(err))
		return nil, err
	}

	if ready, err := rh.apiProvider.IsReady(); !ready {
		rh.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	params, err := rh.parseRemoveReactionParams(request)
	if err != nil {
		rh.logger.Error("Failed to parse remove reaction params", zap.Error(err))
		return nil, err
	}

	err = rh.apiProvider.Slack().RemoveReactionContext(ctx, params.emoji, slack.ItemRef{
		Channel:   params.channel,
		Timestamp: params.timestamp,
	})

	response := ReactionResponse{
		Channel:   params.channel,
		Timestamp: params.timestamp,
		Emoji:     params.emoji,
		Success:   err == nil,
	}

	if err != nil {
		response.Message = err.Error()
		rh.logger.Error("Failed to remove reaction", zap.Error(err))
	} else {
		response.Message = "Reaction removed successfully"
		rh.logger.Debug("Reaction removed successfully",
			zap.String("channel", params.channel),
			zap.String("timestamp", params.timestamp),
			zap.String("emoji", params.emoji),
		)
	}

	return marshalReactionResponseToCSV([]ReactionResponse{response})
}

func (rh *ReactionsHandler) ReactionsGetHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rh.logger.Debug("ReactionsGetHandler called", zap.Any("params", request.Params))

	if authenticated, err := auth.IsAuthenticated(ctx, rh.apiProvider.ServerTransport(), rh.logger); !authenticated {
		rh.logger.Error("Authentication failed for reactions get", zap.Error(err))
		return nil, err
	}

	if ready, err := rh.apiProvider.IsReady(); !ready {
		rh.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	params, err := rh.parseGetReactionsParams(request)
	if err != nil {
		rh.logger.Error("Failed to parse get reactions params", zap.Error(err))
		return nil, err
	}

	reactions, err := rh.apiProvider.Slack().GetReactionsContext(ctx, slack.ItemRef{
		Channel:   params.channel,
		Timestamp: params.timestamp,
	}, slack.NewGetReactionsParameters())

	if err != nil {
		rh.logger.Error("Failed to get reactions", zap.Error(err))
		return nil, err
	}

	var reactionStrings []string
	for _, r := range reactions {
		reactionStrings = append(reactionStrings, fmt.Sprintf("%s:%d", r.Name, r.Count))
	}

	messageReactions := MessageReactions{
		Channel:   params.channel,
		Timestamp: params.timestamp,
		Reactions: strings.Join(reactionStrings, "|"),
	}

	return marshalMessageReactionsToCSV([]MessageReactions{messageReactions})
}

func (rh *ReactionsHandler) ReactionsListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rh.logger.Debug("ReactionsListHandler called", zap.Any("params", request.Params))

	if authenticated, err := auth.IsAuthenticated(ctx, rh.apiProvider.ServerTransport(), rh.logger); !authenticated {
		rh.logger.Error("Authentication failed for reactions list", zap.Error(err))
		return nil, err
	}

	if ready, err := rh.apiProvider.IsReady(); !ready {
		rh.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	params, err := rh.parseListReactionsParams(request)
	if err != nil {
		rh.logger.Error("Failed to parse list reactions params", zap.Error(err))
		return nil, err
	}

	listParams := slack.ListReactionsParameters{
		User:  params.user,
		Count: params.limit,
		Page:  1,
	}

	if params.cursor != "" {
		if page, err := strconv.Atoi(params.cursor); err == nil && page > 0 {
			listParams.Page = page
		}
	}

	reactions, paging, err := rh.apiProvider.Slack().ListReactionsContext(ctx, listParams)
	if err != nil {
		rh.logger.Error("Failed to list reactions", zap.Error(err))
		return nil, err
	}

	var userReactions []UserReaction
	for _, item := range reactions {
		itemType := "message"
		if item.File != nil {
			itemType = "file"
		}

		for _, reaction := range item.Reactions {
			for _, user := range reaction.Users {
				if user == params.user {
					userReactions = append(userReactions, UserReaction{
						Channel:   item.Channel,
						Timestamp: item.Timestamp,
						Emoji:     reaction.Name,
						Type:      itemType,
					})
					break
				}
			}
		}
	}

	if len(userReactions) > 0 && paging.Page < paging.Pages {
		userReactions[len(userReactions)-1].Cursor = strconv.Itoa(paging.Page + 1)
	}

	return marshalUserReactionsToCSV(userReactions)
}

func (rh *ReactionsHandler) parseAddReactionParams(request mcp.CallToolRequest) (*addReactionParams, error) {
	return rh.parseReactionParams(request)
}

func (rh *ReactionsHandler) parseRemoveReactionParams(request mcp.CallToolRequest) (*removeReactionParams, error) {
	params, err := rh.parseReactionParams(request)
	if err != nil {
		return nil, err
	}
	return &removeReactionParams{
		channel:   params.channel,
		timestamp: params.timestamp,
		emoji:     params.emoji,
	}, nil
}

func (rh *ReactionsHandler) parseReactionParams(request mcp.CallToolRequest) (*addReactionParams, error) {
	channel := request.GetString("channel_id", "")
	if channel == "" {
		return nil, errors.New("channel_id must be a string")
	}

	timestamp := request.GetString("timestamp", "")
	if timestamp == "" {
		return nil, errors.New("timestamp must be a string")
	}

	emoji := request.GetString("emoji", "")
	if emoji == "" {
		return nil, errors.New("emoji must be a string")
	}

	if strings.HasPrefix(channel, "#") || strings.HasPrefix(channel, "@") {
		if ready, err := rh.apiProvider.IsReady(); !ready {
			rh.logger.Warn("Provider not ready for channel resolution", zap.Error(err))
			return nil, fmt.Errorf("channel %q not found in cache", channel)
		}
		channelsMaps := rh.apiProvider.ProvideChannelsMaps()
		chn, ok := channelsMaps.ChannelsInv[channel]
		if !ok {
			return nil, fmt.Errorf("channel %q not found", channel)
		}
		channel = channelsMaps.Channels[chn].ID
	}

	if !strings.Contains(timestamp, ".") {
		return nil, errors.New("timestamp must be in format 1234567890.123456")
	}

	emoji = strings.Trim(emoji, ":")

	return &addReactionParams{
		channel:   channel,
		timestamp: timestamp,
		emoji:     emoji,
	}, nil
}

func (rh *ReactionsHandler) parseGetReactionsParams(request mcp.CallToolRequest) (*getReactionsParams, error) {
	channel := request.GetString("channel_id", "")
	if channel == "" {
		return nil, errors.New("channel_id must be a string")
	}

	timestamp := request.GetString("timestamp", "")
	if timestamp == "" {
		return nil, errors.New("timestamp must be a string")
	}

	if strings.HasPrefix(channel, "#") || strings.HasPrefix(channel, "@") {
		if ready, err := rh.apiProvider.IsReady(); !ready {
			rh.logger.Warn("Provider not ready for channel resolution", zap.Error(err))
			return nil, fmt.Errorf("channel %q not found in cache", channel)
		}
		channelsMaps := rh.apiProvider.ProvideChannelsMaps()
		chn, ok := channelsMaps.ChannelsInv[channel]
		if !ok {
			return nil, fmt.Errorf("channel %q not found", channel)
		}
		channel = channelsMaps.Channels[chn].ID
	}

	if !strings.Contains(timestamp, ".") {
		return nil, errors.New("timestamp must be in format 1234567890.123456")
	}

	return &getReactionsParams{
		channel:   channel,
		timestamp: timestamp,
	}, nil
}

func (rh *ReactionsHandler) parseListReactionsParams(request mcp.CallToolRequest) (*listReactionsParams, error) {
	user := request.GetString("user_id", "")
	if user == "" {
		return nil, errors.New("user_id must be a string")
	}

	limit := request.GetInt("limit", 100)
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("limit must be between 1 and 1000")
	}

	cursor := request.GetString("cursor", "")

	if strings.HasPrefix(user, "@") {
		if ready, err := rh.apiProvider.IsReady(); !ready {
			rh.logger.Warn("Provider not ready for user resolution", zap.Error(err))
			return nil, fmt.Errorf("user %q not found in cache", user)
		}
		usersMap := rh.apiProvider.ProvideUsersMap()
		userID, ok := usersMap.UsersInv[strings.TrimPrefix(user, "@")]
		if !ok {
			return nil, fmt.Errorf("user %q not found", user)
		}
		user = userID
	}

	return &listReactionsParams{
		user:   user,
		limit:  limit,
		cursor: cursor,
	}, nil
}

func isReactionsToolEnabled(channel string) bool {
	config := os.Getenv("SLACK_MCP_REACTIONS_TOOL")
	if config == "" || config == "true" || config == "1" {
		return true
	}
	items := strings.Split(config, ",")
	isNegated := strings.HasPrefix(strings.TrimSpace(items[0]), "!")
	for _, item := range items {
		item = strings.TrimSpace(item)
		if isNegated {
			if strings.TrimPrefix(item, "!") == channel {
				return false
			}
		} else {
			if item == channel {
				return true
			}
		}
	}
	return !isNegated
}

func marshalReactionResponseToCSV(responses []ReactionResponse) (*mcp.CallToolResult, error) {
	csvBytes, err := gocsv.MarshalBytes(&responses)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

func marshalMessageReactionsToCSV(messageReactions []MessageReactions) (*mcp.CallToolResult, error) {
	csvBytes, err := gocsv.MarshalBytes(&messageReactions)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

func marshalUserReactionsToCSV(userReactions []UserReaction) (*mcp.CallToolResult, error) {
	csvBytes, err := gocsv.MarshalBytes(&userReactions)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}
