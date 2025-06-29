package handler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/text"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
)

type Message struct {
	UserID   string `json:"userID"`
	UserName string `json:"userUser"`
	RealName string `json:"realName"`
	Channel  string `json:"channelID"`
	ThreadTs string `json:"ThreadTs"`
	Text     string `json:"text"`
	Time     string `json:"time"`
	Cursor   string `json:"cursor"`
}

type conversationParams struct {
	channel  string
	limit    int
	oldest   string
	latest   string
	cursor   string
	activity bool
}

type ConversationsHandler struct {
	apiProvider *provider.ApiProvider
}

func NewConversationsHandler(apiProvider *provider.ApiProvider) *ConversationsHandler {
	return &ConversationsHandler{
		apiProvider: apiProvider,
	}
}

func (ch *ConversationsHandler) ConversationsHistoryHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params, err := ch.parseParams(request)
	if err != nil {
		return nil, err
	}

	api, err := ch.apiProvider.ProvideGeneric()
	if err != nil {
		return nil, err
	}

	historyParams := slack.GetConversationHistoryParameters{
		ChannelID: params.channel,
		Limit:     params.limit,
		Oldest:    params.oldest,
		Latest:    params.latest,
		Cursor:    params.cursor,
		Inclusive: false,
	}

	history, err := api.GetConversationHistoryContext(ctx, &historyParams)
	if err != nil {
		return nil, err
	}

	messages := ch.convertMessages(history.Messages, params.channel, params.activity)

	if len(messages) > 0 && history.HasMore {
		messages[len(messages)-1].Cursor = history.ResponseMetaData.NextCursor
	}

	return ch.marshalMessagesToCSV(messages)
}

func (ch *ConversationsHandler) ConversationsRepliesHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params, err := ch.parseParams(request)
	if err != nil {
		return nil, err
	}

	threadTs := request.GetString("thread_ts", "")
	if threadTs == "" {
		return nil, errors.New("thread_ts must be a string")
	}

	api, err := ch.apiProvider.ProvideGeneric()
	if err != nil {
		return nil, err
	}

	repliesParams := slack.GetConversationRepliesParameters{
		ChannelID: params.channel,
		Timestamp: threadTs,
		Limit:     params.limit,
		Oldest:    params.oldest,
		Latest:    params.latest,
		Cursor:    params.cursor,
		Inclusive: false,
	}

	replies, hasMore, nextCursor, err := api.GetConversationRepliesContext(ctx, &repliesParams)
	if err != nil {
		return nil, err
	}

	messages := ch.convertMessages(replies, params.channel, params.activity)

	if len(messages) > 0 && hasMore {
		messages[len(messages)-1].Cursor = nextCursor
	}

	return ch.marshalMessagesToCSV(messages)
}

func (ch *ConversationsHandler) convertMessages(slackMessages []slack.Message, channel string, includeActivity bool) []Message {
	usersMap := ch.apiProvider.ProvideUsersMap()
	var messages []Message

	for _, msg := range slackMessages {
		if msg.SubType != "" && !includeActivity {
			continue
		}

		userName, realName := getUserInfo(msg.User, usersMap)

		messages = append(messages, Message{
			UserID:   msg.User,
			UserName: userName,
			RealName: realName,
			Text:     text.ProcessText(msg.Text),
			Channel:  channel,
			ThreadTs: msg.ThreadTimestamp,
			Time:     msg.Timestamp,
		})
	}

	return messages
}

func (ch *ConversationsHandler) marshalMessagesToCSV(messages []Message) (*mcp.CallToolResult, error) {
	csvBytes, err := gocsv.MarshalBytes(&messages)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

func (ch *ConversationsHandler) parseParams(request mcp.CallToolRequest) (*conversationParams, error) {
	channel := request.GetString("channel_id", "")
	if channel == "" {
		return nil, errors.New("channel_id must be a string")
	}

	limit := request.GetString("limit", "")
	cursor := request.GetString("cursor", "")
	activity := request.GetBool("include_activity_messages", false)

	var (
		paramLimit  int
		paramOldest string
		paramLatest string
		err         error
	)

	if strings.HasSuffix(limit, "d") {
		paramLimit, paramOldest, paramLatest, err = limitByDays(limit)
		if err != nil {
			return nil, err
		}
	} else if cursor == "" {
		paramLimit, err = limitByNumeric(limit)
		if err != nil {
			return nil, err
		}
	}

	if strings.HasPrefix(channel, "#") {
		channelsMaps := ch.apiProvider.ProvideChannelsMaps()
		chn, ok := channelsMaps.ChannelsInv[channel]
		if !ok {
			return nil, fmt.Errorf("channel %q not found", channel)
		}

		channel = channelsMaps.Channels[chn].ID
	}

	return &conversationParams{
		channel:  channel,
		limit:    paramLimit,
		oldest:   paramOldest,
		latest:   paramLatest,
		cursor:   cursor,
		activity: activity,
	}, nil
}

func getUserInfo(userID string, usersMap map[string]slack.User) (userName, realName string) {
	if user, ok := usersMap[userID]; ok {
		return user.Name, user.RealName
	}
	return userID, userID
}

func limitByNumeric(limit string) (int, error) {
	n, err := strconv.Atoi(limit)
	if err != nil {
		return 0, fmt.Errorf("invalid numeric limit: %q", limit)
	}
	return n, nil
}

// limitByDays parses a string like "1d", "2d", etc.
// It returns:
//   - the per page limit (100)
//   - oldest timestamp = midnight of (today − days + 1),
//   - latest timestamp = now,
//   - or an error if parsing fails.
func limitByDays(limit string) (slackLimit int, oldest, latest string, err error) {
	daysStr := strings.TrimSuffix(limit, "d")
	days, err := strconv.Atoi(daysStr)
	if err != nil || days <= 0 {
		return 0, "", "", fmt.Errorf("invalid duration limit %q: must be a positive integer with 'd' suffix", limit)
	}

	now := time.Now()
	loc := now.Location()

	startOfToday := time.Date(
		now.Year(), now.Month(), now.Day(),
		0, 0, 0, 0,
		loc,
	)

	oldestTime := startOfToday.AddDate(0, 0, -days+1)

	latest = fmt.Sprintf("%d.000000", now.Unix())
	oldest = fmt.Sprintf("%d.000000", oldestTime.Unix())

	return 100, oldest, latest, nil
}
