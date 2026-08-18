package handler

import (
	"context"
	"errors"
	"fmt"
	"testing"

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
		assert.Equal(t, 1, scan.pages, "the bound was hit on page 1, page 2 must not be fetched")
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
