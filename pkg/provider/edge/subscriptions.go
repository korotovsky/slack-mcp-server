package edge

import (
	"context"
	"runtime/trace"
)

// subscriptions.thread.* API — internal Slack APIs backing the "Threads" view
// (thread subscriptions and per-thread read state).
// Only accessible with browser session tokens (xoxc/xoxd).
//
// Known endpoints:
//   - subscriptions.thread.mark     — set the read cursor of a thread (channel, thread_ts, ts)
//   - subscriptions.thread.get      — list subscribed thread_ts values in a channel
//   - subscriptions.thread.getView  — the "Threads" view (threads with unread counts)
//   - subscriptions.thread.add / remove — follow / unfollow a thread
//   - subscriptions.thread.clearAll — mark ALL threads as read (dangerous: no required params)

type subscriptionsThreadMarkForm struct {
	BaseRequest
	Channel  string `json:"channel"`
	ThreadTs string `json:"thread_ts"`
	Ts       string `json:"ts"`
	WebClientFields
}

// SubscriptionsThreadMark marks a thread as read up to ts (subscriptions.thread.mark),
// i.e. sets the thread's last_read cursor to ts. The cursor only moves forward:
// passing a ts older than the current last_read is a no-op. To mark a whole
// thread as read, pass the ts of its latest reply.
func (cl *Client) SubscriptionsThreadMark(ctx context.Context, channel, threadTs, ts string) error {
	ctx, task := trace.NewTask(ctx, "SubscriptionsThreadMark")
	defer task.End()

	form := subscriptionsThreadMarkForm{
		BaseRequest:     BaseRequest{Token: cl.token},
		Channel:         channel,
		ThreadTs:        threadTs,
		Ts:              ts,
		WebClientFields: webclientReason("subscriptions-thread-api/markThreadRead"),
	}

	resp, err := cl.PostForm(ctx, "subscriptions.thread.mark", values(form, true))
	if err != nil {
		return err
	}
	r := baseResponse{}
	if err := cl.ParseResponse(&r, resp); err != nil {
		return err
	}
	if err := r.validate("subscriptions.thread.mark"); err != nil {
		return err
	}
	return nil
}
