package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/limiter"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"github.com/korotovsky/slack-mcp-server/pkg/text"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

const (
	threadsFilterUnread = "unread"
	threadsFilterAll    = "all"

	// threadsPageSize is what subscriptions.thread.getView returns per page
	// regardless of the requested limit.
	threadsPageSize = 10
	// threadsMaxPages bounds one tool call; the caller continues with the cursor.
	threadsMaxPages = 50

	threadsDefaultLimit   = 20
	threadsMaxLimit       = 200
	threadsDefaultReplies = 3
	threadsMaxReplies     = 20
	threadsDefaultSince   = "30d"
	threadsSinceUnbounded = "all"
	threadsRepliesJoin    = " || "
)

// ThreadRow is one thread of Slack's "Threads" view as returned by conversations_threads.
type ThreadRow struct {
	Channel         string `csv:"Channel"`
	ChannelID       string `csv:"ChannelID"`
	ThreadTs        string `csv:"ThreadTs"`
	RootUser        string `csv:"RootUser"`
	RootTime        string `csv:"RootTime"`
	RootText        string `csv:"RootText"`
	ReplyCount      int    `csv:"ReplyCount"`
	UnreadReplies   int    `csv:"UnreadReplies"`
	LatestReplyTime string `csv:"LatestReplyTime"`
	LastRead        string `csv:"LastRead"`
	Replies         string `csv:"Replies"`
	Cursor          string `csv:"Cursor"`
}

type ThreadsHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
	convHandler *ConversationsHandler
}

func NewThreadsHandler(apiProvider *provider.ApiProvider, logger *zap.Logger, convHandler *ConversationsHandler) *ThreadsHandler {
	return &ThreadsHandler{apiProvider: apiProvider, logger: logger, convHandler: convHandler}
}

type threadsParams struct {
	filter         string
	channelID      string
	oldest         string // Slack ts; threads whose latest activity is older stop the scan ("" = unbounded)
	limit          int
	includeReplies int
	cursor         string
}

// threadsPageFetcher fetches one page of the Threads view for a cursor.
type threadsPageFetcher func(ctx context.Context, cursor string) (edge.ThreadsViewResponse, error)

// threadsScan is the outcome of collectThreads.
type threadsScan struct {
	threads    []edge.ThreadView
	nextCursor string // "" when the scan is exhausted (no more pages, or the time bound was reached)
	scanned    int    // threads looked at, matched or not
	pages      int
	hitPageCap bool
}

func (h *ThreadsHandler) ConversationsThreadsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("ConversationsThreadsHandler called", zap.Any("params", request.Params))

	params, err := h.parseThreadsParams(ctx, request)
	if err != nil {
		h.logger.Error("Failed to parse threads params", zap.Error(err))
		return nil, err
	}

	rl := limiter.Tier3.Limiter()
	first := true
	fetch := func(ctx context.Context, cursor string) (edge.ThreadsViewResponse, error) {
		if !first {
			if err := rl.Wait(ctx); err != nil {
				return edge.ThreadsViewResponse{}, err
			}
		}
		first = false
		return h.apiProvider.Slack().SubscriptionsThreadGetView(ctx, cursor, threadsPageSize, params.channelID)
	}

	scan, err := collectThreads(ctx, fetch, params)
	if err != nil {
		h.logger.Error("Threads view fetch failed", zap.Error(err))
		return nil, fmt.Errorf("failed to list threads: %v", err)
	}

	if len(scan.threads) == 0 {
		msg := fmt.Sprintf("No matching threads (scanned %d threads", scan.scanned)
		if scan.nextCursor != "" {
			msg += fmt.Sprintf("; more may follow, continue with cursor=%s", scan.nextCursor)
		}
		return mcp.NewToolResultText(msg + ")"), nil
	}

	rows := h.renderThreadRows(ctx, scan.threads, params.includeReplies)
	if len(rows) > 0 {
		rows[len(rows)-1].Cursor = scan.nextCursor
	}

	csvBytes, err := gocsv.MarshalBytes(&rows)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal threads: %v", err)
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

func (h *ThreadsHandler) parseThreadsParams(ctx context.Context, request mcp.CallToolRequest) (*threadsParams, error) {
	filter := strings.ToLower(strings.TrimSpace(request.GetString("filter", threadsFilterUnread)))
	if filter == "" {
		filter = threadsFilterUnread
	}
	if filter != threadsFilterUnread && filter != threadsFilterAll {
		return nil, fmt.Errorf("filter must be %q or %q, got %q", threadsFilterUnread, threadsFilterAll, filter)
	}

	limit := request.GetInt("limit", threadsDefaultLimit)
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be a positive number, got %d", limit)
	}
	if limit > threadsMaxLimit {
		limit = threadsMaxLimit
	}

	includeReplies := request.GetInt("include_replies", threadsDefaultReplies)
	if includeReplies < 0 {
		return nil, fmt.Errorf("include_replies must be zero or positive, got %d", includeReplies)
	}
	if includeReplies > threadsMaxReplies {
		includeReplies = threadsMaxReplies
	}

	since := strings.ToLower(strings.TrimSpace(request.GetString("since", threadsDefaultSince)))
	if since == "" {
		since = threadsDefaultSince
	}
	oldest := ""
	if since != threadsSinceUnbounded {
		var err error
		if _, oldest, _, err = limitByExpression(since, threadsDefaultSince); err != nil {
			return nil, fmt.Errorf("since must be a duration like 1d, 7d, 2w, 1m or %q: %v", threadsSinceUnbounded, err)
		}
	}

	cursor := strings.TrimSpace(request.GetString("cursor", ""))
	if cursor != "" && !isSlackTimestamp(cursor) {
		return nil, fmt.Errorf("cursor must be a value from the Cursor column of a previous conversations_threads result, got %q", cursor)
	}

	channelID := ""
	if channel := strings.TrimSpace(request.GetString("channel_id", "")); channel != "" {
		resolved, err := h.convHandler.resolveChannelID(ctx, channel)
		if err != nil {
			h.logger.Error("Channel not found", zap.String("channel", channel), zap.Error(err))
			return nil, err
		}
		channelID = resolved
	}

	return &threadsParams{
		filter:         filter,
		channelID:      channelID,
		oldest:         oldest,
		limit:          limit,
		includeReplies: includeReplies,
		cursor:         cursor,
	}, nil
}

