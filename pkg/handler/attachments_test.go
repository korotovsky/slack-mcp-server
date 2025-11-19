package handler

import (
	"testing"

	"go.uber.org/zap"
	"slack-mcp-server/pkg/provider"
)

func TestNewAttachmentsHandler(t *testing.T) {
	logger := zap.NewNop()
	apiProvider := &provider.ApiProvider{}

	handler := NewAttachmentsHandler(apiProvider, logger)

	if handler == nil {
		t.Error("Expected handler to be created, got nil")
	}

	if handler.apiProvider != apiProvider {
		t.Error("Expected apiProvider to be set correctly")
	}

	if handler.logger != logger {
		t.Error("Expected logger to be set correctly")
	}
}

func TestAttachment_Struct(t *testing.T) {
	attachment := Attachment{
		ID:         "F1234567890",
		Name:       "test.pdf",
		Title:      "Test Document",
		MimeType:   "application/pdf",
		FileType:   "pdf",
		Size:       1024,
		URL:        "https://files.slack.com/files-pri/T1234567890-F1234567890/test.pdf",
		URLPrivate: "https://files.slack.com/files-pri/T1234567890-F1234567890/download/test.pdf",
		Permalink:  "https://example.slack.com/files/U1234567890/F1234567890/test.pdf",
		MessageID:  "1234567890.123456",
		ChannelID:  "C1234567890",
		UserID:     "U1234567890",
		UserName:   "testuser",
		Timestamp:  "2023-01-01T00:00:00Z",
		AuthToken:  "Bearer xoxc-token",
	}

	if attachment.ID != "F1234567890" {
		t.Errorf("Expected ID to be F1234567890, got %s", attachment.ID)
	}

	if attachment.MimeType != "application/pdf" {
		t.Errorf("Expected MimeType to be application/pdf, got %s", attachment.MimeType)
	}

	if attachment.Size != 1024 {
		t.Errorf("Expected Size to be 1024, got %d", attachment.Size)
	}
}
