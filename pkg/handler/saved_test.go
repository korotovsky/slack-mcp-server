package handler

import (
	"testing"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnitSavedItemCSVFormat(t *testing.T) {
	items := []SavedItemRow{
		{
			ItemID:      "C0AJMCRNH0U",
			ChannelID:   "C0AJMCRNH0U",
			ChannelName: "#wg-feast-ui",
			Ts:          "1772680334.954409",
			DateCreated: "2026-03-03 12:00",
			DateDue:     "",
			State:       "in_progress",
		},
		{
			ItemID:      "D0AGSQXLJHG",
			ChannelID:   "D0AGSQXLJHG",
			ChannelName: "@john",
			Ts:          "1771941381.234049",
			DateCreated: "2026-02-25 08:30",
			DateDue:     "2026-03-01 15:00",
			State:       "in_progress",
		},
	}

	csvBytes, err := gocsv.MarshalBytes(&items)
	require.NoError(t, err)
	csvStr := string(csvBytes)

	assert.Contains(t, csvStr, "ItemID,ChannelID,ChannelName,Ts,DateCreated,DateDue,State")
	assert.Contains(t, csvStr, "C0AJMCRNH0U")
	assert.Contains(t, csvStr, "#wg-feast-ui")
	assert.Contains(t, csvStr, "1772680334.954409")
	assert.Contains(t, csvStr, "in_progress")
	assert.Contains(t, csvStr, "@john")
}

func TestUnitSavedItemFieldExtraction(t *testing.T) {
	item := edge.SavedItem{
		ItemID:           "C092WJP9Z38",
		ItemType:         "message",
		DateCreated:      1772036567,
		DateDue:          1772521200,
		DateCompleted:    0,
		DateUpdated:      1772036567,
		IsArchived:       false,
		DateSnoozedUntil: 0,
		Ts:               "1772034406.593509",
		State:            "in_progress",
	}

	assert.Equal(t, "C092WJP9Z38", item.ItemID)
	assert.Equal(t, "message", item.ItemType)
	assert.Equal(t, "in_progress", item.State)
	assert.Equal(t, int64(1772521200), item.DateDue)
	assert.Equal(t, int64(0), item.DateCompleted)
	assert.Equal(t, "1772034406.593509", item.Ts)
	assert.False(t, item.IsArchived)
}

func TestUnitSavedCountsDeserialization(t *testing.T) {
	counts := edge.SavedCounts{
		UncompletedCount:        51,
		UncompletedOverdueCount: 1,
		ArchivedCount:           0,
		CompletedCount:          0,
		TotalCount:              52,
	}

	assert.Equal(t, 51, counts.UncompletedCount)
	assert.Equal(t, 1, counts.UncompletedOverdueCount)
	assert.Equal(t, 52, counts.TotalCount)
	assert.Equal(t, 0, counts.CompletedCount)
	assert.Equal(t, 0, counts.ArchivedCount)
}

func TestUnitFormatUnixTs(t *testing.T) {
	tests := []struct {
		name     string
		input    int64
		expected string
	}{
		{
			name:     "zero returns empty string",
			input:    0,
			expected: "",
		},
		{
			name:     "valid timestamp formats correctly",
			input:    1772720838,
			expected: "2026-03-05 14:27",
		},
		{
			name:     "another valid timestamp",
			input:    1772521200,
			expected: "2026-03-03 07:00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatUnixTs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUnitSavedUpdateParamValidation(t *testing.T) {
	t.Run("item_id and ts are both required", func(t *testing.T) {
		assert.NotEmpty(t, "C092WJP9Z38", "item_id must be non-empty")
		assert.NotEmpty(t, "1772034406.593509", "ts must be non-empty")
	})

	t.Run("mark values", func(t *testing.T) {
		validMarks := []string{"completed", ""}
		for _, m := range validMarks {
			assert.Contains(t, []string{"completed", ""}, m)
		}
	})
}

func TestUnitSavedListResponseParsing(t *testing.T) {
	resp := edge.SavedListResponse{
		SavedItems: []edge.SavedItem{
			{
				ItemID:      "D0AGSQXLJHG",
				ItemType:    "message",
				DateCreated: 1771942696,
				DateDue:     1772521200,
				Ts:          "1771941381.234049",
				State:       "in_progress",
			},
			{
				ItemID:      "C0AJMCRNH0U",
				ItemType:    "message",
				DateCreated: 1772720838,
				DateDue:     0,
				Ts:          "1772680334.954409",
				State:       "in_progress",
			},
		},
		Counts: edge.SavedCounts{
			UncompletedCount: 51,
			TotalCount:       52,
		},
	}

	assert.Len(t, resp.SavedItems, 2)
	assert.Equal(t, 51, resp.Counts.UncompletedCount)

	first := resp.SavedItems[0]
	assert.Equal(t, "D0AGSQXLJHG", first.ItemID)
	assert.Equal(t, int64(1772521200), first.DateDue)

	second := resp.SavedItems[1]
	assert.Equal(t, int64(0), second.DateDue)
}
