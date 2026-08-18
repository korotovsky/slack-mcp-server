package handler

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mkThread builds a Threads-view entry with the given latest activity and unread count.
func mkThread(channel, ts, latestReply string, unread int) edge.ThreadView {
	t := edge.ThreadView{}
	t.RootMsg.Channel = channel
	t.RootMsg.Timestamp = ts
	t.RootMsg.ThreadTimestamp = ts
	t.RootMsg.LatestReply = latestReply
	t.RootMsg.ReplyCount = unread + 1
	for i := 0; i < unread; i++ {
		t.UnreadReplies = append(t.UnreadReplies, slack.Message{})
	}
	return t
}

// fakePager serves pre-built pages keyed by cursor ("" = first page) and records the cursors requested.
type fakePager struct {
	pages   map[string]edge.ThreadsViewResponse
	err     error
	cursors []string
}

func (p *fakePager) fetch(ctx context.Context, cursor string) (edge.ThreadsViewResponse, error) {
	p.cursors = append(p.cursors, cursor)
	if p.err != nil {
		return edge.ThreadsViewResponse{}, p.err
	}
	resp, ok := p.pages[cursor]
	if !ok {
		return edge.ThreadsViewResponse{}, fmt.Errorf("unexpected cursor %q", cursor)
	}
	return resp, nil
}

