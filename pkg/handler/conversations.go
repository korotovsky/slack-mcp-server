package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/server/auth"
	"github.com/korotovsky/slack-mcp-server/pkg/text"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	slackGoUtil "github.com/takara2314/slack-go-util"
)

const defaultConversationsNumericLimit = 50
const defaultConversationsExpressionLimit = "1d"

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

type User struct {
	UserID   string `json:"userID"`
	UserName string `json:"userName"`
	RealName string `json:"realName"`
}

type conversationParams struct {
	channel  string
	limit    int
	oldest   string
	latest   string
	cursor   string
	activity bool
}

var validFilterKeys = map[string]struct{}{
	"is":     {},
	"in":     {},
	"from":   {},
	"with":   {},
	"before": {},
	"after":  {},
	"on":     {},
	"during": {},
}

type searchParams struct {
	query string // query:search query
	limit int    // limit:100
	page  int    // page:1
}

type addMessageParams struct {
	channel     string
	threadTs    string
	text        string
	contentType string
}

type ConversationsHandler struct {
	apiProvider *provider.ApiProvider
}

func NewConversationsHandler(apiProvider *provider.ApiProvider) *ConversationsHandler {
	return &ConversationsHandler{
		apiProvider: apiProvider,
	}
}

func (ch *ConversationsHandler) UsersResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	// mark3labs/mcp-go does not support middlewares for resources.
	if authenticated, err := auth.IsAuthenticated(ctx, ch.apiProvider.ServerTransport()); !authenticated {
		return nil, err
	}

	if ready, err := ch.apiProvider.IsReady(); !ready {
		return nil, err
	}

	ar, err := ch.apiProvider.Slack().AuthTest()
	if err != nil {
		return nil, err
	}

	ws, err := text.Workspace(ar.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse workspace from URL: %v", err)
	}

	usersMaps := ch.apiProvider.ProvideUsersMap()
	users := usersMaps.Users

	usersList := make([]User, 0, len(users))
	for _, user := range users {
		usersList = append(usersList, User{
			UserID:   user.ID,
			UserName: user.Name,
			RealName: user.RealName,
		})
	}

	csvBytes, err := gocsv.MarshalBytes(&usersList)
	if err != nil {
		return nil, err
	}

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      "slack://" + ws + "/users",
			MIMEType: "text/csv",
			Text:     string(csvBytes),
		},
	}, nil
}

func (ch *ConversationsHandler) ConversationsAddMessageHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params, err := ch.parseParamsToolAddMessage(request)
	if err != nil {
		return nil, err
	}

	var options []slack.MsgOption

	if params.threadTs != "" {
		options = append(options, slack.MsgOptionTS(params.threadTs))
	}

	if params.contentType == "text/plain" {
		options = append(options, slack.MsgOptionDisableMarkdown())
		options = append(options, slack.MsgOptionText(params.text, false))
	} else if params.contentType == "text/markdown" {
		blocks, err := slackGoUtil.ConvertMarkdownTextToBlocks(params.text)
		if err == nil {
			options = append(options, slack.MsgOptionBlocks(blocks...))
		} else {
			// fallback to plain text if conversion fails
			log.Printf("Markdown parsing error: %s\n", err.Error())

			options = append(options, slack.MsgOptionDisableMarkdown())
			options = append(options, slack.MsgOptionText(params.text, false))
		}
	} else {
		return nil, errors.New("content_type must be either 'text/plain' or 'text/markdown'")
	}

	respChannel, respTimestamp, err := ch.apiProvider.Slack().PostMessageContext(ctx, params.channel, options...)

	if err != nil {
		return nil, err
	}

	historyParams := slack.GetConversationHistoryParameters{
		ChannelID: respChannel,
		Limit:     1,
		Oldest:    respTimestamp,
		Latest:    respTimestamp,
		Inclusive: true,
	}

	history, err := ch.apiProvider.Slack().GetConversationHistoryContext(ctx, &historyParams)
	if err != nil {
		return nil, err
	}

	messages := ch.convertMessagesFromHistory(history.Messages, historyParams.ChannelID, false)

	return marshalMessagesToCSV(messages)
}

