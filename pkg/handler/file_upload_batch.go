package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
)

const maxFilesPerUpload = 10

type uploadFilePlan struct {
	Filename    string `json:"filename"`
	Title       string `json:"title"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	AltText     string `json:"alt_txt,omitempty"`
	SnippetType string `json:"snippet_type,omitempty"`
}

type uploadPlan struct {
	Channel        string           `json:"channel"`
	ChannelName    string           `json:"channel_name,omitempty"`
	ThreadTS       string           `json:"thread_ts,omitempty"`
	InitialComment string           `json:"initial_comment"`
	Files          []uploadFilePlan `json:"files"`
	PlanHash       string           `json:"plan_hash"`
}

type uploadResult struct {
	uploadPlan
	Status     string   `json:"status"`
	Verified   bool     `json:"verified"`
	MessageTS  string   `json:"message_ts,omitempty"`
	FileIDs    []string `json:"file_ids,omitempty"`
	Message    string   `json:"message,omitempty"`
	DoNotRetry bool     `json:"do_not_retry"`
}

func uploadToolResult(result uploadResult, isError bool) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if isError {
		return mcp.NewToolResultError(string(data)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

var uploadSourceKeys = []string{"file_path", "content", "content_base64", "filename", "title", "alt_txt", "snippet_type"}

func (ch *ConversationsHandler) parseUploadBatch(ctx context.Context, request mcp.CallToolRequest) ([]*fileUploadParams, error) {
	comment := request.GetString("initial_comment", "")
	if strings.TrimSpace(comment) == "" {
		return nil, errors.New("initial_comment is required: files must accompany a message")
	}
	if ts := request.GetString("thread_ts", ""); ts != "" && !messageTimestampPattern.MatchString(ts) {
		return nil, errors.New("thread_ts must be a timestamp in format 1234567890.123456")
	}
	args := request.GetArguments()
	var inputs []map[string]any
	if raw, present := args["files"]; present {
		for _, key := range uploadSourceKeys {
			if _, exists := args[key]; exists {
				return nil, fmt.Errorf("files cannot be combined with top-level %s", key)
			}
		}
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, errors.New("files must be an array of file objects")
		}
		if err := json.Unmarshal(data, &inputs); err != nil || len(inputs) == 0 {
			return nil, errors.New("files must be a non-empty array of file objects")
		}
	} else {
		inputs = []map[string]any{args}
	}
	if len(inputs) > maxFilesPerUpload {
		return nil, fmt.Errorf("at most %d files can be shared in one message", maxFilesPerUpload)
	}
	var batch []*fileUploadParams
	for _, input := range inputs {
		item := mcp.CallToolRequest{}
		item.Params.Name = "file_upload"
		itemArgs := map[string]any{"channel_id": args["channel_id"], "initial_comment": comment}
		if ts, present := args["thread_ts"]; present {
			itemArgs["thread_ts"] = ts
		}
		for _, key := range uploadSourceKeys {
			if value, present := input[key]; present {
				if _, ok := value.(string); !ok {
					closeUploadBatch(batch)
					return nil, fmt.Errorf("%s must be a string", key)
				}
				itemArgs[key] = value
			}
		}
		item.Params.Arguments = itemArgs
		params, err := ch.parseParamsToolFileUpload(ctx, item)
		if err != nil {
			closeUploadBatch(batch)
			return nil, err
		}
		batch = append(batch, params)
	}
	return batch, nil
}

func closeUploadBatch(batch []*fileUploadParams) {
	for _, file := range batch {
		if file.file != nil {
			_ = file.file.Close()
		}
	}
}

// Snapshot disk files for the send phase so an edit after hashing cannot change
// the bytes that the confirmed plan will upload. Staging is disk-backed, private,
// and removed when the call finishes. A preview creates no staging files.
func prepareUploadFile(ctx context.Context, params *fileUploadParams, dryRun bool) (uploadFilePlan, io.Reader, func(), error) {
	plan := uploadFilePlan{Filename: params.filename, Title: params.title, Size: params.size, AltText: params.altTxt, SnippetType: params.snippetType}
	var reader io.Reader = bytes.NewReader(params.data)
	if params.file != nil {
		reader = params.file
	}
	hash := sha256.New()
	var staging *os.File
	cleanup := func() {}
	var writer io.Writer = hash
	if !dryRun && params.file != nil {
		dir := os.Getenv("SLACK_MCP_FILE_UPLOAD_STAGING_DIR")
		if dir == "" {
			cache, err := os.UserCacheDir()
			if err != nil {
				return plan, nil, cleanup, err
			}
			dir = filepath.Join(cache, "slack-mcp-server", "upload-staging")
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return plan, nil, cleanup, err
		}
		var err error
		staging, err = os.CreateTemp(dir, "upload-*")
		if err != nil {
			return plan, nil, cleanup, err
		}
		cleanup = func() { _ = staging.Close(); _ = os.Remove(staging.Name()) }
		writer = io.MultiWriter(staging, hash)
	}
	limit, err := UploadMaxBytes()
	if err != nil {
		cleanup()
		return plan, nil, func() {}, err
	}
	n, err := io.Copy(writer, &uploadContextReader{ctx: ctx, reader: io.LimitReader(reader, limit+1)})
	if err == nil && (n != params.size || n == 0 || n > limit) {
		err = fmt.Errorf("upload source size changed: expected %d bytes, read %d", params.size, n)
	}
	if err != nil {
		cleanup()
		return plan, nil, func() {}, err
	}
	plan.SHA256 = fmt.Sprintf("%x", hash.Sum(nil))
	if staging != nil {
		if _, err := staging.Seek(0, io.SeekStart); err != nil {
			cleanup()
			return plan, nil, func() {}, err
		}
		return plan, staging, cleanup, nil
	}
	return plan, bytes.NewReader(params.data), cleanup, nil
}

type uploadContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *uploadContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func safeUploadError(err error) string {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err.Error() // Do not return a signed upload URL.
	}
	return err.Error()
}

// FileUploadHandler previews by default. A send is one completion call for the
// whole batch and is never automatically retried, even if its outcome is unclear.
func (ch *ConversationsHandler) FileUploadHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if ready, err := ch.apiProvider.IsReady(); !ready {
		return nil, err
	}
	if value, present := request.GetArguments()["dry_run"]; present {
		if _, ok := value.(bool); !ok {
			return nil, errors.New("dry_run must be a boolean")
		}
	}
	dryRun := request.GetBool("dry_run", true)
	batch, err := ch.parseUploadBatch(ctx, request)
	if err != nil {
		return nil, err
	}
	defer closeUploadBatch(batch)
	plan := uploadPlan{Channel: batch[0].channel, ThreadTS: batch[0].threadTs, InitialComment: batch[0].initialComment}
	if cache := ch.apiProvider.ProvideChannelsMaps(); cache != nil {
		plan.ChannelName = cache.Channels[plan.Channel].Name
	}
	var readers []io.Reader
	for _, file := range batch {
		item, reader, cleanup, err := prepareUploadFile(ctx, file, dryRun)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		plan.Files = append(plan.Files, item)
		readers = append(readers, reader)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	plan.PlanHash = fmt.Sprintf("%x", sha256.Sum256(encoded))
	result := uploadResult{uploadPlan: plan, Status: "preview"}
	if dryRun {
		return uploadToolResult(result, false)
	}
	if request.GetString("expected_plan_hash", "") != plan.PlanHash {
		return nil, errors.New("expected_plan_hash must match a fresh preview; destination, caption, metadata or file bytes changed")
	}
	api := ch.apiProvider.Slack()
	var summaries []slack.FileSummary
	for i, file := range batch {
		upload, err := api.GetUploadURLExternalContext(ctx, slack.GetUploadURLExternalParameters{FileName: file.filename, FileSize: int(file.size), AltTxt: file.altTxt, SnippetType: file.snippetType})
		if err != nil {
			result.Status, result.Message = "upload_failed", safeUploadError(err)
			return uploadToolResult(result, true)
		}
		if upload == nil || upload.FileID == "" || upload.UploadURL == "" {
			result.Status, result.Message = "upload_failed", "Slack returned no file ID or upload URL"
			return uploadToolResult(result, true)
		}
		result.FileIDs = append(result.FileIDs, upload.FileID)
		if err := api.UploadToURL(ctx, slack.UploadToURLParameters{UploadURL: upload.UploadURL, Filename: file.filename, Reader: readers[i]}); err != nil {
			result.Status, result.Message = "upload_failed", safeUploadError(err)
			return uploadToolResult(result, true)
		}
		summaries = append(summaries, slack.FileSummary{ID: upload.FileID, Title: file.title})
	}
	_, err = api.CompleteUploadExternalContext(ctx, slack.CompleteUploadExternalParameters{Files: summaries, Channel: plan.Channel, ThreadTimestamp: plan.ThreadTS, InitialComment: plan.InitialComment})
	if err != nil {
		result.Status, result.Message, result.DoNotRetry = "sharing_unknown", "Sharing may have succeeded. Verify these file IDs in the conversation before taking further action; do not upload again. "+safeUploadError(err), true
		return uploadToolResult(result, true)
	}
	message, err := ch.verifyUploadedMessage(ctx, plan.Channel, plan.ThreadTS, result.FileIDs)
	if err != nil {
		result.Status, result.Message, result.DoNotRetry = "posted_unverified", "Read the conversation to verify the upload; do not upload again. "+safeUploadError(err), true
		return uploadToolResult(result, true)
	}
	result.Status, result.Verified, result.MessageTS = "posted", true, message.Timestamp
	return uploadToolResult(result, false)
}

func hasUploadedFiles(message slack.Message, ids []string, thread string) bool {
	if thread != "" && (message.Timestamp == thread || message.ThreadTimestamp != thread) {
		return false
	}
	if thread == "" && message.ThreadTimestamp != "" && message.ThreadTimestamp != message.Timestamp {
		return false
	}
	if len(message.Files) != len(ids) {
		return false
	}
	found := make(map[string]bool, len(message.Files))
	for _, file := range message.Files {
		found[file.ID] = true
	}
	for _, id := range ids {
		if !found[id] {
			return false
		}
	}
	return messageTimestampPattern.MatchString(message.Timestamp)
}

func (ch *ConversationsHandler) verifyUploadedMessage(ctx context.Context, channel, thread string, ids []string) (slack.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for attempt := 0; attempt < 10; attempt++ {
		cursor := ""
		seen := map[string]bool{}
		for page := 0; page < 20; page++ {
			var messages []slack.Message
			var more bool
			var next string
			var err error
			if thread != "" {
				messages, more, next, err = ch.apiProvider.Slack().GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{ChannelID: channel, Timestamp: thread, Limit: 100, Cursor: cursor})
			} else {
				var history *slack.GetConversationHistoryResponse
				history, err = ch.apiProvider.Slack().GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{ChannelID: channel, Limit: 100, Cursor: cursor})
				if err == nil && history == nil {
					err = errors.New("empty conversation history response")
				}
				if err == nil {
					messages, more, next = history.Messages, history.HasMore, history.ResponseMetaData.NextCursor
				}
			}
			if err != nil {
				return slack.Message{}, err
			}
			for _, message := range messages {
				if hasUploadedFiles(message, ids, thread) {
					return message, nil
				}
			}
			if !more || next == "" || seen[next] {
				break
			}
			seen[next], cursor = true, next
		}
		if attempt < 9 {
			delay := min(2*time.Second, 200*time.Millisecond*time.Duration(1<<attempt))
			select {
			case <-ctx.Done():
				return slack.Message{}, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return slack.Message{}, errors.New("uploaded files are not visible together in one message yet")
}