func TestUnitCollectThreads(t *testing.T) {
	ctx := context.Background()
	// Two pages, newest first. Page 1 ends at latest 1700000600; page 2 is older.
	page1 := edge.ThreadsViewResponse{HasMore: true, Threads: []edge.ThreadView{
		mkThread("C1", "1700000100.000001", "1700000900.000000", 2),
		mkThread("C1", "1700000200.000001", "1700000800.000000", 0),
		mkThread("C2", "1700000300.000001", "1700000700.000000", 1),
		mkThread("C2", "1700000400.000001", "1700000600.000000", 0),
	}}
	page2 := edge.ThreadsViewResponse{HasMore: false, Threads: []edge.ThreadView{
		mkThread("C3", "1600000100.000001", "1600000500.000000", 3),
		mkThread("C3", "1600000200.000001", "", 0), // never replied: activity = its own ts
	}}
	pages := map[string]edge.ThreadsViewResponse{"": page1, "1700000600.000000": page2}

	t.Run("unread filter across pages, exhausted view clears cursor", func(t *testing.T) {
		p := &fakePager{pages: pages}
		scan, err := collectThreads(ctx, p.fetch, &threadsParams{filter: threadsFilterUnread, limit: 20})
		require.NoError(t, err)
		require.Len(t, scan.threads, 3)
		assert.Equal(t, "C1", scan.threads[0].RootMsg.Channel)
		assert.Equal(t, "C3", scan.threads[2].RootMsg.Channel)
		assert.Equal(t, 6, scan.scanned)
		assert.Equal(t, 2, scan.pages)
		assert.Empty(t, scan.nextCursor)
		assert.False(t, scan.hitPageCap)
		assert.Equal(t, []string{"", "1700000600.000000"}, p.cursors, "second page must be requested with the oldest activity of page 1")
	})

	t.Run("filter all returns every thread", func(t *testing.T) {
		p := &fakePager{pages: pages}
		scan, err := collectThreads(ctx, p.fetch, &threadsParams{filter: threadsFilterAll, limit: 20})
		require.NoError(t, err)
		assert.Len(t, scan.threads, 6)
	})

	t.Run("limit reached mid-page: cursor continues from that thread's activity", func(t *testing.T) {
		p := &fakePager{pages: pages}
		scan, err := collectThreads(ctx, p.fetch, &threadsParams{filter: threadsFilterUnread, limit: 1})
		require.NoError(t, err)
		require.Len(t, scan.threads, 1)
		assert.Equal(t, "1700000900.000000", scan.nextCursor)
		assert.Equal(t, 1, scan.pages)
	})

	t.Run("since bound stops the scan and clears the cursor", func(t *testing.T) {
		p := &fakePager{pages: pages}
		scan, err := collectThreads(ctx, p.fetch, &threadsParams{filter: threadsFilterAll, limit: 20, oldest: "1700000650.000000"})
		require.NoError(t, err)
		assert.Len(t, scan.threads, 3, "threads with activity older than the bound are not returned")
		assert.Equal(t, 3, scan.scanned)
		assert.Empty(t, scan.nextCursor)
		assert.True(t, scan.hitTimeBound)
		assert.Equal(t, 1, scan.pages, "the bound was hit on page 1, page 2 must not be fetched")
	})

	t.Run("since bound compares numerically, not lexicographically", func(t *testing.T) {
		// A 9-digit "oldest" (windows reaching before 2001) must not stop the scan.
		p := &fakePager{pages: pages}
		scan, err := collectThreads(ctx, p.fetch, &threadsParams{filter: threadsFilterAll, limit: 20, oldest: "998082000.000000"})
		require.NoError(t, err)
		assert.Len(t, scan.threads, 6)
		assert.False(t, scan.hitTimeBound)
	})

	t.Run("limit reached on the last thread of the last page: no cursor", func(t *testing.T) {
		p := &fakePager{pages: pages}
		scan, err := collectThreads(ctx, p.fetch, &threadsParams{filter: threadsFilterAll, limit: 6})
		require.NoError(t, err)
		assert.Len(t, scan.threads, 6)
		assert.Empty(t, scan.nextCursor, "the view is exhausted; no continuation cursor")
	})

	t.Run("malformed entries without timestamps are skipped, not counted, and do not break the cursor", func(t *testing.T) {
		broken := edge.ThreadsViewResponse{HasMore: true, Threads: []edge.ThreadView{
			mkThread("C1", "", "", 1),
			mkThread("C1", "1700000100.000001", "1700000900.000000", 1),
		}}
		p := &fakePager{pages: map[string]edge.ThreadsViewResponse{"": broken, "1700000900.000000": {HasMore: false}}}
		scan, err := collectThreads(ctx, p.fetch, &threadsParams{filter: threadsFilterUnread, limit: 20, oldest: "1600000000.000000"})
		require.NoError(t, err)
		assert.Len(t, scan.threads, 1)
		assert.Equal(t, 1, scan.scanned)
		assert.Equal(t, []string{"", "1700000900.000000"}, p.cursors)
	})

	t.Run("cursor param is used for the first fetch", func(t *testing.T) {
		p := &fakePager{pages: pages}
		scan, err := collectThreads(ctx, p.fetch, &threadsParams{filter: threadsFilterAll, limit: 20, cursor: "1700000600.000000"})
		require.NoError(t, err)
		assert.Equal(t, []string{"1700000600.000000"}, p.cursors)
		assert.Len(t, scan.threads, 2)
	})

	t.Run("empty page ends the scan", func(t *testing.T) {
		p := &fakePager{pages: map[string]edge.ThreadsViewResponse{"": {HasMore: true}}}
		scan, err := collectThreads(ctx, p.fetch, &threadsParams{filter: threadsFilterAll, limit: 20})
		require.NoError(t, err)
		assert.Empty(t, scan.threads)
		assert.Empty(t, scan.nextCursor)
	})

	t.Run("fetch error is propagated", func(t *testing.T) {
		p := &fakePager{err: errors.New("not_allowed_token_type")}
		_, err := collectThreads(ctx, p.fetch, &threadsParams{filter: threadsFilterAll, limit: 20})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not_allowed_token_type")
	})

	t.Run("page cap: stops with a cursor to continue", func(t *testing.T) {
		// Endless view: every page has one read thread and says has_more.
		endless := &fakePager{pages: map[string]edge.ThreadsViewResponse{}}
		ts := int64(1700000000)
		cursor := ""
		for i := 0; i <= threadsMaxPages; i++ {
			latest := fmt.Sprintf("%d.000000", ts-int64(i))
			endless.pages[cursor] = edge.ThreadsViewResponse{HasMore: true, Threads: []edge.ThreadView{mkThread("C1", "1600000000.000001", latest, 0)}}
			cursor = latest
		}
		scan, err := collectThreads(ctx, endless.fetch, &threadsParams{filter: threadsFilterUnread, limit: 20})
		require.NoError(t, err)
		assert.True(t, scan.hitPageCap)
		assert.Equal(t, threadsMaxPages, scan.pages)
		assert.NotEmpty(t, scan.nextCursor)
		assert.Empty(t, scan.threads)
		assert.Equal(t, threadsMaxPages, scan.scanned)
	})
}

