package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/korotovsky/slack-mcp-server/pkg/limiter"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/slack-go/slack"
)

// A bounded, read-only fallback recovers files when search omits their metadata.
// Unknown metadata is labelled explicitly, never reported as confirmed zero.
func (ch *ConversationsHandler) resolveSearchFiles(ctx context.Context, matches []provider.SearchMessageWithFiles, resolveMissing bool) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rl := limiter.Tier3.Limiter()
	for i := range matches {
		match := &matches[i]
		if match.Files != nil {
			match.FileMetadataStatus = "complete"
			continue
		}
		match.FileMetadataStatus = "not_requested"
		if !resolveMissing {
			continue
		}
		match.FileMetadataStatus = "unavailable"
		if !messageTimestampPattern.MatchString(match.Timestamp) || match.Channel.ID == "" {
			continue
		}
		if err := rl.Wait(ctx); err != nil {
			continue
		}
		message, err := ch.exactSearchMessage(ctx, *match)
		if err != nil {
			continue // Retain the original search hit and its unavailable status.
		}
		match.Files, match.FileMetadataStatus = message.Files, "complete"
	}
}

func (ch *ConversationsHandler) exactSearchMessage(ctx context.Context, match provider.SearchMessageWithFiles) (slack.Message, error) {
	thread := match.ThreadTimestamp
	if thread == "" {
		thread, _ = extractThreadTS(match.Permalink)
	}
	var messages []slack.Message
	if thread != "" && thread != match.Timestamp {
		var err error
		messages, _, _, err = ch.apiProvider.Slack().GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{
			ChannelID: match.Channel.ID, Timestamp: thread, Oldest: match.Timestamp, Latest: match.Timestamp, Inclusive: true, Limit: 100,
		})
		if err != nil {
			return slack.Message{}, err
		}
	} else {
		history, err := ch.apiProvider.Slack().GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
			ChannelID: match.Channel.ID, Oldest: match.Timestamp, Latest: match.Timestamp, Inclusive: true, Limit: 1,
		})
		if err != nil {
			return slack.Message{}, err
		}
		if history != nil {
			messages = history.Messages
		}
	}
	for _, message := range messages {
		if message.Timestamp == match.Timestamp {
			return message, nil
		}
	}
	return slack.Message{}, errors.New("exact search message not available")
}

func (ch *ConversationsHandler) convertSearchMessagesWithFiles(ctx context.Context, matches []provider.SearchMessageWithFiles) []Message {
	base := make([]slack.SearchMessage, len(matches))
	byMessage := make(map[string]provider.SearchMessageWithFiles, len(matches))
	for i, match := range matches {
		base[i] = match.SearchMessage
		byMessage[fmt.Sprintf("%s (#%s)|%s", match.Channel.ID, match.Channel.Name, match.Timestamp)] = match
	}
	messages := ch.convertMessagesFromSearch(ctx, base)
	for i := range messages {
		message := &messages[i]
		match := byMessage[message.Channel+"|"+message.MsgID]
		message.FileCount, message.FileMetadataStatus = len(match.Files), match.FileMetadataStatus
		if match.ThreadTimestamp != "" {
			message.ThreadTs = match.ThreadTimestamp
		}
		var ids []string
		for _, file := range match.Files {
			id := file.ID
			if file.Name != "" {
				id += " (" + file.Name + ")"
			}
			ids = append(ids, id)
		}
		message.AttachmentIDs = strings.Join(ids, ", ")
		message.HasMedia = message.HasMedia || len(match.Files) > 0
	}
	return messages
}
