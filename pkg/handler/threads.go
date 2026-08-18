package handler

import (
	"context"
	"fmt"
	"strconv"
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
	since          string // as given/defaulted, for messages ("all" = unbounded)
	oldest         string // Slack ts; threads whose latest activity is older stop the scan ("" = unbounded)
	limit          int
	includeReplies int
	cursor         string
}

// threadsPageFetcher fetches one page of the Threads view for a cursor.
type threadsPageFetcher func(ctx context.Context, cursor string) (edge.ThreadsViewResponse, error)

// threadsScan is the outcome of collectThreads.
type threadsScan struct {
	threads      []edge.ThreadView
	nextCursor   string // "" when the scan is exhausted (no more pages, or the time bound was reached)
	scanned      int    // threads looked at within the time window, matched or not
	pages        int
	hitPageCap   bool // stopped after threadsMaxPages; nextCursor continues the scan
	hitTimeBound bool // stopped because the next thread's latest activity is older than params.oldest
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
		return mcp.NewToolResultText(noThreadsMessage(scan, params)), nil
	}

	channelsMaps := h.apiProvider.ProvideChannelsMaps()
	channelName := func(id string) string {
		if cached, ok := channelsMaps.Channels[id]; ok {
			return cached.Name
		}
		return id
	}
	render := func(ctx context.Context, msgs []slack.Message, channelID string) []Message {
		return h.convHandler.convertMessagesFromHistory(ctx, msgs, channelID, true)
	}
	rows := buildThreadRows(ctx, scan.threads, params, scan.nextCursor, channelName, render)

	csvBytes, err := gocsv.MarshalBytes(&rows)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal threads: %v", err)
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

