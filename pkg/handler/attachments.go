package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gocarina/gocsv"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
	"slack-mcp-server/pkg/provider"
	"slack-mcp-server/pkg/server/auth"
)

type Attachment struct {
	ID         string `json:"id" csv:"id"`
	Name       string `json:"name" csv:"name"`
	Title      string `json:"title" csv:"title"`
	MimeType   string `json:"mimeType" csv:"mimeType"`
	FileType   string `json:"fileType" csv:"fileType"`
	Size       int    `json:"size" csv:"size"`
	URL        string `json:"url" csv:"url"`
	URLPrivate string `json:"urlPrivate" csv:"urlPrivate"`
	Permalink  string `json:"permalink" csv:"permalink"`
	MessageID  string `json:"messageID" csv:"messageID"`
	ChannelID  string `json:"channelID" csv:"channelID"`
	UserID     string `json:"userID" csv:"userID"`
	UserName   string `json:"userName" csv:"userName"`
	Timestamp  string `json:"timestamp" csv:"timestamp"`
	AuthToken  string `json:"authToken" csv:"authToken"`
}

type AttachmentsHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
}

func NewAttachmentsHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *AttachmentsHandler {
	return &AttachmentsHandler{
		apiProvider: apiProvider,
		logger:      logger,
	}
}

// MessagesWithAttachmentsHandler searches for messages that contain attachments
func (ah *AttachmentsHandler) MessagesWithAttachmentsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ah.logger.Debug("MessagesWithAttachmentsHandler called", zap.Any("params", request.Params))

	if authenticated, err := auth.IsAuthenticated(ctx, ah.apiProvider.ServerTransport(), ah.logger); !authenticated {
		ah.logger.Error("Authentication failed", zap.Error(err))
		return nil, err
	}

	channelID := request.GetString("channel_id", "")
	if channelID == "" {
		return nil, errors.New("channel_id is required")
	}

	limit := request.GetInt("limit", 100)
	cursor := request.GetString("cursor", "")

	// Resolve channel name to ID if needed
	if strings.HasPrefix(channelID, "#") || strings.HasPrefix(channelID, "@") {
		channelsMaps := ah.apiProvider.ProvideChannelsMaps()
		chn, ok := channelsMaps.ChannelsInv[channelID]
		if !ok {
			return nil, fmt.Errorf("channel %q not found", channelID)
		}
		channelID = channelsMaps.Channels[chn].ID
	}

	// Get conversation history and filter for messages with files
	historyParams := slack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Limit:     limit,
		Cursor:    cursor,
		Inclusive: false,
	}

	history, err := ah.apiProvider.Slack().GetConversationHistoryContext(ctx, &historyParams)
	if err != nil {
		ah.logger.Error("Failed to get conversation history", zap.Error(err))
		return nil, err
	}

	var attachments []Attachment
	usersMap := ah.apiProvider.ProvideUsersMap()

	for _, msg := range history.Messages {
		// Only process messages that have files
		if len(msg.Files) == 0 {
			continue
		}

		for _, file := range msg.Files {
			userName := file.User
			if user, ok := usersMap.Users[file.User]; ok {
				userName = user.Name
			}

			attachment := Attachment{
				ID:         file.ID,
				Name:       file.Name,
				Title:      file.Title,
				MimeType:   file.Mimetype,
				FileType:   file.Filetype,
				Size:       file.Size,
				URL:        file.URLDownload,
				URLPrivate: file.URLPrivateDownload,
				Permalink:  file.Permalink,
				MessageID:  msg.Timestamp,
				ChannelID:  channelID,
				UserID:     file.User,
				UserName:   userName,
				Timestamp:  msg.Timestamp,
				AuthToken:  "Bearer " + ah.getAuthToken(),
			}
			attachments = append(attachments, attachment)
		}
	}

	csvBytes, err := gocsv.MarshalBytes(&attachments)
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(csvBytes)), nil
}

// GetAttachmentDetailsHandler gets detailed information about a specific attachment
func (ah *AttachmentsHandler) GetAttachmentDetailsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ah.logger.Debug("GetAttachmentDetailsHandler called", zap.Any("params", request.Params))

	if authenticated, err := auth.IsAuthenticated(ctx, ah.apiProvider.ServerTransport(), ah.logger); !authenticated {
		ah.logger.Error("Authentication failed", zap.Error(err))
		return nil, err
	}

	fileID := request.GetString("file_id", "")
	if fileID == "" {
		return nil, errors.New("file_id is required")
	}

	// Get file info using Slack API
	file, _, _, err := ah.apiProvider.Slack().GetFileInfoContext(ctx, fileID, 1, 1)
	if err != nil {
		ah.logger.Error("Failed to get file info", zap.String("file_id", fileID), zap.Error(err))
		return nil, err
	}

	usersMap := ah.apiProvider.ProvideUsersMap()
	return ah.buildAttachmentResponse(file, usersMap)
}

func (ah *AttachmentsHandler) buildAttachmentResponse(file *slack.File, usersMap *provider.UsersCache) (*mcp.CallToolResult, error) {

	userName := file.User
	if user, ok := usersMap.Users[file.User]; ok {
		userName = user.Name
	}

	attachment := Attachment{
		ID:         file.ID,
		Name:       file.Name,
		Title:      file.Title,
		MimeType:   file.Mimetype,
		FileType:   file.Filetype,
		Size:       file.Size,
		URL:        file.URLDownload,
		URLPrivate: file.URLPrivateDownload,
		Permalink:  file.Permalink,
		MessageID:  "", // Not available in file info
		ChannelID:  "", // Not available in file info
		UserID:     file.User,
		UserName:   userName,
		Timestamp:  file.Timestamp.String(),
		AuthToken:  "Bearer " + ah.getAuthToken(),
	}

	jsonBytes, err := json.MarshalIndent(attachment, "", "  ")
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(jsonBytes)), nil
}

// getAuthToken returns the appropriate auth token for accessing attachments
func (ah *AttachmentsHandler) getAuthToken() string {
	// Try to get the token from the API provider's auth
	if client := ah.apiProvider.Slack(); client != nil {
		if mcpClient, ok := client.(*provider.MCPSlackClient); ok {
			if authResp := mcpClient.AuthResponse(); authResp != nil {
				// For file access, we need the actual token
				// This is a simplified approach - in production you'd want to handle this more securely
				return "xoxc-token-required-for-file-access"
			}
		}
	}
	return "auth-token-required"
}