func (ch *ConversationsHandler) ConversationsHistoryHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params, err := ch.parseParamsToolConversations(request)
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

	history, err := ch.apiProvider.Slack().GetConversationHistoryContext(ctx, &historyParams)
	if err != nil {
		return nil, err
	}

	messages := ch.convertMessagesFromHistory(history.Messages, params.channel, params.activity)

	if len(messages) > 0 && history.HasMore {
		messages[len(messages)-1].Cursor = history.ResponseMetaData.NextCursor
	}

	return marshalMessagesToCSV(messages)
}

func (ch *ConversationsHandler) ConversationsRepliesHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params, err := ch.parseParamsToolConversations(request)
	if err != nil {
		return nil, err
	}

	threadTs := request.GetString("thread_ts", "")
	if threadTs == "" {
		return nil, errors.New("thread_ts must be a string")
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

	replies, hasMore, nextCursor, err := ch.apiProvider.Slack().GetConversationRepliesContext(ctx, &repliesParams)
	if err != nil {
		return nil, err
	}

	messages := ch.convertMessagesFromHistory(replies, params.channel, params.activity)

	if len(messages) > 0 && hasMore {
		messages[len(messages)-1].Cursor = nextCursor
	}

	return marshalMessagesToCSV(messages)
}

func (ch *ConversationsHandler) ConversationsSearchHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params, err := ch.parseParamsToolSearch(request)
	if err != nil {
		return nil, err
	}

	searchParams := slack.SearchParameters{
		Sort:          slack.DEFAULT_SEARCH_SORT,
		SortDirection: slack.DEFAULT_SEARCH_SORT_DIR,
		Highlight:     false,
		Count:         params.limit,
		Page:          params.page,
	}

	messagesRes, _, err := ch.apiProvider.Slack().SearchContext(ctx, params.query, searchParams)
	if err != nil {
		return nil, err
	}

	messages := ch.convertMessagesFromSearch(messagesRes.Matches)

	if len(messages) > 0 && ((messagesRes.Pagination.PerPage * messagesRes.Pagination.PageCount) < messagesRes.Pagination.TotalCount) {
		nextCursor := fmt.Sprintf("page:%d", messagesRes.Pagination.PageCount+1)

		messages[len(messages)-1].Cursor = base64.StdEncoding.EncodeToString([]byte(nextCursor))
	}

	return marshalMessagesToCSV(messages)
}