// collectThreads pages through the Threads view (newest activity first) and
// returns up to params.limit threads matching params.filter, stopping at the
// time bound (params.oldest), the end of the view, or threadsMaxPages. The
// returned cursor lets the caller continue exactly where the scan stopped.
func collectThreads(ctx context.Context, fetch threadsPageFetcher, params *threadsParams) (*threadsScan, error) {
	scan := &threadsScan{}
	cursor := params.cursor

	for scan.pages < threadsMaxPages {
		resp, err := fetch(ctx, cursor)
		if err != nil {
			return nil, err
		}
		scan.pages++
		if len(resp.Threads) == 0 {
			scan.nextCursor = ""
			return scan, nil
		}

		for _, t := range resp.Threads {
			latest := threadLatestActivity(t)

			// Threads are ordered by latest activity descending; once we are
			// past the time bound nothing further can match.
			if params.oldest != "" && latest < params.oldest {
				scan.nextCursor = ""
				return scan, nil
			}
			scan.scanned++

			if params.filter == threadsFilterUnread && len(t.UnreadReplies) == 0 {
				continue
			}
			scan.threads = append(scan.threads, t)
			if len(scan.threads) >= params.limit {
				// Continue from this thread's activity timestamp: getView returns
				// threads strictly older than current_ts.
				scan.nextCursor = latest
				return scan, nil
			}
		}

		if !resp.HasMore {
			scan.nextCursor = ""
			return scan, nil
		}
		next := threadLatestActivity(resp.Threads[len(resp.Threads)-1])
		for _, t := range resp.Threads {
			if l := threadLatestActivity(t); l < next {
				next = l
			}
		}
		if next == cursor {
			// Defensive: no progress possible.
			scan.nextCursor = ""
			return scan, nil
		}
		cursor = next
	}

	scan.hitPageCap = true
	scan.nextCursor = cursor
	return scan, nil
}

// threadLatestActivity is the timestamp the Threads view orders by.
func threadLatestActivity(t edge.ThreadView) string {
	if t.RootMsg.LatestReply != "" {
		return t.RootMsg.LatestReply
	}
	return t.RootMsg.Timestamp
}

// renderThreadRows converts threads into CSV rows, resolving user names and
// message text through the same pipeline as the other conversation tools.
func (h *ThreadsHandler) renderThreadRows(ctx context.Context, threads []edge.ThreadView, includeReplies int) []ThreadRow {
	channelsMaps := h.apiProvider.ProvideChannelsMaps()
	rows := make([]ThreadRow, 0, len(threads))

	for _, t := range threads {
		channelID := t.RootMsg.Channel
		channelName := channelID
		if cached, ok := channelsMaps.Channels[channelID]; ok {
			channelName = cached.Name
		}

		row := ThreadRow{
			Channel:         channelName,
			ChannelID:       channelID,
			ThreadTs:        t.RootMsg.Timestamp,
			ReplyCount:      t.RootMsg.ReplyCount,
			UnreadReplies:   len(t.UnreadReplies),
			LatestReplyTime: slackTsToISO(t.RootMsg.LatestReply),
			LastRead:        slackTsToISO(t.RootMsg.LastRead),
		}

		if root := h.convHandler.convertMessagesFromHistory(ctx, []slack.Message{t.RootMsg}, channelID, true); len(root) > 0 {
			row.RootUser = messageAuthor(root[0])
			row.RootTime = root[0].Time
			row.RootText = root[0].Text
		} else {
			row.RootTime = slackTsToISO(t.RootMsg.Timestamp)
		}

		if includeReplies > 0 {
			replies := t.UnreadReplies
			if len(replies) == 0 {
				replies = t.LatestReplies
			}
			if len(replies) > includeReplies {
				replies = replies[len(replies)-includeReplies:]
			}
			rendered := h.convHandler.convertMessagesFromHistory(ctx, replies, channelID, true)
			parts := make([]string, 0, len(rendered))
			for _, m := range rendered {
				parts = append(parts, fmt.Sprintf("%s %s: %s", m.Time, messageAuthor(m), m.Text))
			}
			row.Replies = strings.Join(parts, threadsRepliesJoin)
		}

		rows = append(rows, row)
	}
	return rows
}

func messageAuthor(m Message) string {
	switch {
	case m.RealName != "":
		return m.RealName
	case m.UserName != "":
		return m.UserName
	case m.BotName != "":
		return m.BotName
	default:
		return m.UserID
	}
}

// slackTsToISO formats a Slack timestamp as RFC3339, or "" when empty/invalid.
func slackTsToISO(ts string) string {
	if ts == "" {
		return ""
	}
	iso, err := text.TimestampToIsoRFC3339(ts)
	if err != nil {
		return ""
	}
	return iso
}

// isSlackTimestamp reports whether s looks like a Slack timestamp ("1234567890.123456").
func isSlackTimestamp(s string) bool {
	sec, frac, ok := strings.Cut(s, ".")
	if !ok || sec == "" || frac == "" {
		return false
	}
	for _, r := range sec + frac {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