// noThreadsMessage explains an empty result: what was scanned and why the scan stopped.
func noThreadsMessage(scan *threadsScan, params *threadsParams) string {
	what := "threads with unread replies"
	if params.filter == threadsFilterAll {
		what = "threads"
	}
	msg := fmt.Sprintf("No %s found (scanned %d threads", what, scan.scanned)
	switch {
	case scan.hitTimeBound:
		msg += fmt.Sprintf("; the time window since=%s ended the scan — use a larger since, or since=all", params.since)
	case scan.hitPageCap:
		msg += fmt.Sprintf("; stopped after %d threads, continue with cursor=%s", scan.scanned, scan.nextCursor)
	case scan.nextCursor != "":
		msg += fmt.Sprintf("; more may follow, continue with cursor=%s", scan.nextCursor)
	default:
		msg += "; end of the Threads view reached"
	}
	return msg + ")"
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
			return nil, fmt.Errorf("since must be a duration like 1d, 7d, 2w, 1m (d=days, w=weeks, m=months) or %q: %v", threadsSinceUnbounded, err)
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
		since:          since,
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

		for i, t := range resp.Threads {
			latest := threadLatestActivity(t)
			if latest == "" {
				// Malformed entry (no timestamps at all): nothing to order or filter on.
				continue
			}

			// Threads are ordered by latest activity descending; once we are
			// past the time bound nothing further can match.
			if params.oldest != "" && slackTsBefore(latest, params.oldest) {
				scan.hitTimeBound = true
				scan.nextCursor = ""
				return scan, nil
			}
			scan.scanned++

			if params.filter == threadsFilterUnread && len(t.UnreadReplies) == 0 {
				continue
			}
			scan.threads = append(scan.threads, t)
			if len(scan.threads) >= params.limit {
				if i == len(resp.Threads)-1 && !resp.HasMore {
					// Nothing left in the view.
					scan.nextCursor = ""
				} else {
					// Continue from this thread's activity timestamp: getView
					// returns threads strictly older than current_ts.
					scan.nextCursor = latest
				}
				return scan, nil
			}
		}

		if !resp.HasMore {
			scan.nextCursor = ""
			return scan, nil
		}
		next := ""
		for _, t := range resp.Threads {
			if l := threadLatestActivity(t); l != "" && (next == "" || slackTsBefore(l, next)) {
				next = l
			}
		}
		if next == "" || next == cursor {
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

// slackTsBefore reports whether Slack timestamp a is strictly older than b,
// comparing numerically (seconds, then fraction) rather than as strings, so
// timestamps of different widths compare correctly.
func slackTsBefore(a, b string) bool {
	aSec, aFrac, _ := strings.Cut(a, ".")
	bSec, bFrac, _ := strings.Cut(b, ".")
	as, aErr := strconv.ParseInt(aSec, 10, 64)
	bs, bErr := strconv.ParseInt(bSec, 10, 64)
	if aErr != nil || bErr != nil {
		return a < b // not Slack timestamps; fall back to string order
	}
	if as != bs {
		return as < bs
	}
	for len(aFrac) < len(bFrac) {
		aFrac += "0"
	}
	for len(bFrac) < len(aFrac) {
		bFrac += "0"
	}
	return aFrac < bFrac
}

// threadLatestActivity is the timestamp the Threads view orders by.
func threadLatestActivity(t edge.ThreadView) string {
	if t.RootMsg.LatestReply != "" {
		return t.RootMsg.LatestReply
	}
	return t.RootMsg.Timestamp
}

// threadMessageRenderer converts raw Slack messages of one channel into Message
// rows (user names resolved, text processed) — in production this is
// ConversationsHandler.convertMessagesFromHistory.
type threadMessageRenderer func(ctx context.Context, msgs []slack.Message, channelID string) []Message

// buildThreadRows converts threads into CSV rows. channelName maps a channel ID
// to its display name; the last row carries nextCursor.
func buildThreadRows(ctx context.Context, threads []edge.ThreadView, params *threadsParams, nextCursor string, channelName func(string) string, render threadMessageRenderer) []ThreadRow {
	rows := make([]ThreadRow, 0, len(threads))

	for _, t := range threads {
		channelID := t.RootMsg.Channel
		if channelID == "" {
			// Slack normally sets root_msg.channel; fall back to the requested channel.
			channelID = params.channelID
		}

		row := ThreadRow{
			Channel:         channelName(channelID),
			ChannelID:       channelID,
			ThreadTs:        t.RootMsg.Timestamp,
			ReplyCount:      t.RootMsg.ReplyCount,
			UnreadReplies:   len(t.UnreadReplies),
			LatestReplyTime: slackTsToISO(t.RootMsg.LatestReply),
			LastRead:        slackTsToISO(t.RootMsg.LastRead),
			RootTime:        slackTsToISO(t.RootMsg.Timestamp),
		}

		if root := render(ctx, []slack.Message{t.RootMsg}, channelID); len(root) > 0 {
			row.RootUser = messageAuthor(root[0])
			row.RootTime = root[0].Time
			row.RootText = root[0].Text
		}

		if params.includeReplies > 0 {
			replies := t.UnreadReplies
			if len(replies) == 0 {
				replies = t.LatestReplies
			}
			if len(replies) > params.includeReplies {
				replies = replies[len(replies)-params.includeReplies:]
			}
			rendered := render(ctx, replies, channelID)
			parts := make([]string, 0, len(rendered))
			for _, m := range rendered {
				parts = append(parts, fmt.Sprintf("%s %s: %s", m.Time, messageAuthor(m), m.Text))
			}
			row.Replies = strings.Join(parts, threadsRepliesJoin)
		}

		rows = append(rows, row)
	}
	if len(rows) > 0 {
		rows[len(rows)-1].Cursor = nextCursor
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

// isSlackTimestamp reports whether s is a Slack timestamp ("1234567890.123456":
// digits, a dot, exactly six fractional digits — anything else Slack rejects).
func isSlackTimestamp(s string) bool {
	sec, frac, ok := strings.Cut(s, ".")
	if !ok || sec == "" || len(frac) != 6 {
		return false
	}
	for _, r := range sec + frac {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
