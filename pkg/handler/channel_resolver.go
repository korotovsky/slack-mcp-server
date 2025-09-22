package handler

import (
	"errors"
	"fmt"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"go.uber.org/zap"
)

// resolveChannelID resolves a channel name (starting with # or @) to its ID
// If the input is already an ID, it returns it unchanged
func resolveChannelID(channelIDOrName string, apiProvider *provider.ApiProvider, logger *zap.Logger) (string, error) {
	// If it doesn't start with # or @, assume it's already an ID
	if !strings.HasPrefix(channelIDOrName, "#") && !strings.HasPrefix(channelIDOrName, "@") {
		return channelIDOrName, nil
	}

	// Check if provider is ready for name resolution
	if ready, err := apiProvider.IsReady(); !ready {
		if errors.Is(err, provider.ErrUsersNotReady) {
			logger.Warn(
				"WARNING: Slack users sync is not ready yet, you may experience some limited functionality and see UIDs instead of resolved names as well as unable to query users by their @handles. Users sync is part of channels sync and operations on channels depend on users collection (IM, MPIM). Please wait until users are synced and try again",
				zap.Error(err),
			)
		}
		if errors.Is(err, provider.ErrChannelsNotReady) {
			logger.Warn(
				"WARNING: Slack channels sync is not ready yet, you may experience some limited functionality and be able to request conversation only by Channel ID, not by its name. Please wait until channels are synced and try again.",
				zap.Error(err),
			)
		}
		return "", fmt.Errorf("channel %q not found in empty cache", channelIDOrName)
	}

	// Resolve the name to ID using the channels cache
	channelsMaps := apiProvider.ProvideChannelsMaps()
	channelID, ok := channelsMaps.ChannelsInv[channelIDOrName]
	if !ok {
		logger.Error("Channel not found in synced cache", zap.String("channel", channelIDOrName))
		return "", fmt.Errorf("channel %q not found in synced cache. Try to remove old cache file and restart MCP Server", channelIDOrName)
	}

	return channelsMaps.Channels[channelID].ID, nil
}
