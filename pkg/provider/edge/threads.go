package edge

import (
	"context"
	"runtime/trace"

	"github.com/slack-go/slack"
)

// subscriptions.thread.getView — the internal Slack API behind the "Threads"
// view: threads the user is subscribed to, newest activity first, with the
// user's unread state per thread. Only accessible with browser session tokens
// (xoxc/xoxd).
//
// Observed behaviour (2026-08): the page size is capped at 10 threads whatever
// `limit` says; `current_ts` is the pagination cursor (threads with latest
// activity strictly older than it are returned); `channel_id` filters
// server-side; there is no server-side "unread only" filter.

type subscriptionsThreadGetViewForm struct {
	BaseRequest
	Limit     int    `json:"limit"`
	CurrentTs string `json:"current_ts,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	WebClientFields
}

// ThreadsViewResponse is the response of subscriptions.thread.getView.
type ThreadsViewResponse struct {
	baseResponse
	Threads            []ThreadView `json:"threads"`
	TotalUnreadReplies int          `json:"total_unread_replies"`
	NewThreadsCount    int          `json:"new_threads_count"`
	HasMore            bool         `json:"has_more"`
	MaxTs              string       `json:"max_ts"`
}

// ThreadView is one thread of the "Threads" view. RootMsg carries the thread's
// parent message including the user's per-thread read state (LastRead,
// LatestReply, ReplyCount, Subscribed). UnreadReplies are the replies the user
// has not read yet (empty when the thread is fully read); LatestReplies are the
// most recent replies Slack includes for context.
type ThreadView struct {
	RootMsg       slack.Message   `json:"root_msg"`
	LatestReplies []slack.Message `json:"latest_replies"`
	UnreadReplies []slack.Message `json:"unread_replies"`
}

// SubscriptionsThreadGetView fetches one page of the "Threads" view. Pass the
// previous page's cursor (the smallest latest-activity timestamp seen) as
// currentTs to get older threads, and channelID to restrict to one channel.
func (cl *Client) SubscriptionsThreadGetView(ctx context.Context, currentTs string, limit int, channelID string) (ThreadsViewResponse, error) {
	ctx, task := trace.NewTask(ctx, "SubscriptionsThreadGetView")
	defer task.End()

	form := subscriptionsThreadGetViewForm{
		BaseRequest:     BaseRequest{Token: cl.token},
		Limit:           limit,
		CurrentTs:       currentTs,
		ChannelID:       channelID,
		WebClientFields: webclientReason("fetch-threads-view"),
	}

	resp, err := cl.PostForm(ctx, "subscriptions.thread.getView", values(form, true))
	if err != nil {
		return ThreadsViewResponse{}, err
	}
	r := ThreadsViewResponse{}
	if err := cl.ParseResponse(&r, resp); err != nil {
		return ThreadsViewResponse{}, err
	}
	if err := r.validate("subscriptions.thread.getView"); err != nil {
		return ThreadsViewResponse{}, err
	}
	return r, nil
}