func TestUnitThreadsParseParams(t *testing.T) {
	h := &ThreadsHandler{logger: zap.NewNop()}
	ctx := context.Background()
	newReq := func(args map[string]any) mcp.CallToolRequest {
		req := mcp.CallToolRequest{}
		req.Params.Name = "conversations_threads"
		req.Params.Arguments = args
		return req
	}

	t.Run("defaults", func(t *testing.T) {
		p, err := h.parseThreadsParams(ctx, newReq(map[string]any{}))
		require.NoError(t, err)
		assert.Equal(t, threadsFilterUnread, p.filter)
		assert.Equal(t, threadsDefaultLimit, p.limit)
		assert.Equal(t, threadsDefaultReplies, p.includeReplies)
		assert.NotEmpty(t, p.oldest, "default since=30d must produce a time bound")
		assert.Empty(t, p.channelID)
		assert.Empty(t, p.cursor)
	})

	t.Run("since=all removes the time bound; durations are accepted", func(t *testing.T) {
		p, err := h.parseThreadsParams(ctx, newReq(map[string]any{"since": "all"}))
		require.NoError(t, err)
		assert.Empty(t, p.oldest)
		for _, v := range []string{"1d", "7d", "2w", "1m", " 3D "} {
			p, err := h.parseThreadsParams(ctx, newReq(map[string]any{"since": v}))
			require.NoError(t, err, "since=%q", v)
			assert.NotEmpty(t, p.oldest)
		}
	})

	t.Run("invalid values are rejected", func(t *testing.T) {
		cases := []map[string]any{
			{"filter": "starred"},
			{"limit": 0},
			{"limit": -3},
			{"include_replies": -1},
			{"since": "yesterday"},
			{"since": "7x"},
			{"cursor": "not-a-ts"},
		}
		for _, args := range cases {
			_, err := h.parseThreadsParams(ctx, newReq(args))
			require.Error(t, err, "args %v must be rejected", args)
		}
	})

	t.Run("limits are clamped, filter is case-insensitive, cursor accepted", func(t *testing.T) {
		p, err := h.parseThreadsParams(ctx, newReq(map[string]any{"filter": "ALL", "limit": 5000, "include_replies": 99, "cursor": "1700000600.000000"}))
		require.NoError(t, err)
		assert.Equal(t, threadsFilterAll, p.filter)
		assert.Equal(t, threadsMaxLimit, p.limit)
		assert.Equal(t, threadsMaxReplies, p.includeReplies)
		assert.Equal(t, "1700000600.000000", p.cursor)
	})
}

func TestUnitThreadsHelpers(t *testing.T) {
	assert.True(t, isSlackTimestamp("1700000600.000000"))
	assert.False(t, isSlackTimestamp("1700000600"))
	assert.False(t, isSlackTimestamp("abc.def"))
	assert.False(t, isSlackTimestamp("1700000600."))
	assert.False(t, isSlackTimestamp(".000000"))
	assert.False(t, isSlackTimestamp("1700000600.0002"), "Slack requires exactly six fractional digits")
	assert.False(t, isSlackTimestamp("1700000600.00020000"))

	assert.True(t, slackTsBefore("998082000.000000", "1700000900.000000"), "9-digit seconds are older, not lexicographically greater")
	assert.False(t, slackTsBefore("1700000900.000000", "998082000.000000"))
	assert.True(t, slackTsBefore("1700000900.000000", "1700000900.000001"))
	assert.False(t, slackTsBefore("1700000900.000000", "1700000900.000000"))
	assert.False(t, slackTsBefore("1700000900.000000", "1700000900.0"), "equal values with different fraction widths")

	assert.Equal(t, "", slackTsToISO(""))
	assert.Equal(t, "", slackTsToISO("garbage"))
	assert.NotEmpty(t, slackTsToISO("1700000600.000000"))

	assert.Equal(t, "Real", messageAuthor(Message{RealName: "Real", UserName: "user", UserID: "U1"}))
	assert.Equal(t, "user", messageAuthor(Message{UserName: "user", UserID: "U1"}))
	assert.Equal(t, "bot", messageAuthor(Message{BotName: "bot", UserID: "B1"}))
	assert.Equal(t, "U1", messageAuthor(Message{UserID: "U1"}))

	withReply := mkThread("C1", "1.000000", "2.000000", 0)
	assert.Equal(t, "2.000000", threadLatestActivity(withReply))
	noReply := mkThread("C1", "1.000000", "", 0)
	assert.Equal(t, "1.000000", threadLatestActivity(noReply))
}

