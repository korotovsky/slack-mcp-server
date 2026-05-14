package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/limiter"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

type ActivityItem struct {
	Type        string `csv:"Type"`
	ChannelID   string `csv:"ChannelID"`
	ChannelName string `csv:"ChannelName"`
	ThreadTs    string `csv:"ThreadTs"`
	UnreadCount int    `csv:"UnreadCount"`
	FeedTs      string `csv:"FeedTs"`
	Key         string `csv:"Key"`
	MinUnreadTs string `csv:"MinUnreadTs"`
}

type ActivityHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
	convHandler *ConversationsHandler
}

func NewActivityHandler(apiProvider *provider.ApiProvider, logger *zap.Logger, convHandler *ConversationsHandler) *ActivityHandler {
	return &ActivityHandler{apiProvider: apiProvider, logger: logger, convHandler: convHandler}
}

func (h *ActivityHandler) ActivityUnreadsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("ActivityUnreadsHandler called", zap.Any("params", request.Params))

	includeMessages := request.GetBool("include_messages", true)
	maxMsgsPerThread := request.GetInt("max_messages_per_thread", 10)
	limit := request.GetInt("limit", 30)

	feedResp, err := h.apiProvider.Slack().ActivityFeed(ctx, limit)
	if err != nil {
		h.logger.Error("ActivityFeed failed", zap.Error(err))
		return nil, fmt.Errorf("failed to get activity feed: %v", err)
	}

	channelsMaps := h.apiProvider.ProvideChannelsMaps()
	usersMap := h.apiProvider.ProvideUsersMap()

	var items []ActivityItem
	for _, fi := range feedResp.Items {
		if !fi.IsUnread {
			continue
		}

		var ai ActivityItem
		ai.FeedTs = fi.FeedTs
		ai.Key = fi.Key
		ai.Type = fi.Item.Type

		switch fi.Item.Type {
		case "thread_v2":
			if fi.Item.BundleInfo == nil {
				continue
			}
			te := fi.Item.BundleInfo.Payload.ThreadEntry
			ai.ChannelID = te.ChannelID
			ai.ThreadTs = te.ThreadTs
			ai.UnreadCount = te.UnreadMsgCount
			ai.MinUnreadTs = te.MinUnreadTs
		default:
			if fi.Item.Message == nil {
				continue
			}
			ai.ChannelID = fi.Item.Message.Channel
			ai.ThreadTs = fi.Item.Message.ThreadTs
			ai.UnreadCount = 1
			ai.MinUnreadTs = fi.Item.Message.Ts
		}

		if cached, ok := channelsMaps.Channels[ai.ChannelID]; ok {
			ai.ChannelName = cached.Name
		} else {
			ai.ChannelName = ai.ChannelID
		}

		items = append(items, ai)
	}

	h.logger.Debug("Filtered unread activity items", zap.Int("count", len(items)))

	if !includeMessages {
		csvBytes, err := gocsv.MarshalBytes(&items)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal activity items: %v", err)
		}
		return mcp.NewToolResultText(string(csvBytes)), nil
	}

	// Fetch unread messages per unique thread
	type threadKey struct {
		ChannelID string
		ThreadTs  string
	}
	seen := make(map[threadKey]bool)
	var threads []struct {
		ChannelID   string
		ThreadTs    string
		MinUnreadTs string
	}
	for _, ai := range items {
		if ai.ThreadTs == "" {
			continue
		}
		tk := threadKey{ai.ChannelID, ai.ThreadTs}
		if seen[tk] {
			continue
		}
		seen[tk] = true
		threads = append(threads, struct {
			ChannelID   string
			ThreadTs    string
			MinUnreadTs string
		}{ai.ChannelID, ai.ThreadTs, ai.MinUnreadTs})
	}

	rl := limiter.Tier3.Limiter()
	var allMessages []Message

	for _, t := range threads {
		if err := rl.Wait(ctx); err != nil {
			h.logger.Warn("Rate limiter wait failed, stopping fetch", zap.Error(err))
			break
		}

		oldest := t.MinUnreadTs
		repliesParams := slack.GetConversationRepliesParameters{
			ChannelID: t.ChannelID,
			Timestamp: t.ThreadTs,
			Oldest:    oldest,
			Limit:     maxMsgsPerThread,
			Inclusive: true,
		}
		replies, _, _, err := h.apiProvider.Slack().GetConversationRepliesContext(ctx, &repliesParams)
		if err != nil {
			h.logger.Warn("Failed to get thread replies",
				zap.String("channel", t.ChannelID),
				zap.String("thread_ts", t.ThreadTs),
				zap.Error(err))
			continue
		}

		msgs := h.convHandler.convertMessagesFromHistory(replies, t.ChannelID, false)

		// Annotate with channel name
		channelName := t.ChannelID
		if cached, ok := channelsMaps.Channels[t.ChannelID]; ok {
			channelName = cached.Name
		}
		_ = channelName
		_ = usersMap

		allMessages = append(allMessages, msgs...)
	}

	if len(allMessages) == 0 {
		// Fall back to summary if no messages could be fetched
		var sb strings.Builder
		sb.WriteString("No messages could be fetched. Activity summary:\n")
		csvBytes, err := gocsv.MarshalBytes(&items)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal activity items: %v", err)
		}
		sb.Write(csvBytes)
		return mcp.NewToolResultText(sb.String()), nil
	}

	return marshalMessagesToCSV(allMessages)
}

func (h *ActivityHandler) ActivityMarkReadHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("ActivityMarkReadHandler called", zap.Any("params", request.Params))

	key := request.GetString("key", "")
	feedTs := request.GetString("feed_ts", "")
	itemType := request.GetString("type", "")

	if key == "" || feedTs == "" || itemType == "" {
		return nil, fmt.Errorf("key, feed_ts, and type are all required parameters")
	}

	err := h.apiProvider.Slack().ActivityMarkRead(ctx, itemType, feedTs, key)
	if err != nil {
		h.logger.Error("ActivityMarkRead failed",
			zap.String("key", key),
			zap.String("feed_ts", feedTs),
			zap.String("type", itemType),
			zap.Error(err))
		return nil, fmt.Errorf("failed to mark activity as read: %v", err)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully marked activity item as read (key=%s)", key)), nil
}
