package edge

import (
	"context"
	"runtime/trace"

	"github.com/korotovsky/slack-mcp-server/pkg/limiter"
	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge/fasttime"
	"github.com/rusq/slack"
)

// client.* API

type clientCountsForm struct {
	BaseRequest
	ThreadCountsByChannel bool `json:"thread_counts_by_channel"`
	OrgWideAware          bool `json:"org_wide_aware"`
	IncludeFileChannels   bool `json:"include_file_channels"`
	WebClientFields
}

type ClientCountsResponse struct {
	baseResponse
	Channels []ChannelSnapshot `json:"channels,omitempty"`
	MPIMs    []ChannelSnapshot `json:"mpims,omitempty"`
	IMs      []ChannelSnapshot `json:"ims,omitempty"`
}

type ChannelSnapshot struct {
	ID             string        `json:"id"`
	LastRead       fasttime.Time `json:"last_read"`
	Latest         fasttime.Time `json:"latest"`
	HistoryInvalid fasttime.Time `json:"history_invalid"`
	MentionCount   int           `json:"mention_count"`
	HasUnreads     bool          `json:"has_unreads"`
}

func (cl *Client) ClientCounts(ctx context.Context) (ClientCountsResponse, error) {
	ctx, task := trace.NewTask(ctx, "ClientCounts")
	defer task.End()

	form := clientCountsForm{
		BaseRequest:           BaseRequest{Token: cl.token},
		ThreadCountsByChannel: true,
		OrgWideAware:          true,
		IncludeFileChannels:   true,
		WebClientFields:       webclientReason("client-counts-api/fetchClientCounts"),
	}

	resp, err := cl.PostForm(ctx, "client.counts", values(form, true))
	if err != nil {
		return ClientCountsResponse{}, err
	}
	r := ClientCountsResponse{}
	if err := cl.ParseResponse(&r, resp); err != nil {
		return ClientCountsResponse{}, err
	}
	if err := r.validate("client.counts"); err != nil {
		return ClientCountsResponse{}, err
	}
	return r, nil
}

type clientDMsForm struct {
	BaseRequest
	Count          int    `json:"count"`
	IncludeClosed  bool   `json:"include_closed"`
	IncludeChannel bool   `json:"include_channel"`
	ExcludeBots    bool   `json:"exclude_bots"`
	Cursor         string `json:"cursor,omitempty"`
	WebClientFields
}

type clientDMsResponse struct {
	baseResponse
	IMs   []ClientDM `json:"ims,omitempty"`
	MPIMs []ClientDM `json:"mpims,omitempty"` //TODO
}

type ClientDM struct {
	ID string `json:"id"`
	// Message slack.Message `json:"message,omitempty"`
	Channel IM            `json:"channel,omitempty"`
	Latest  fasttime.Time `json:"latest,omitempty"` // i.e. "1710632873.037269"
}

type IM struct {
	ID               string         `json:"id"`
	Created          slack.JSONTime `json:"created"`
	IsFrozen         bool           `json:"is_frozen"`
	IsArchived       bool           `json:"is_archived"`
	IsIM             bool           `json:"is_im"`
	IsOrgShared      bool           `json:"is_org_shared"`
	ContextTeamID    string         `json:"context_team_id"`
	Updated          slack.JSONTime `json:"updated"`
	IsShared         bool           `json:"is_shared"`
	IsExtShared      bool           `json:"is_ext_shared"`
	User             string         `json:"user"`
	LastRead         fasttime.Time  `json:"last_read"`
	Latest           fasttime.Time  `json:"latest"`
	IsOpen           bool           `json:"is_open"`
	SharedTeamIds    []string       `json:"shared_team_ids"`
	ConnectedTeamIds []string       `json:"connected_team_ids"`
}

func (c IM) SlackChannel() slack.Channel {
	// Add Members array with just the User for IM channels
	var members []string
	if c.User != "" {
		members = []string{c.User}
	}

	return slack.Channel{
		GroupConversation: slack.GroupConversation{
			Conversation: slack.Conversation{
				ID:          c.ID,
				Created:     c.Created,
				IsIM:        c.IsIM,
				IsOrgShared: c.IsOrgShared,
				User:        c.User,
				LastRead:    c.LastRead.SlackString(),
			},
			IsArchived: c.IsArchived,
			Members:    members,
		},
	}

}

