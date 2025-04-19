package handler

import (
	"context"
	"errors"
	"fmt"
	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/internal/provider"
	"github.com/korotovsky/slack-mcp-server/internal/text"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	UserID   string `json:"userID"`
	UserName string `json:"userUser"`
	RealName string `json:"realName"`
	Channel  string `json:"channelID"`
	Text     string `json:"text"`
	Time     string `json:"time"`
	Cursor   string `json:"cursor"`
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
	var (
		err         error
		paramLimit  int
		paramOldest string
		paramLatest string
	)

	channel, ok := request.Params.Arguments["channel_id"].(string)
	if !ok {
		return nil, errors.New("channel_id must be a string")
	}

	limit := ""
	limitOptional := request.Params.Arguments["limit"]
	if limitOptional != nil {
		limit = limitOptional.(string)
	}

	cursor := ""
	cursorOptional := request.Params.Arguments["cursor"]
	if cursorOptional != nil {
		cursor = cursorOptional.(string)
	}

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

	api, err := ch.apiProvider.Provide()
	if err != nil {
		return nil, err
	}

	params := slack.GetConversationHistoryParameters{
		ChannelID: channel,
		Limit:     paramLimit,
		Oldest:    paramOldest,
		Latest:    paramLatest,
		Cursor:    cursor,
		Inclusive: false,
	}
	messages, err := api.GetConversationHistoryContext(ctx, &params)
	if err != nil {
		return nil, err
	}

	usersMap := ch.apiProvider.ProvideUsersMap()

	var messageList []Message
	for _, message := range messages.Messages {
		textTokenized := text.ProcessText(message.Text)
		user, ok := usersMap[message.User]
		if !ok {
			// TODO: add periodic refetch of users
			continue
		}

		messageList = append(messageList, Message{
			UserID:   message.User,
			UserName: user.Name,
			RealName: user.RealName,
			Text:     textTokenized,
			Channel:  channel,
			Time:     message.Timestamp,
		})
	}

	if len(messageList) > 0 && messages.HasMore {
		messageList[len(messageList)-1].Cursor = messages.ResponseMetaData.NextCursor
	}

	csvBytes, err := gocsv.MarshalBytes(&messageList)
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(csvBytes)), nil
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