func isChannelAllowed(channel string) bool {
	config := os.Getenv("SLACK_MCP_ADD_MESSAGE_TOOL")
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

func (ch *ConversationsHandler) convertMessagesFromHistory(slackMessages []slack.Message, channel string, includeActivity bool) []Message {
	usersMap := ch.apiProvider.ProvideUsersMap()
	var messages []Message
	warn := false

	for _, msg := range slackMessages {
		if msg.SubType != "" && !includeActivity {
			continue
		}

		userName, realName, ok := getUserInfo(msg.User, usersMap.Users)
		if !ok {
			warn = true
		}

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

	if ready, err := ch.apiProvider.IsReady(); !ready {
		if warn && errors.Is(err, provider.ErrUsersNotReady) {
			log.Printf("WARNING: Slack users sync is not ready yet, you may experience some limited functionality and see UIDs instead of resolved names as well as unable to query users by their @handles. Users sync is part of channels sync and operations on channels depend on users collection (IM, MPIM). Please wait until users are synced and try again.\n")
		}
	}

	return messages
}

func (ch *ConversationsHandler) convertMessagesFromSearch(slackMessages []slack.SearchMessage) []Message {
	usersMap := ch.apiProvider.ProvideUsersMap()
	var messages []Message
	warn := false

	for _, msg := range slackMessages {
		userName, realName, ok := getUserInfo(msg.User, usersMap.Users)
		if !ok {
			warn = true
		}

		threadTs, _ := extractThreadTS(msg.Permalink)

		messages = append(messages, Message{
			UserID:   msg.User,
			UserName: userName,
			RealName: realName,
			Text:     text.ProcessText(msg.Text),
			Channel:  fmt.Sprintf("#%s", msg.Channel.Name),
			ThreadTs: threadTs,
			Time:     msg.Timestamp,
		})
	}

	if ready, err := ch.apiProvider.IsReady(); !ready {
		if warn && errors.Is(err, provider.ErrUsersNotReady) {
			log.Printf("WARNING: Slack users sync is not ready yet, you may experience some limited functionality and see UIDs instead of resolved names as well as unable to query users by their @handles. Users sync is part of channels sync and operations on channels depend on users collection (IM, MPIM). Please wait until users are synced and try again.\n")
		}
	}

	return messages
}

func (ch *ConversationsHandler) parseParamsToolConversations(request mcp.CallToolRequest) (*conversationParams, error) {
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

	if strings.HasSuffix(limit, "d") || strings.HasSuffix(limit, "w") || strings.HasSuffix(limit, "m") {
		paramLimit, paramOldest, paramLatest, err = limitByExpression(limit, defaultConversationsExpressionLimit)
		if err != nil {
			return nil, err
		}
	} else if cursor == "" {
		paramLimit, err = limitByNumeric(limit, defaultConversationsNumericLimit)
		if err != nil {
			return nil, err
		}
	}

	if strings.HasPrefix(channel, "#") || strings.HasPrefix(channel, "@") {
		if ready, err := ch.apiProvider.IsReady(); !ready {
			if errors.Is(err, provider.ErrUsersNotReady) {
				log.Printf("WARNING: Slack users sync is not ready yet, you may experience some limited functionality and see UIDs instead of resolved names as well as unable to query users by their @handles. Users sync is part of channels sync and operations on channels depend on users collection (IM, MPIM). Please wait until users are synced and try again.\n")
			}
			if errors.Is(err, provider.ErrChannelsNotReady) {
				log.Printf("WARNING: Slack channels sync is not ready yet, you may experience some limited functionality and be able to request conversation only by Channel ID, not by its name. Please wait until channels are synced and try again.\n")
			}

			return nil, fmt.Errorf("channel %q not found in empty cache", channel)
		} else {
			channelsMaps := ch.apiProvider.ProvideChannelsMaps()
			chn, ok := channelsMaps.ChannelsInv[channel]
			if !ok {
				return nil, fmt.Errorf("channel %q not found in synced cache. Try to remove old cache file and restart MCP Server", channel)
			}

			channel = channelsMaps.Channels[chn].ID
		}
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

func (ch *ConversationsHandler) parseParamsToolAddMessage(request mcp.CallToolRequest) (*addMessageParams, error) {
	toolConfig := os.Getenv("SLACK_MCP_ADD_MESSAGE_TOOL")
	if toolConfig == "" {
		return nil, errors.New("by default, the conversations_add_message tool is disabled to guard Slack workspaces against accidental spamming. To enable it, set the SLACK_MCP_ADD_MESSAGE_TOOL environment variable to true, 1, or comma separated list of channels to limit where the MCP can post messages, e.g. 'SLACK_MCP_ADD_MESSAGE_TOOL=C1234567890,D0987654321', 'SLACK_MCP_ADD_MESSAGE_TOOL=!C1234567890' to enable all except one or 'SLACK_MCP_ADD_MESSAGE_TOOL=true' for all channels and DMs")
	}

	channel := request.GetString("channel_id", "")
	if channel == "" {
		return nil, errors.New("channel_id must be a string")
	}

	if strings.HasPrefix(channel, "#") || strings.HasPrefix(channel, "@") {
		channelsMaps := ch.apiProvider.ProvideChannelsMaps()
		chn, ok := channelsMaps.ChannelsInv[channel]
		if !ok {
			return nil, fmt.Errorf("channel %q not found", channel)
		}

		channel = channelsMaps.Channels[chn].ID
	}

	if !isChannelAllowed(channel) {
		return nil, fmt.Errorf("conversations_add_message tool is not allowed for channel %q, applied policy: %s", channel, toolConfig)
	}

	threadTs := request.GetString("thread_ts", "")
	if threadTs != "" && !strings.Contains(threadTs, ".") {
		return nil, errors.New("thread_ts must be a valid timestamp in format 1234567890.123456")
	}

	msgText := request.GetString("payload", "")
	if msgText == "" {
		return nil, errors.New("text must be a string")
	}

	contentType := request.GetString("content_type", "text/markdown")
	if contentType != "text/plain" && contentType != "text/markdown" {
		return nil, errors.New("content_type must be either 'text/plain' or 'text/markdown'")
	}

	return &addMessageParams{
		channel:     channel,
		threadTs:    threadTs,
		text:        msgText,
		contentType: contentType,
	}, nil
}

func (ch *ConversationsHandler) parseParamsToolSearch(req mcp.CallToolRequest) (*searchParams, error) {
	rawQuery := strings.TrimSpace(req.GetString("search_query", ""))

	freeText, filters := splitQuery(rawQuery)

	// is:thread
	if req.GetBool("filter_threads_only", false) {
		addFilter(filters, "is", "thread")
	}

	// in:channel or in:IM
	if chName := req.GetString("filter_in_channel", ""); chName != "" {
		f, err := ch.paramFormatChannel(chName)
		if err != nil {
			return nil, err
		}
		addFilter(filters, "in", f)
	} else if im := req.GetString("filter_in_im_or_mpim", ""); im != "" {
		f, err := ch.paramFormatUser(im)
		if err != nil {
			return nil, err
		}
		addFilter(filters, "in", f)
	}

	// with:
	if with := req.GetString("filter_users_with", ""); with != "" {
		f, err := ch.paramFormatUser(with)
		if err != nil {
			return nil, err
		}
		addFilter(filters, "with", f)
	}

	// from:
	if from := req.GetString("filter_users_from", ""); from != "" {
		f, err := ch.paramFormatUser(from)
		if err != nil {
			return nil, err
		}
		addFilter(filters, "from", f)
	}

	// date filters
	dateMap, err := buildDateFilters(
		req.GetString("filter_date_before", ""),
		req.GetString("filter_date_after", ""),
		req.GetString("filter_date_on", ""),
		req.GetString("filter_date_during", ""),
	)
	if err != nil {
		return nil, err
	}
	for key, val := range dateMap {
		addFilter(filters, key, val)
	}

	finalQuery := buildQuery(freeText, filters)

	limit := req.GetInt("limit", 100)
	cursor := req.GetString("cursor", "")

	var (
		page          int
		decodedCursor []byte
	)
	if cursor != "" {
		decodedCursor, err = base64.StdEncoding.DecodeString(cursor)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor: %v", err)
		}
		partOfCursor := strings.Split(string(decodedCursor), ":")
		if len(partOfCursor) != 2 {
			return nil, fmt.Errorf("invalid cursor: %v", cursor)
		}
		page, err = strconv.Atoi(partOfCursor[1])
		if err != nil || page < 1 {
			return nil, fmt.Errorf("invalid cursor page: %v", err)
		}
	} else {
		page = 1
	}

	return &searchParams{
		query: finalQuery,
		limit: limit,
		page:  page,
	}, nil
}

func (ch *ConversationsHandler) paramFormatUser(raw string) (string, error) {
	users := ch.apiProvider.ProvideUsersMap()

	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "U") {
		u, ok := users.Users[raw]
		if !ok {
			return "", fmt.Errorf("user %q not found", raw)
		}

		return fmt.Sprintf("<@%s>", u.ID), nil
	} else {
		if strings.HasPrefix(raw, "<@") {
			return raw, nil
		}
		if strings.HasPrefix(raw, "@") {
			raw = raw[1:]
		}
		u, ok := users.UsersInv[raw]
		if !ok {
			return "", fmt.Errorf("user %q not found", raw)
		}
		return fmt.Sprintf("@%s", users.Users[u].Name), nil
	}
}

func (ch *ConversationsHandler) paramFormatChannel(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	cms := ch.apiProvider.ProvideChannelsMaps()
	if strings.HasPrefix(raw, "#") {
		if chID, ok := cms.ChannelsInv[raw]; ok {
			return "#" + cms.Channels[chID].Name, nil
		}
		return "", fmt.Errorf("channel %q not found", raw)
	}
	if strings.HasPrefix(raw, "C") {
		if chn, ok := cms.Channels[raw]; ok {
			return "#" + chn.Name, nil
		}
		return "", fmt.Errorf("channel %q not found", raw)
	}
	return "", fmt.Errorf("invalid channel format: %q", raw)
}

func marshalMessagesToCSV(messages []Message) (*mcp.CallToolResult, error) {
	csvBytes, err := gocsv.MarshalBytes(&messages)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

func getUserInfo(userID string, usersMap map[string]slack.User) (userName string, realName string, ok bool) {
	if user, ok := usersMap[userID]; ok {
		return user.Name, user.RealName, true
	}
	return userID, userID, false
}

func limitByNumeric(limit string, defaultLimit int) (int, error) {
	if limit == "" {
		return defaultLimit, nil
	}

	n, err := strconv.Atoi(limit)
	if err != nil {
		return 0, fmt.Errorf("invalid numeric limit: %q", limit)
	}
	return n, nil
}

// limitByExpression parses a string like "1d", "2w", "3m", etc.
// It returns:
//   - the per page limit (always 100)
//   - oldest timestamp = midnight of (today − duration + 1 day) for days/weeks,
//     or midnight of (today − X months) for months,
//   - latest timestamp = now,
//   - or an error if parsing fails.
func limitByExpression(limit string, defaultLimit string) (slackLimit int, oldest, latest string, err error) {
	if limit == "" {
		limit = defaultLimit
	}

	if len(limit) < 2 {
		return 0, "", "", fmt.Errorf("invalid duration limit %q: too short", limit)
	}

	// suffix is the last character: d, w, or m
	suffix := limit[len(limit)-1]
	numStr := limit[:len(limit)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return 0, "", "", fmt.Errorf(
			"invalid duration limit %q: must be a positive integer followed by 'd', 'w', or 'm'",
			limit,
		)
	}

	now := time.Now()
	loc := now.Location()
	startOfToday := time.Date(
		now.Year(), now.Month(), now.Day(),
		0, 0, 0, 0,
		loc,
	)

	var oldestTime time.Time
	switch suffix {
	case 'd':
		oldestTime = startOfToday.AddDate(0, 0, -n+1)
	case 'w':
		oldestTime = startOfToday.AddDate(0, 0, -n*7+1)
	case 'm':
		oldestTime = startOfToday.AddDate(0, -n, 0)
	default:
		return 0, "", "", fmt.Errorf(
			"invalid duration limit %q: must end in 'd', 'w', or 'm'",
			limit,
		)
	}

	latest = fmt.Sprintf("%d.000000", now.Unix())
	oldest = fmt.Sprintf("%d.000000", oldestTime.Unix())

	return 100, oldest, latest, nil
}

func extractThreadTS(rawurl string) (string, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return "", err
	}
	return u.Query().Get("thread_ts"), nil
}

// parseFlexibleDate parses various date formats and returns the parsed time,
// the normalized YYYY-MM-DD format, and any error
func parseFlexibleDate(dateStr string) (time.Time, string, error) {
	dateStr = strings.TrimSpace(dateStr)

	// Try standard formats first (existing logic)
	standardFormats := []string{
		"2006-01-02",      // YYYY-MM-DD
		"2006/01/02",      // YYYY/MM/DD
		"01-02-2006",      // MM-DD-YYYY
		"01/02/2006",      // MM/DD/YYYY
		"02-01-2006",      // DD-MM-YYYY
		"02/01/2006",      // DD/MM/YYYY
		"Jan 2, 2006",     // Jan 2, 2006
		"January 2, 2006", // January 2, 2006
		"2 Jan 2006",      // 2 Jan 2006
		"2 January 2006",  // 2 January 2006
	}

	for _, format := range standardFormats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t, t.Format("2006-01-02"), nil
		}
	}

	// Month name mappings
	monthMap := map[string]int{
		"january": 1, "jan": 1,
		"february": 2, "feb": 2,
		"march": 3, "mar": 3,
		"april": 4, "apr": 4,
		"may":  5,
		"june": 6, "jun": 6,
		"july": 7, "jul": 7,
		"august": 8, "aug": 8,
		"september": 9, "sep": 9, "sept": 9,
		"october": 10, "oct": 10,
		"november": 11, "nov": 11,
		"december": 12, "dec": 12,
	}

	// Try flexible month-year formats
	// Pattern: "Month Year" or "Year Month"
	monthYearPattern := regexp.MustCompile(`^(\d{4})\s+([a-zA-Z]+)$|^([a-zA-Z]+)\s+(\d{4})$`)
	if matches := monthYearPattern.FindStringSubmatch(dateStr); matches != nil {
		var year int
		var monthStr string

		if matches[1] != "" && matches[2] != "" {
			// Format: "2025 July"
			year, _ = strconv.Atoi(matches[1])
			monthStr = strings.ToLower(matches[2])
		} else if matches[3] != "" && matches[4] != "" {
			// Format: "July 2025"
			monthStr = strings.ToLower(matches[3])
			year, _ = strconv.Atoi(matches[4])
		}

		if month, ok := monthMap[monthStr]; ok && year > 0 {
			t := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
			return t, t.Format("2006-01-02"), nil
		}
	}

	// Try patterns with day: "1-July-2025", "July-25-2025", "July 10 2025", "10 July 2025"
	// Pattern: "DD-Month-YYYY" or "Month-DD-YYYY"
	dayMonthYearPattern1 := regexp.MustCompile(`^(\d{1,2})[-\s]+([a-zA-Z]+)[-\s]+(\d{4})$`)
	if matches := dayMonthYearPattern1.FindStringSubmatch(dateStr); matches != nil {
		day, _ := strconv.Atoi(matches[1])
		monthStr := strings.ToLower(matches[2])
		year, _ := strconv.Atoi(matches[3])

		if month, ok := monthMap[monthStr]; ok && year > 0 && day > 0 && day <= 31 {
			t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
			if t.Day() == day {
				return t, t.Format("2006-01-02"), nil
			}
		}
	}

	// Pattern: "Month-DD-YYYY" or "Month DD YYYY"
	monthDayYearPattern := regexp.MustCompile(`^([a-zA-Z]+)[-\s]+(\d{1,2})[-\s]+(\d{4})$`)
	if matches := monthDayYearPattern.FindStringSubmatch(dateStr); matches != nil {
		monthStr := strings.ToLower(matches[1])
		day, _ := strconv.Atoi(matches[2])
		year, _ := strconv.Atoi(matches[3])

		if month, ok := monthMap[monthStr]; ok && year > 0 && day > 0 && day <= 31 {
			t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
			if t.Day() == day {
				return t, t.Format("2006-01-02"), nil
			}
		}
	}

	// Pattern: "YYYY Month DD" (e.g., "2025 July 10")
	yearMonthDayPattern := regexp.MustCompile(`^(\d{4})[-\s]+([a-zA-Z]+)[-\s]+(\d{1,2})$`)
	if matches := yearMonthDayPattern.FindStringSubmatch(dateStr); matches != nil {
		year, _ := strconv.Atoi(matches[1])
		monthStr := strings.ToLower(matches[2])
		day, _ := strconv.Atoi(matches[3])

		if month, ok := monthMap[monthStr]; ok && year > 0 && day > 0 && day <= 31 {
			t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
			if t.Day() == day {
				return t, t.Format("2006-01-02"), nil
			}
		}
	}

	lowerDateStr := strings.ToLower(dateStr)
	now := time.Now().UTC()

	switch lowerDateStr {
	case "today":
		t := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return t, t.Format("2006-01-02"), nil
	case "yesterday":
		t := now.AddDate(0, 0, -1)
		t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return t, t.Format("2006-01-02"), nil
	case "tomorrow":
		t := now.AddDate(0, 0, 1)
		t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return t, t.Format("2006-01-02"), nil
	}

	// Try "X days ago" pattern
	daysAgoPattern := regexp.MustCompile(`^(\d+)\s+days?\s+ago$`)
	if matches := daysAgoPattern.FindStringSubmatch(lowerDateStr); matches != nil {
		days, _ := strconv.Atoi(matches[1])
		t := now.AddDate(0, 0, -days)
		t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return t, t.Format("2006-01-02"), nil
	}

	return time.Time{}, "", fmt.Errorf("unable to parse date: %s", dateStr)
}

