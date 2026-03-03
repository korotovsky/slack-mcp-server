package edge

import (
	"errors"
	"testing"

	"github.com/rusq/slack"
	"github.com/stretchr/testify/assert"
)

// TestPartialResultCollection verifies the result collection pattern used in
// GetConversationsContext: individual goroutine failures should not discard
// channels from goroutines that succeeded. Only if all sources fail should an
// error be returned.
func TestPartialResultCollection(t *testing.T) {
	// collectResults mirrors the production result-collection loop in
	// GetConversationsContext. It's extracted here so we can test the logic
	// without needing real Slack API calls.
	type result struct {
		Channels []slack.Channel
		Err      error
	}

	collectResults := func(results []result) ([]slack.Channel, error) {
		var channels []slack.Channel
		seen := make(map[string]struct{})
		var lastErr error

		for _, r := range results {
			if r.Err != nil {
				lastErr = r.Err
				continue
			}
			for _, c := range r.Channels {
				if _, ok := seen[c.ID]; !ok {
					seen[c.ID] = struct{}{}
					channels = append(channels, c)
				}
			}
		}

		if len(channels) == 0 && lastErr != nil {
			return nil, lastErr
		}
		return channels, nil
	}

	ch := func(id string) slack.Channel {
		return slack.Channel{GroupConversation: slack.GroupConversation{
			Conversation: slack.Conversation{ID: id},
		}}
	}

	t.Run("all sources succeed", func(t *testing.T) {
		results := []result{
			{Channels: []slack.Channel{ch("C1"), ch("C2")}},
			{Channels: []slack.Channel{ch("C3")}},
			{Channels: []slack.Channel{ch("C4")}},
		}
		channels, err := collectResults(results)
		assert.NoError(t, err)
		assert.Len(t, channels, 4)
	})

	t.Run("one source fails, others succeed", func(t *testing.T) {
		results := []result{
			{Channels: []slack.Channel{ch("C1"), ch("C2")}},
			{Err: errors.New("IMList failed")},
			{Channels: []slack.Channel{ch("C3")}},
		}
		channels, err := collectResults(results)
		assert.NoError(t, err, "should not propagate error when some sources succeeded")
		assert.Len(t, channels, 3, "should keep channels from successful sources")
	})

	t.Run("two sources fail, one succeeds", func(t *testing.T) {
		results := []result{
			{Err: errors.New("ClientUserBoot failed")},
			{Err: errors.New("IMList failed")},
			{Channels: []slack.Channel{ch("C1")}},
		}
		channels, err := collectResults(results)
		assert.NoError(t, err, "should not propagate error when at least one source succeeded")
		assert.Len(t, channels, 1)
	})

	t.Run("all sources fail", func(t *testing.T) {
		results := []result{
			{Err: errors.New("ClientUserBoot failed")},
			{Err: errors.New("IMList failed")},
			{Err: errors.New("SearchChannels failed")},
		}
		channels, err := collectResults(results)
		assert.Error(t, err, "should propagate error when all sources failed")
		assert.Nil(t, channels)
	})

	t.Run("deduplicates channels across sources", func(t *testing.T) {
		results := []result{
			{Channels: []slack.Channel{ch("C1"), ch("C2")}},
			{Channels: []slack.Channel{ch("C2"), ch("C3")}}, // C2 is duplicate
			{Channels: []slack.Channel{ch("C1")}},           // C1 is duplicate
		}
		channels, err := collectResults(results)
		assert.NoError(t, err)
		assert.Len(t, channels, 3, "duplicates should be removed")
	})

	t.Run("no results and no errors returns empty", func(t *testing.T) {
		results := []result{
			{Channels: []slack.Channel{}},
			{Channels: []slack.Channel{}},
		}
		channels, err := collectResults(results)
		assert.NoError(t, err)
		assert.Empty(t, channels)
	})
}
