package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// maxUploadFileSizeBytes caps a single upload. Slack itself allows 1GB, but an
// agent-driven tool has no business shipping arbitrarily large payloads.
const maxUploadFileSizeBytes = 100 * 1024 * 1024 // 100MB

type fileUploadParams struct {
	channel        string
	threadTs       string
	filename       string
	title          string
	initialComment string
	altTxt         string
	snippetType    string

	// Exactly one of the two is populated: data holds inline content, file is
	// an already-open descriptor for an on-disk upload. The caller owns file
	// and must close it.
	data     []byte
	file     *os.File
	filePath string
	size     int64
}

// openUploadFile opens an already-validated path and re-checks it through the
// descriptor. Validating a path and then reopening it by name is a TOCTOU hole:
// the name can point at something else by the time we read it. Everything after
// this call works off the descriptor, which cannot be swapped.
func openUploadFile(resolved string) (*os.File, int64, error) {
	f, err := os.OpenFile(resolved, os.O_RDONLY|openUploadExtraFlags, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot open %q: %w", resolved, err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("cannot stat %q: %w", resolved, err)
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, 0, fmt.Errorf("%q is not a regular file", resolved)
	}
	if info.Size() > maxUploadFileSizeBytes {
		f.Close()
		return nil, 0, fmt.Errorf("file size %d bytes exceeds maximum allowed size of %d bytes", info.Size(), maxUploadFileSizeBytes)
	}
	if info.Size() == 0 {
		f.Close()
		return nil, 0, fmt.Errorf("file %q is empty, Slack rejects zero-length uploads", resolved)
	}

	return f, info.Size(), nil
}

// parseUploadPathsEnv splits SLACK_MCP_FILE_UPLOAD_PATHS into directory roots.
func parseUploadPathsEnv(raw string) []string {
	var roots []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			roots = append(roots, item)
		}
	}
	return roots
}

