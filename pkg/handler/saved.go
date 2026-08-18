package handler

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/limiter"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

type SavedItemRow struct {
	ItemID      string `csv:"ItemID"`
	ChannelID   string `csv:"ChannelID"`
	ChannelName string `csv:"ChannelName"`
	Ts          string `csv:"Ts"`
	DateCreated string `csv:"DateCreated"`
	DateDue     string `csv:"DateDue"`
	State       string `csv:"State"`
}

// savedItemTypeMessage is the item_type used for saved messages. Slack's
// saved.* API also supports other item types (e.g. files), but the saved_*
// tools only manage messages.
const savedItemTypeMessage = "message"

// slackTsRe matches a Slack message timestamp, e.g. "1234567890.123456".
var slackTsRe = regexp.MustCompile(`^\d+\.\d+$`)

type SavedHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
	convHandler *ConversationsHandler
}

func NewSavedHandler(apiProvider *provider.ApiProvider, logger *zap.Logger, convHandler *ConversationsHandler) *SavedHandler {
	return &SavedHandler{apiProvider: apiProvider, logger: logger, convHandler: convHandler}
}

func (h *SavedHandler) SavedListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("SavedListHandler called", zap.Any("params", request.Params))

	filter := request.GetString("filter", "saved")
	limit := request.GetInt("limit", 50)
	includeMessages := request.GetBool("include_messages", true)
	maxMsgsPerItem := request.GetInt("max_messages_per_item", 5)

	channelsMaps := h.apiProvider.ProvideChannelsMaps()
	rl := limiter.Tier3.Limiter()

	var allItems []SavedItemRow
	var allMessages []Message
	cursor := ""
	fetched := 0
	pageSize := limit
	if pageSize > 50 {
		pageSize = 50
	}

	for fetched < limit {
		if cursor != "" {
			if err := rl.Wait(ctx); err != nil {
				h.logger.Warn("Rate limiter wait failed, stopping pagination", zap.Error(err))
				break
			}
		}

		resp, err := h.apiProvider.Slack().SavedList(ctx, filter, pageSize, cursor)
		if err != nil {
			h.logger.Error("SavedList failed", zap.Error(err))
			return nil, fmt.Errorf("failed to list saved items: %v", err)
		}

		for _, item := range resp.SavedItems {
			if fetched >= limit {
				break
			}

			channelName := item.ItemID
			if cached, ok := channelsMaps.Channels[item.ItemID]; ok {
				channelName = cached.Name
			}

			row := SavedItemRow{
				ItemID:      item.ItemID,
				ChannelID:   item.ItemID,
				ChannelName: channelName,
				Ts:          item.Ts,
				DateCreated: formatUnixTs(item.DateCreated),
				DateDue:     formatUnixTs(item.DateDue),
				State:       item.State,
			}
			allItems = append(allItems, row)
			fetched++

			if includeMessages {
				if err := rl.Wait(ctx); err != nil {
					break
				}
				histParams := &slack.GetConversationHistoryParameters{
					ChannelID: item.ItemID,
					Latest:    item.Ts,
					Oldest:    item.Ts,
					Inclusive: true,
					Limit:     1,
				}
				histResp, err := h.apiProvider.Slack().GetConversationHistoryContext(ctx, histParams)
				if err != nil {
					h.logger.Warn("Failed to fetch saved message via history, trying replies",
						zap.String("channel", item.ItemID),
						zap.String("ts", item.Ts),
						zap.Error(err))
					allMessages = append(allMessages, Message{
						MsgID:   item.Ts,
						Channel: channelName,
						Text:    "[message content unavailable — channel access denied]",
						Time:    formatUnixTs(item.DateCreated),
					})
					continue
				}
				if len(histResp.Messages) > 0 {
					msg := histResp.Messages[0]
					if msg.ThreadTimestamp != "" && msg.ThreadTimestamp != msg.Timestamp {
						repliesParams := &slack.GetConversationRepliesParameters{
							ChannelID: item.ItemID,
							Timestamp: msg.ThreadTimestamp,
							Latest:    item.Ts,
							Oldest:    item.Ts,
							Inclusive: true,
							Limit:     maxMsgsPerItem,
						}
						replies, _, _, err := h.apiProvider.Slack().GetConversationRepliesContext(ctx, repliesParams)
						if err == nil && len(replies) > 0 {
							msgs := h.convHandler.convertMessagesFromHistory(ctx, replies, item.ItemID, false)
							allMessages = append(allMessages, msgs...)
							continue
						}
					}
					msgs := h.convHandler.convertMessagesFromHistory(ctx, histResp.Messages, item.ItemID, false)
					allMessages = append(allMessages, msgs...)
				} else {
					repliesParams := &slack.GetConversationRepliesParameters{
						ChannelID: item.ItemID,
						Timestamp: item.Ts,
						Latest:    item.Ts,
						Oldest:    item.Ts,
						Inclusive: true,
						Limit:     maxMsgsPerItem,
					}
					replies, _, _, err := h.apiProvider.Slack().GetConversationRepliesContext(ctx, repliesParams)
					if err == nil && len(replies) > 0 {
						msgs := h.convHandler.convertMessagesFromHistory(ctx, replies, item.ItemID, false)
						allMessages = append(allMessages, msgs...)
					} else {
						allMessages = append(allMessages, Message{
							MsgID:   item.Ts,
							Channel: channelName,
							Text:    "[saved item — message not found in channel history]",
							Time:    formatUnixTs(item.DateCreated),
						})
					}
				}
			}
		}

		if resp.ResponseMetadata.NextCursor == "" || fetched >= limit {
			break
		}
		cursor = resp.ResponseMetadata.NextCursor
	}

	if includeMessages && len(allMessages) > 0 {
		return marshalMessagesToCSV(allMessages)
	}

	csvBytes, err := gocsv.MarshalBytes(&allItems)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal saved items: %v", err)
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