func TestUnitBuildThreadRows(t *testing.T) {
	ctx := context.Background()
	msg := func(ts, user, text string) slack.Message {
		m := slack.Message{}
		m.Timestamp = ts
		m.User = user
		m.Text = text
		return m
	}
	// A renderer that mimics the production pipeline closely enough: RFC3339-ish time, resolved names, text passthrough.
	render := func(ctx context.Context, msgs []slack.Message, channelID string) []Message {
		out := make([]Message, 0, len(msgs))
		for _, m := range msgs {
			out = append(out, Message{MsgID: m.Timestamp, UserID: m.User, RealName: "Name " + m.User, Text: m.Text, Time: "T" + m.Timestamp, Channel: channelID})
		}
		return out
	}
	channelName := func(id string) string {
		if id == "C1" {
			return "#general"
		}
		return id
	}

	unreadThread := edge.ThreadView{}
	unreadThread.RootMsg = msg("1700000100.000001", "UROOT", "root, with \"quotes\"\nand a newline")
	unreadThread.RootMsg.Channel = "C1"
	unreadThread.RootMsg.ThreadTimestamp = "1700000100.000001"
	unreadThread.RootMsg.ReplyCount = 5
	unreadThread.RootMsg.LatestReply = "1700000500.000000"
	unreadThread.RootMsg.LastRead = "1700000200.000000"
	unreadThread.LatestReplies = []slack.Message{msg("1700000150.000000", "UA", "read reply")}
	unreadThread.UnreadReplies = []slack.Message{
		msg("1700000300.000000", "UB", "unread 1"),
		msg("1700000400.000000", "UC", "unread 2, with, commas"),
		msg("1700000500.000000", "UD", "unread 3"),
	}

	readThread := edge.ThreadView{}
	readThread.RootMsg = msg("1600000100.000001", "UROOT2", "read root")
	readThread.RootMsg.Channel = "" // missing channel: falls back to the requested one
	readThread.RootMsg.ReplyCount = 1
	readThread.RootMsg.LatestReply = "1600000200.000000"
	readThread.LatestReplies = []slack.Message{msg("1600000200.000000", "UE", "latest only")}

	params := &threadsParams{includeReplies: 2, channelID: "C9"}
	rows := buildThreadRows(ctx, []edge.ThreadView{unreadThread, readThread}, params, "1600000200.000000", channelName, render)
	require.Len(t, rows, 2)

	r0 := rows[0]
	assert.Equal(t, "#general", r0.Channel)
	assert.Equal(t, "C1", r0.ChannelID)
	assert.Equal(t, "1700000100.000001", r0.ThreadTs)
	assert.Equal(t, "Name UROOT", r0.RootUser)
	assert.Equal(t, "T1700000100.000001", r0.RootTime)
	assert.Contains(t, r0.RootText, "quotes")
	assert.Equal(t, 5, r0.ReplyCount)
	assert.Equal(t, 3, r0.UnreadReplies)
	assert.NotEmpty(t, r0.LatestReplyTime)
	assert.NotEmpty(t, r0.LastRead)
	// unread replies preferred over latest, and only the last include_replies of them
	assert.Equal(t, "T1700000400.000000 Name UC: unread 2, with, commas || T1700000500.000000 Name UD: unread 3", r0.Replies)
	assert.Empty(t, r0.Cursor, "only the last row carries the cursor")

	r1 := rows[1]
	assert.Equal(t, "C9", r1.ChannelID, "missing root_msg.channel falls back to the requested channel_id")
	assert.Equal(t, "C9", r1.Channel)
	assert.Equal(t, 0, r1.UnreadReplies)
	assert.Equal(t, "T1600000200.000000 Name UE: latest only", r1.Replies, "read thread shows latest replies")
	assert.Equal(t, "1600000200.000000", r1.Cursor)

	t.Run("include_replies=0 omits reply text; renderer dropping the root keeps ts-derived time", func(t *testing.T) {
		none := func(ctx context.Context, msgs []slack.Message, channelID string) []Message { return nil }
		rows := buildThreadRows(ctx, []edge.ThreadView{unreadThread}, &threadsParams{includeReplies: 0}, "", channelName, none)
		require.Len(t, rows, 1)
		assert.Empty(t, rows[0].Replies)
		assert.Empty(t, rows[0].RootUser)
		assert.NotEmpty(t, rows[0].RootTime, "falls back to the root ts when the renderer returns nothing")
		assert.Empty(t, rows[0].Cursor)
	})

	t.Run("CSV round-trips quotes, commas and newlines", func(t *testing.T) {
		out, err := gocsv.MarshalString(&rows)
		require.NoError(t, err)
		back := []ThreadRow{}
		require.NoError(t, gocsv.UnmarshalString(out, &back))
		require.Len(t, back, 2)
		assert.Equal(t, rows[0].RootText, back[0].RootText)
		assert.Equal(t, rows[0].Replies, back[0].Replies)
		assert.Equal(t, rows[1].Cursor, back[1].Cursor)
	})
}

func TestUnitNoThreadsMessage(t *testing.T) {
	p := &threadsParams{filter: threadsFilterUnread, since: "30d"}
	assert.Equal(t, "No threads with unread replies found (scanned 12 threads; the time window since=30d ended the scan — use a larger since, or since=all)",
		noThreadsMessage(&threadsScan{scanned: 12, hitTimeBound: true}, p))
	assert.Equal(t, "No threads with unread replies found (scanned 500 threads; stopped after 500 threads, continue with cursor=1700000000.000000)",
		noThreadsMessage(&threadsScan{scanned: 500, hitPageCap: true, nextCursor: "1700000000.000000"}, p))
	assert.Equal(t, "No threads found (scanned 3 threads; end of the Threads view reached)",
		noThreadsMessage(&threadsScan{scanned: 3}, &threadsParams{filter: threadsFilterAll, since: "all"}))
	assert.Contains(t, noThreadsMessage(&threadsScan{scanned: 1, nextCursor: "1.000000"}, p), "continue with cursor=1.000000")
}