// resolveUploadFilePath validates that path points at a regular file located
// inside one of allowedRoots. Symlinks are resolved first, so a link sitting in
// an allowed directory cannot smuggle a file out of an unallowed one.
func resolveUploadFilePath(path string, allowedRoots []string) (string, error) {
	if len(allowedRoots) == 0 {
		return "", errors.New(
			"uploading by file_path is disabled: set SLACK_MCP_FILE_UPLOAD_PATHS to a comma separated list " +
				"of directories the server may read, e.g. 'SLACK_MCP_FILE_UPLOAD_PATHS=/home/user/Pictures,/tmp'",
		)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("file_path must be an absolute path, got %q", path)
	}

	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("cannot access file_path %q: %w", path, err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("cannot stat file_path %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("file_path %q is not a regular file", path)
	}

	for _, root := range allowedRoots {
		if !filepath.IsAbs(root) {
			continue
		}
		// Roots may themselves be symlinks; ignore ones that cannot be resolved.
		resolvedRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(resolvedRoot, resolved)
		if err != nil {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return resolved, nil
		}
	}

	return "", fmt.Errorf(
		"file_path %q is not allowed, it resolves outside SLACK_MCP_FILE_UPLOAD_PATHS", path)
}

func (ch *ConversationsHandler) parseParamsToolFileUpload(ctx context.Context, request mcp.CallToolRequest) (*fileUploadParams, error) {
	toolConfig := os.Getenv("SLACK_MCP_FILE_UPLOAD_TOOL")
	enabledTools := os.Getenv("SLACK_MCP_ENABLED_TOOLS")

	if toolConfig == "" {
		if !strings.Contains(enabledTools, "file_upload") {
			ch.logger.Error("File-upload tool disabled by default")
			return nil, errors.New(
				"by default, the file_upload tool is disabled to guard Slack workspaces against accidental uploads. " +
					"To enable it, set the SLACK_MCP_FILE_UPLOAD_TOOL environment variable to true, 1, or a comma separated list of channels " +
					"to limit where the MCP can upload files, e.g. 'SLACK_MCP_FILE_UPLOAD_TOOL=C1234567890,D0987654321', " +
					"'SLACK_MCP_FILE_UPLOAD_TOOL=!C1234567890' to enable all except one or 'SLACK_MCP_FILE_UPLOAD_TOOL=true' for all channels and DMs",
			)
		}
		toolConfig = "true"
	}

	channel := request.GetString("channel_id", "")
	if channel == "" {
		ch.logger.Error("channel_id missing in file-upload params")
		return nil, errors.New("channel_id must be a string")
	}
	channel, err := ch.resolveChannelID(ctx, channel)
	if err != nil {
		ch.logger.Error("Channel not found", zap.String("channel", channel), zap.Error(err))
		return nil, err
	}
	if !isChannelAllowedForConfig(channel, toolConfig) {
		ch.logger.Warn("File-upload tool not allowed for channel", zap.String("channel", channel), zap.String("policy", toolConfig))
		return nil, fmt.Errorf("file_upload tool is not allowed for channel %q, applied policy: %s", channel, toolConfig)
	}

	threadTs := request.GetString("thread_ts", "")
	if threadTs != "" && !strings.Contains(threadTs, ".") {
		ch.logger.Error("Invalid thread_ts format", zap.String("thread_ts", threadTs))
		return nil, errors.New("thread_ts must be a valid timestamp in format 1234567890.123456")
	}

	filePath := strings.TrimSpace(request.GetString("file_path", ""))
	content := request.GetString("content", "")
	contentB64 := strings.TrimSpace(request.GetString("content_base64", ""))

	sources := 0
	for _, present := range []bool{filePath != "", content != "", contentB64 != ""} {
		if present {
			sources++
		}
	}
	if sources != 1 {
		return nil, errors.New("exactly one of file_path, content or content_base64 must be provided")
	}

	params := &fileUploadParams{
		channel:        channel,
		threadTs:       threadTs,
		title:          request.GetString("title", ""),
		initialComment: request.GetString("initial_comment", ""),
		altTxt:         request.GetString("alt_txt", ""),
		snippetType:    request.GetString("snippet_type", ""),
	}

	// Slack shows whatever we send as the file name, so never let a path leak into it.
	filename := strings.TrimSpace(request.GetString("filename", ""))
	if filename != "" {
		filename = filepath.Base(filepath.Clean(filename))
	}

	switch {
	case filePath != "":
		resolved, err := resolveUploadFilePath(filePath, parseUploadPathsEnv(os.Getenv("SLACK_MCP_FILE_UPLOAD_PATHS")))
		if err != nil {
			ch.logger.Warn("Rejected file_path", zap.String("file_path", filePath), zap.Error(err))
			return nil, err
		}
		f, size, err := openUploadFile(resolved)
		if err != nil {
			ch.logger.Warn("Rejected file_path on open", zap.String("file_path", filePath), zap.Error(err))
			return nil, err
		}
		if filename == "" {
			filename = filepath.Base(resolved)
		}
		params.file = f
		params.filePath = resolved
		params.size = size

	case contentB64 != "":
		if filename == "" {
			return nil, errors.New("filename is required when uploading content_base64")
		}
		if len(contentB64) > maxUploadFileSizeBytes {
			return nil, fmt.Errorf("content_base64 length %d exceeds maximum allowed size of %d bytes", len(contentB64), maxUploadFileSizeBytes)
		}
		data, err := base64.StdEncoding.DecodeString(contentB64)
		if err != nil {
			return nil, fmt.Errorf("content_base64 is not valid base64: %w", err)
		}
		params.data = data
		params.size = int64(len(data))

	default:
		if filename == "" {
			return nil, errors.New("filename is required when uploading content")
		}
		if len(content) > maxUploadFileSizeBytes {
			return nil, fmt.Errorf("content length %d exceeds maximum allowed size of %d bytes", len(content), maxUploadFileSizeBytes)
		}
		params.data = []byte(content)
		params.size = int64(len(content))
	}

	params.filename = filename
	if params.title == "" {
		params.title = params.filename
	}

	return params, nil
}

// FileUploadHandler uploads a file to Slack and shares it into a channel or thread.
func (ch *ConversationsHandler) FileUploadHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Deliberately not logging request.Params: for this tool they carry the
	// file payload itself (content / content_base64).
	ch.logger.Debug("FileUploadHandler called")

	if ready, err := ch.apiProvider.IsReady(); !ready {
		ch.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	params, err := ch.parseParamsToolFileUpload(ctx, request)
	if err != nil {
		ch.logger.Error("Failed to parse file_upload params", zap.Error(err))
		return nil, err
	}

	uploadParams := slack.UploadFileParameters{
		Filename:        params.filename,
		FileSize:        int(params.size),
		Title:           params.title,
		InitialComment:  params.initialComment,
		Channel:         params.channel,
		ThreadTimestamp: params.threadTs,
		AltTxt:          params.altTxt,
		SnippetType:     params.snippetType,
	}

	if params.file != nil {
		// Descriptor was opened and vetted during validation, so nothing can be
		// swapped underneath us. Streaming it keeps the file out of memory.
		defer params.file.Close()
		uploadParams.Reader = params.file
	} else {
		uploadParams.Reader = bytes.NewReader(params.data)
	}

	ch.logger.Debug("Uploading file to Slack",
		zap.String("channel", params.channel),
		zap.String("thread_ts", params.threadTs),
		zap.String("filename", params.filename),
		zap.Int("size", uploadParams.FileSize),
	)

	summary, err := ch.apiProvider.Slack().UploadFileContext(ctx, uploadParams)
	if err != nil {
		ch.logger.Error("Slack UploadFileContext failed", zap.Error(err))
		return nil, err
	}

	ch.logger.Debug("File uploaded", zap.String("file_id", summary.ID))

	return mcp.NewToolResultText(fmt.Sprintf(
		`{"file_id":"%s","filename":"%s","title":"%s","size":%d,"channel":"%s","thread_ts":"%s"}`,
		escapeJSON(summary.ID),
		escapeJSON(params.filename),
		escapeJSON(summary.Title),
		uploadParams.FileSize,
		escapeJSON(params.channel),
		escapeJSON(params.threadTs),
	)), nil
}