// parseSavedItemParams reads and validates the channel_id and ts parameters
// shared by saved_add and saved_delete, resolving #channel / @user names to IDs.
// For convenience, item_id (the name used by saved_list output and saved_update)
// is accepted as an alias of channel_id.
func (h *SavedHandler) parseSavedItemParams(ctx context.Context, request mcp.CallToolRequest) (channelID string, ts string, err error) {
	channel := strings.TrimSpace(request.GetString("channel_id", ""))
	if channel == "" {
		channel = strings.TrimSpace(request.GetString("item_id", ""))
	}
	ts = strings.TrimSpace(request.GetString("ts", ""))

	if channel == "" || ts == "" {
		return "", "", fmt.Errorf("channel_id and ts are required parameters")
	}
	if !slackTsRe.MatchString(ts) {
		return "", "", fmt.Errorf("ts must be a Slack message timestamp in format 1234567890.123456, got %q", ts)
	}

	channelID, err = h.convHandler.resolveChannelID(ctx, channel)
	if err != nil {
		h.logger.Error("Channel not found", zap.String("channel", channel), zap.Error(err))
		return "", "", err
	}

	return channelID, ts, nil
}

func (h *SavedHandler) SavedAddHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("SavedAddHandler called", zap.Any("params", request.Params))

	dateDue := int64(request.GetInt("date_due", 0))
	if dateDue < 0 {
		return nil, fmt.Errorf("date_due must be a positive unix timestamp, got %d", dateDue)
	}

	channelID, ts, err := h.parseSavedItemParams(ctx, request)
	if err != nil {
		return nil, err
	}

	if err := h.apiProvider.Slack().SavedAdd(ctx, savedItemTypeMessage, channelID, ts); err != nil {
		h.logger.Error("SavedAdd failed",
			zap.String("item_id", channelID),
			zap.String("ts", ts),
			zap.Error(err))
		return nil, fmt.Errorf("failed to save message: %v", err)
	}

	result := fmt.Sprintf("Successfully saved message to 'Save for Later' (item_id=%s, ts=%s)", channelID, ts)

	// saved.add only saves the item; a due date / reminder is applied with a
	// follow-up saved.update, exactly like the Slack client does.
	if dateDue > 0 {
		if err := h.apiProvider.Slack().SavedUpdate(ctx, savedItemTypeMessage, channelID, ts, "", dateDue); err != nil {
			h.logger.Error("SavedUpdate (date_due) after SavedAdd failed",
				zap.String("item_id", channelID),
				zap.String("ts", ts),
				zap.Int64("date_due", dateDue),
				zap.Error(err))
			return nil, fmt.Errorf("message was saved (item_id=%s, ts=%s) but setting the due date failed: %v", channelID, ts, err)
		}
		result += fmt.Sprintf(", due date set to %s", formatUnixTs(dateDue))
	}

	return mcp.NewToolResultText(result), nil
}

func (h *SavedHandler) SavedDeleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("SavedDeleteHandler called", zap.Any("params", request.Params))

	channelID, ts, err := h.parseSavedItemParams(ctx, request)
	if err != nil {
		return nil, err
	}

	if err := h.apiProvider.Slack().SavedDelete(ctx, savedItemTypeMessage, channelID, ts); err != nil {
		h.logger.Error("SavedDelete failed",
			zap.String("item_id", channelID),
			zap.String("ts", ts),
			zap.Error(err))
		return nil, fmt.Errorf("failed to remove saved item: %v", err)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully removed message from 'Save for Later' (item_id=%s, ts=%s)", channelID, ts)), nil
}

func (h *SavedHandler) SavedUpdateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("SavedUpdateHandler called", zap.Any("params", request.Params))

	itemID := request.GetString("item_id", "")
	ts := request.GetString("ts", "")
	mark := request.GetString("mark", "")
	dateDue := int64(request.GetInt("date_due", 0))

	if itemID == "" || ts == "" {
		return nil, fmt.Errorf("item_id and ts are required parameters")
	}
	if mark == "" && dateDue == 0 {
		return nil, fmt.Errorf("at least one of mark or date_due must be provided")
	}

	err := h.apiProvider.Slack().SavedUpdate(ctx, "message", itemID, ts, mark, dateDue)
	if err != nil {
		h.logger.Error("SavedUpdate failed",
			zap.String("item_id", itemID),
			zap.String("ts", ts),
			zap.String("mark", mark),
			zap.Int64("date_due", dateDue),
			zap.Error(err))
		return nil, fmt.Errorf("failed to update saved item: %v", err)
	}

	action := "updated"
	if mark == "completed" {
		action = "marked as completed"
	}
	if dateDue > 0 {
		dueTime := time.Unix(dateDue, 0).UTC().Format("2006-01-02 15:04")
		action += fmt.Sprintf(", due date set to %s", dueTime)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully %s saved item (item_id=%s, ts=%s)", action, itemID, ts)), nil
}

func (h *SavedHandler) SavedClearCompletedHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("SavedClearCompletedHandler called", zap.Any("params", request.Params))

	err := h.apiProvider.Slack().SavedClearCompleted(ctx)
	if err != nil {
		h.logger.Error("SavedClearCompleted failed", zap.Error(err))
		return nil, fmt.Errorf("failed to clear completed saved items: %v", err)
	}

	return mcp.NewToolResultText("Successfully cleared all completed saved items"), nil
}

func formatUnixTs(ts int64) string {
	if ts == 0 {
		return ""
	}
	return time.Unix(ts, 0).UTC().Format("2006-01-02 15:04")
}