// buildDateFilters remains the same as it already uses parseFlexibleDate
func buildDateFilters(before, after, on, during string) (map[string]string, error) {
	out := make(map[string]string)

	if on != "" {
		if during != "" || before != "" || after != "" {
			return nil, fmt.Errorf("'on' cannot be combined with other date filters")
		}
		_, normalized, err := parseFlexibleDate(on)
		if err != nil {
			return nil, fmt.Errorf("invalid 'on' date: %v", err)
		}
		out["on"] = normalized
		return out, nil
	}
	if during != "" {
		if before != "" || after != "" {
			return nil, fmt.Errorf("'during' cannot be combined with 'before' or 'after'")
		}
		_, normalized, err := parseFlexibleDate(during)
		if err != nil {
			return nil, fmt.Errorf("invalid 'during' date: %v", err)
		}
		out["during"] = normalized
		return out, nil
	}
	if after != "" {
		_, normalized, err := parseFlexibleDate(after)
		if err != nil {
			return nil, fmt.Errorf("invalid 'after' date: %v", err)
		}
		out["after"] = normalized
	}
	if before != "" {
		_, normalized, err := parseFlexibleDate(before)
		if err != nil {
			return nil, fmt.Errorf("invalid 'before' date: %v", err)
		}
		out["before"] = normalized
	}
	if after != "" && before != "" {
		a, _, _ := parseFlexibleDate(after)
		b, _, _ := parseFlexibleDate(before)
		if a.After(b) {
			return nil, fmt.Errorf("'after' date is after 'before' date")
		}
	}
	return out, nil
}

func isFilterKey(key string) bool {
	_, ok := validFilterKeys[strings.ToLower(key)]
	return ok
}

func splitQuery(q string) (freeText []string, filters map[string][]string) {
	filters = make(map[string][]string)
	for _, tok := range strings.Fields(q) {
		parts := strings.SplitN(tok, ":", 2)
		if len(parts) == 2 && isFilterKey(parts[0]) {
			key := strings.ToLower(parts[0])
			filters[key] = append(filters[key], parts[1])
		} else {
			freeText = append(freeText, tok)
		}
	}
	return
}

func addFilter(filters map[string][]string, key, val string) {
	for _, existing := range filters[key] {
		if existing == val {
			return
		}
	}
	filters[key] = append(filters[key], val)
}

func buildQuery(freeText []string, filters map[string][]string) string {
	out := make([]string, 0, len(freeText)+len(filters)*2)
	out = append(out, freeText...)
	for _, key := range []string{"is", "in", "from", "with", "before", "after", "on", "during"} {
		for _, val := range filters[key] {
			out = append(out, fmt.Sprintf("%s:%s", key, val))
		}
	}
	return strings.Join(out, " ")
}