func (cl *Client) ClientDMs(ctx context.Context) ([]ClientDM, error) {
	form := clientDMsForm{
		BaseRequest:     BaseRequest{Token: cl.token},
		Count:           250,
		IncludeClosed:   true,
		IncludeChannel:  true,
		ExcludeBots:     false,
		Cursor:          "",
		WebClientFields: webclientReason("dms-tab-populate"),
	}
	lim := limiter.Tier2boost.Limiter()
	var IMs []ClientDM
	for {
		resp, err := cl.PostFormRaw(ctx, cl.webapiURL("client.dms"), values(form, true))
		if err != nil {
			return nil, err
		}
		r := clientDMsResponse{}
		if err := cl.ParseResponse(&r, resp); err != nil {
			return nil, err
		}
		IMs = append(IMs, r.IMs...)
		if r.ResponseMetadata.NextCursor == "" {
			break
		}
		form.Cursor = r.ResponseMetadata.NextCursor
		if err := lim.Wait(ctx); err != nil {
			return nil, err
		}
	}
	return IMs, nil
}

// activity.feed API

type activityFeedForm struct {
	BaseRequest
	Limit               int    `json:"limit"`
	Types               string `json:"types"`
	Mode                string `json:"mode"`
	ArchiveOnly         bool   `json:"archive_only"`
	UnreadOnly          bool   `json:"unread_only"`
	PriorityOnly        bool   `json:"priority_only"`
	OnlySalesforceChans bool   `json:"only_salesforce_channels"`
	IsActivityInbox     bool   `json:"is_activity_inbox"`
	WebClientFields
}

type ActivityFeedResponse struct {
	baseResponse
	Items []ActivityFeedItem `json:"items,omitempty"`
}

type ActivityFeedItem struct {
	IsUnread bool              `json:"is_unread"`
	FeedTs   string            `json:"feed_ts"`
	Key      string            `json:"key"`
	Item     ActivityItemInner `json:"item"`
}

type ActivityItemInner struct {
	Type       string              `json:"type"`
	BundleInfo *ActivityBundleInfo `json:"bundle_info,omitempty"`
	Message    *ActivityMessage    `json:"message,omitempty"`
}

type ActivityBundleInfo struct {
	Payload struct {
		ThreadEntry struct {
			ChannelID      string `json:"channel_id"`
			ThreadTs       string `json:"thread_ts"`
			LatestTs       string `json:"latest_ts"`
			UnreadMsgCount int    `json:"unread_msg_count"`
			MinUnreadTs    string `json:"min_unread_ts"`
		} `json:"thread_entry"`
	} `json:"payload"`
}

type ActivityMessage struct {
	Ts           string `json:"ts"`
	Channel      string `json:"channel"`
	ThreadTs     string `json:"thread_ts"`
	AuthorUserID string `json:"author_user_id"`
	IsBroadcast  bool   `json:"is_broadcast"`
}

func (cl *Client) ActivityFeed(ctx context.Context, limit int) (ActivityFeedResponse, error) {
	ctx, task := trace.NewTask(ctx, "ActivityFeed")
	defer task.End()

	form := activityFeedForm{
		BaseRequest: BaseRequest{Token: cl.token},
		Limit:       limit,
		Types:       "thread_v2,at_user,at_user_group,at_channel,at_everyone",
		Mode:        "priority_unreads_v1",
		WebClientFields: webclientReason("fetchActivityFeed"),
	}

	resp, err := cl.PostForm(ctx, "activity.feed", values(form, true))
	if err != nil {
		return ActivityFeedResponse{}, err
	}
	r := ActivityFeedResponse{}
	if err := cl.ParseResponse(&r, resp); err != nil {
		return ActivityFeedResponse{}, err
	}
	if err := r.validate("activity.feed"); err != nil {
		return ActivityFeedResponse{}, err
	}
	return r, nil
}

// activity.markRead API

type activityMarkReadForm struct {
	BaseRequest
	Type    string `json:"type"`
	FeedTs  string `json:"feed_ts"`
	Key     string `json:"key"`
	WebClientFields
}

func (cl *Client) ActivityMarkRead(ctx context.Context, itemType, feedTs, key string) error {
	ctx, task := trace.NewTask(ctx, "ActivityMarkRead")
	defer task.End()

	form := activityMarkReadForm{
		BaseRequest: BaseRequest{Token: cl.token},
		Type:        itemType,
		FeedTs:      feedTs,
		Key:         key,
		WebClientFields: webclientReason("mark-as-read-v2"),
	}

	resp, err := cl.PostForm(ctx, "activity.markRead", values(form, true))
	if err != nil {
		return err
	}
	r := baseResponse{}
	if err := cl.ParseResponse(&r, resp); err != nil {
		return err
	}
	if err := r.validate("activity.markRead"); err != nil {
		return err
	}
	return nil
}
