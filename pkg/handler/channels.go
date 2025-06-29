package handler

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
)

type Channel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Topic       string `json:"topic"`
	Purpose     string `json:"purpose"`
	MemberCount int    `json:"memberCount"`
	Cursor      string `json:"cursor"`
}

type ChannelsHandler struct {
	apiProvider *provider.ApiProvider
	validTypes  map[string]bool
}

func NewChannelsHandler(apiProvider *provider.ApiProvider) *ChannelsHandler {
	validTypes := make(map[string]bool, len(provider.AllChanTypes))
	for _, v := range provider.AllChanTypes {
		validTypes[v] = true
	}

	return &ChannelsHandler{
		apiProvider: apiProvider,
		validTypes:  validTypes,
	}
}

func (ch *ChannelsHandler) ChannelsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sortType := request.GetString("sort", "popularity")
	types := request.GetString("channel_types", provider.PubChanType)

	// MCP Inspector v0.14.0 has issues with Slice type
	// introspection, so some type simplification makes sense here
	channelTypes := []string{}
	for _, t := range strings.Split(types, ",") {
		t = strings.TrimSpace(t)
		if ch.validTypes[t] {
			channelTypes = append(channelTypes, t)
		}
	}

	cursor := request.GetString("cursor", "")
	limit := request.GetInt("limit", 0)
	if limit == 0 && cursor == "" {
		limit = 100
	}
	if limit == 0 {
		limit = 100
	}
	if limit >= 1000 {
		return nil, fmt.Errorf("limit must be less than 1000, got %d", limit)
	}

	var (
		nextcur     string
		channelList []Channel
	)

	channels := filterChannelsByTypes(ch.apiProvider.ProvideChannelsMaps().Channels, channelTypes)

	var chans []provider.Channel

	chans, nextcur = paginateChannels(
		channels,
		cursor,
		limit,
	)

	for _, channel := range chans {
		channelList = append(channelList, Channel{
			ID:          channel.ID,
			Name:        channel.Name,
			Topic:       channel.Topic,
			Purpose:     channel.Purpose,
			MemberCount: channel.MemberCount,
		})
	}

	switch sortType {
	case "popularity":
		sort.Slice(channelList, func(i, j int) bool {
			return channelList[i].MemberCount > channelList[j].MemberCount
		})
	default:
		// pass
	}

	if len(channelList) > 0 && nextcur != "" {
		channelList[len(channelList)-1].Cursor = nextcur
	}

	csvBytes, err := gocsv.MarshalBytes(&channelList)
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(csvBytes)), nil
}

func filterChannelsByTypes(channels map[string]provider.Channel, types []string) []provider.Channel {
	var result []provider.Channel
	typeSet := make(map[string]bool)

	for _, t := range types {
		typeSet[t] = true
	}

	for _, ch := range channels {
		if typeSet["public_channel"] && !ch.IsPrivate && !ch.IsIM && !ch.IsMpIM {
			result = append(result, ch)
		}
		if typeSet["private_channel"] && ch.IsPrivate && !ch.IsIM && !ch.IsMpIM {
			result = append(result, ch)
		}
		if typeSet["im"] && ch.IsIM {
			result = append(result, ch)
		}
		if typeSet["mpim"] && ch.IsMpIM {
			result = append(result, ch)
		}
	}
	return result
}

func paginateChannels(channels []provider.Channel, cursor string, limit int) ([]provider.Channel, string) {
	sort.Slice(channels, func(i, j int) bool {
		return channels[i].ID < channels[j].ID
	})

	startIndex := 0
	if cursor != "" {
		if decoded, err := base64.StdEncoding.DecodeString(cursor); err == nil {
			lastID := string(decoded)
			for i, ch := range channels {
				if ch.ID > lastID {
					startIndex = i
					break
				}
			}
		}
	}

	endIndex := startIndex + limit
	if endIndex > len(channels) {
		endIndex = len(channels)
	}

	paged := channels[startIndex:endIndex]

	var nextCursor string
	if endIndex < len(channels) {
		nextCursor = base64.StdEncoding.EncodeToString([]byte(channels[endIndex-1].ID))
	}

	return paged, nextCursor
}
