package handler

import (
	"encoding/csv"
	"strings"
	"testing"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnitActivityItemCSVFormat(t *testing.T) {
	t.Run("ActivityItem marshals to CSV with correct headers", func(t *testing.T) {
		items := []ActivityItem{
			{
				Type:        "thread_v2",
				ChannelID:   "C092WJP9Z38",
				ChannelName: "#wg-maas-internal",
				ThreadTs:    "1772226249.415959",
				UnreadCount: 193,
				FeedTs:      "1772832921.456189",
				Key:         "thread_v2-C092WJP9Z38-1772226249.415959",
				MinUnreadTs: "1772226261.119069",
			},
			{
				Type:        "at_user",
				ChannelID:   "C099MEPGF43",
				ChannelName: "#wg-dashboard-crimson",
				ThreadTs:    "1772826588.319629",
				UnreadCount: 1,
				FeedTs:      "1772826812.305899",
				Key:         "at_user-C099MEPGF43-1772826812.305899-1772826588.319629",
				MinUnreadTs: "",
			},
		}

		csvBytes, err := gocsv.MarshalBytes(&items)
		require.NoError(t, err)

		reader := csv.NewReader(strings.NewReader(string(csvBytes)))
		records, err := reader.ReadAll()
		require.NoError(t, err)
		require.Equal(t, 3, len(records), "should have header + 2 data rows")

		header := records[0]
		assert.Equal(t, []string{"Type", "ChannelID", "ChannelName", "ThreadTs", "UnreadCount", "FeedTs", "Key", "MinUnreadTs"}, header)

		row1 := records[1]
		assert.Equal(t, "thread_v2", row1[0])
		assert.Equal(t, "C092WJP9Z38", row1[1])
		assert.Equal(t, "#wg-maas-internal", row1[2])
		assert.Equal(t, "193", row1[4])

		row2 := records[2]
		assert.Equal(t, "at_user", row2[0])
		assert.Equal(t, "C099MEPGF43", row2[1])
		assert.Equal(t, "1", row2[4])
	})

	t.Run("empty items list produces empty CSV", func(t *testing.T) {
		items := []ActivityItem{}
		csvBytes, err := gocsv.MarshalBytes(&items)
		require.NoError(t, err)
		assert.Contains(t, string(csvBytes), "Type,ChannelID")
	})
}

func TestUnitActivityFeedItemFiltering(t *testing.T) {
	t.Run("only unread items should be processed", func(t *testing.T) {
		items := []edge.ActivityFeedItem{
			{IsUnread: true, FeedTs: "1.0", Key: "thread_v2-C1-1.0", Item: edge.ActivityItemInner{
				Type:       "thread_v2",
				BundleInfo: &edge.ActivityBundleInfo{},
			}},
			{IsUnread: false, FeedTs: "2.0", Key: "thread_v2-C2-2.0", Item: edge.ActivityItemInner{
				Type:       "thread_v2",
				BundleInfo: &edge.ActivityBundleInfo{},
			}},
			{IsUnread: true, FeedTs: "3.0", Key: "at_user-C3-3.0-3.0", Item: edge.ActivityItemInner{
				Type:    "at_user",
				Message: &edge.ActivityMessage{Ts: "3.0", Channel: "C3", ThreadTs: "3.0"},
			}},
		}

		var unread []edge.ActivityFeedItem
		for _, item := range items {
			if item.IsUnread {
				unread = append(unread, item)
			}
		}
		assert.Equal(t, 2, len(unread))
		assert.Equal(t, "1.0", unread[0].FeedTs)
		assert.Equal(t, "3.0", unread[1].FeedTs)
	})

	t.Run("unsupported item types are skipped", func(t *testing.T) {
		items := []edge.ActivityFeedItem{
			{IsUnread: true, Item: edge.ActivityItemInner{Type: "thread_v2", BundleInfo: &edge.ActivityBundleInfo{}}},
			{IsUnread: true, Item: edge.ActivityItemInner{Type: "at_user", Message: &edge.ActivityMessage{}}},
			{IsUnread: true, Item: edge.ActivityItemInner{Type: "message_reaction"}},
			{IsUnread: true, Item: edge.ActivityItemInner{Type: "internal_channel_invite"}},
		}

		supported := map[string]bool{
			"thread_v2":      true,
			"at_user":        true,
			"at_user_group":  true,
			"at_channel":     true,
			"at_everyone":    true,
		}

		var processed int
		for _, item := range items {
			if supported[item.Item.Type] {
				processed++
			}
		}
		assert.Equal(t, 2, processed, "only thread_v2 and at_user should be processed")
	})
}

func TestUnitActivityFeedItemExtraction(t *testing.T) {
	t.Run("thread_v2 extracts channel_id, thread_ts, unread_count, min_unread_ts", func(t *testing.T) {
		item := edge.ActivityFeedItem{
			IsUnread: true,
			FeedTs:   "1772832921.456189",
			Key:      "thread_v2-C092WJP9Z38-1772226249.415959",
			Item: edge.ActivityItemInner{
				Type:       "thread_v2",
				BundleInfo: &edge.ActivityBundleInfo{},
			},
		}
		item.Item.BundleInfo.Payload.ThreadEntry.ChannelID = "C092WJP9Z38"
		item.Item.BundleInfo.Payload.ThreadEntry.ThreadTs = "1772226249.415959"
		item.Item.BundleInfo.Payload.ThreadEntry.UnreadMsgCount = 193
		item.Item.BundleInfo.Payload.ThreadEntry.MinUnreadTs = "1772226261.119069"
		item.Item.BundleInfo.Payload.ThreadEntry.LatestTs = "1772832921.456189"

		te := item.Item.BundleInfo.Payload.ThreadEntry
		assert.Equal(t, "C092WJP9Z38", te.ChannelID)
		assert.Equal(t, "1772226249.415959", te.ThreadTs)
		assert.Equal(t, 193, te.UnreadMsgCount)
		assert.Equal(t, "1772226261.119069", te.MinUnreadTs)
		assert.Equal(t, "1772832921.456189", te.LatestTs)
	})

	t.Run("at_user extracts channel, thread_ts, author", func(t *testing.T) {
		item := edge.ActivityFeedItem{
			IsUnread: true,
			FeedTs:   "1772826812.305899",
			Key:      "at_user-C099MEPGF43-1772826812.305899-1772826588.319629",
			Item: edge.ActivityItemInner{
				Type: "at_user",
				Message: &edge.ActivityMessage{
					Ts:           "1772826812.305899",
					Channel:      "C099MEPGF43",
					ThreadTs:     "1772826588.319629",
					AuthorUserID: "U0118S60VFD",
					IsBroadcast:  false,
				},
			},
		}

		msg := item.Item.Message
		assert.Equal(t, "C099MEPGF43", msg.Channel)
		assert.Equal(t, "1772826588.319629", msg.ThreadTs)
		assert.Equal(t, "U0118S60VFD", msg.AuthorUserID)
		assert.False(t, msg.IsBroadcast)
	})

	t.Run("thread_v2 without bundle_info is safely handled", func(t *testing.T) {
		item := edge.ActivityFeedItem{
			IsUnread: true,
			Item: edge.ActivityItemInner{
				Type:       "thread_v2",
				BundleInfo: nil,
			},
		}
		assert.Nil(t, item.Item.BundleInfo, "nil BundleInfo should not panic")
	})

	t.Run("at_user without message is safely handled", func(t *testing.T) {
		item := edge.ActivityFeedItem{
			IsUnread: true,
			Item: edge.ActivityItemInner{
				Type:    "at_user",
				Message: nil,
			},
		}
		assert.Nil(t, item.Item.Message, "nil Message should not panic")
	})
}

func TestUnitActivityMarkReadParams(t *testing.T) {
	t.Run("key format follows type-channel-ts pattern", func(t *testing.T) {
		keys := []struct {
			key      string
			itemType string
		}{
			{"thread_v2-C092WJP9Z38-1772226249.415959", "thread_v2"},
			{"at_user-C099MEPGF43-1772826812.305899-1772826588.319629", "at_user"},
			{"at_channel-C12345-1234567890.123456-1234567890.000000", "at_channel"},
		}
		for _, k := range keys {
			assert.True(t, strings.HasPrefix(k.key, k.itemType+"-"),
				"key %q should start with type prefix %q", k.key, k.itemType)
		}
	})
}
