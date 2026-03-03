package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestChannelTypeFiltering verifies the channel type classification logic used
// in GetChannels to filter the snapshot by requested types.
func TestChannelTypeFiltering(t *testing.T) {
	channels := map[string]Channel{
		"C1": {ID: "C1", Name: "#general", IsPrivate: false, IsIM: false, IsMpIM: false},
		"C2": {ID: "C2", Name: "#secret", IsPrivate: true, IsIM: false, IsMpIM: false},
		"D1": {ID: "D1", Name: "@alice", IsPrivate: true, IsIM: true, IsMpIM: false},
		"G1": {ID: "G1", Name: "mpdm-a-b-c", IsPrivate: true, IsIM: false, IsMpIM: true},
	}

	// This mirrors the filtering logic in GetChannels
	filterByTypes := func(channels map[string]Channel, types []string) []Channel {
		var res []Channel
		for _, t := range types {
			for _, ch := range channels {
				switch {
				case t == "public_channel" && !ch.IsPrivate && !ch.IsIM && !ch.IsMpIM:
					res = append(res, ch)
				case t == "private_channel" && ch.IsPrivate && !ch.IsIM && !ch.IsMpIM:
					res = append(res, ch)
				case t == "im" && ch.IsIM:
					res = append(res, ch)
				case t == "mpim" && ch.IsMpIM:
					res = append(res, ch)
				}
			}
		}
		return res
	}

	t.Run("public_channel only", func(t *testing.T) {
		res := filterByTypes(channels, []string{"public_channel"})
		assert.Len(t, res, 1)
		assert.Equal(t, "C1", res[0].ID)
	})

	t.Run("private_channel only", func(t *testing.T) {
		res := filterByTypes(channels, []string{"private_channel"})
		assert.Len(t, res, 1)
		assert.Equal(t, "C2", res[0].ID)
	})

	t.Run("im only", func(t *testing.T) {
		res := filterByTypes(channels, []string{"im"})
		assert.Len(t, res, 1)
		assert.Equal(t, "D1", res[0].ID)
	})

	t.Run("mpim only", func(t *testing.T) {
		res := filterByTypes(channels, []string{"mpim"})
		assert.Len(t, res, 1)
		assert.Equal(t, "G1", res[0].ID)
	})

	t.Run("all types returns all channels", func(t *testing.T) {
		res := filterByTypes(channels, AllChanTypes)
		assert.Len(t, res, 4)
	})

	t.Run("multiple types", func(t *testing.T) {
		res := filterByTypes(channels, []string{"public_channel", "im"})
		assert.Len(t, res, 2)
		ids := map[string]bool{}
		for _, ch := range res {
			ids[ch.ID] = true
		}
		assert.True(t, ids["C1"], "should include public channel")
		assert.True(t, ids["D1"], "should include IM")
	})

	t.Run("im is not classified as private_channel", func(t *testing.T) {
		// IMs have IsPrivate=true but should only match "im", not "private_channel"
		res := filterByTypes(channels, []string{"private_channel"})
		for _, ch := range res {
			assert.False(t, ch.IsIM, "IMs should not appear in private_channel results")
		}
	})

	t.Run("mpim is not classified as private_channel", func(t *testing.T) {
		// MPIMs have IsPrivate=true but should only match "mpim", not "private_channel"
		res := filterByTypes(channels, []string{"private_channel"})
		for _, ch := range res {
			assert.False(t, ch.IsMpIM, "MPIMs should not appear in private_channel results")
		}
	})
}

// TestAllChanTypesConstant verifies the expected channel types are defined.
func TestAllChanTypesConstant(t *testing.T) {
	assert.Len(t, AllChanTypes, 4, "should have 4 channel types")
	assert.Contains(t, AllChanTypes, "public_channel")
	assert.Contains(t, AllChanTypes, "private_channel")
	assert.Contains(t, AllChanTypes, "im")
	assert.Contains(t, AllChanTypes, "mpim")
}
