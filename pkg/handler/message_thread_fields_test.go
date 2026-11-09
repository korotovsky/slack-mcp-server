package handler

import (
	"testing"

	"github.com/gocarina/gocsv"
	"github.com/stretchr/testify/require"
)

func TestUnitMessageThreadFields(t *testing.T) {
	rows := []Message{{Text: "parent", ReplyCount: 4, LatestReply: "1234567890.123456"}, {Text: "standalone"}}
	data, err := gocsv.MarshalBytes(&rows)
	require.NoError(t, err)
	require.Contains(t, string(data), "ReplyCount")
	require.Contains(t, string(data), "LatestReply")
	require.Contains(t, string(data), "1234567890.123456")
}
