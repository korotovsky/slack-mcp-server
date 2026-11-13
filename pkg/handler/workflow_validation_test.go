package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/korotovsky/slack-mcp-server/pkg/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Exercise the real Slack SDK and cookie transport against an HTTP fixture.
// These tests never contact Slack or use real credentials.
type workflowSlack struct {
	mutationSlack
}

func (s workflowSlack) PostMessageContext(ctx context.Context, channel string, opts ...slack.MsgOption) (string, string, error) {
	return s.client.PostMessageContext(ctx, channel, opts...)
}

func (s workflowSlack) UploadFileContext(ctx context.Context, params slack.UploadFileParameters) (*slack.FileSummary, error) {
	return s.client.UploadFileContext(ctx, params)
}

func (s workflowSlack) GetUploadURLExternalContext(ctx context.Context, params slack.GetUploadURLExternalParameters) (*slack.GetUploadURLExternalResponse, error) {
	return s.client.GetUploadURLExternalContext(ctx, params)
}

func (s workflowSlack) UploadToURL(ctx context.Context, params slack.UploadToURLParameters) error {
	return s.client.UploadToURL(ctx, params)
}

func (s workflowSlack) CompleteUploadExternalContext(ctx context.Context, params slack.CompleteUploadExternalParameters) (*slack.CompleteUploadExternalResponse, error) {
	return s.client.CompleteUploadExternalContext(ctx, params)
}

func (s workflowSlack) GetConversationHistoryContext(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
	return s.client.GetConversationHistoryContext(ctx, params)
}

func (s workflowSlack) GetConversationRepliesContext(ctx context.Context, params *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error) {
	return s.client.GetConversationRepliesContext(ctx, params)
}

func (s workflowSlack) GetFileInfoContext(ctx context.Context, id string, count, page int) (*slack.File, []slack.Comment, *slack.Paging, error) {
	return s.client.GetFileInfoContext(ctx, id, count, page)
}

func (s workflowSlack) GetFileContext(ctx context.Context, u string, w io.Writer) error {
	return s.client.GetFileContext(ctx, u, w)
}

func workflowHandler(serverURL string) *ConversationsHandler {
	httpClient := &http.Client{Transport: transport.NewUserAgentTransport(
		http.DefaultTransport, "test-only", []*http.Cookie{{Name: "d", Value: "xoxd-test-only"}}, zap.NewNop())}
	api := workflowSlack{mutationSlack{client: slack.New("xoxc-test-only", slack.OptionAPIURL(serverURL+"/"), slack.OptionHTTPClient(httpClient))}}
	return &ConversationsHandler{apiProvider: mutationProvider{api: api}, logger: zap.NewNop()}
}

func TestUnitWorkflowUploadBrowserSessionLargeThreadFile(t *testing.T) {
	t.Setenv("SLACK_MCP_FILE_UPLOAD_TOOL", "true")
	t.Setenv("SLACK_MCP_ENABLED_TOOLS", "")
	body := bytes.Repeat([]byte("test attachment\x00"), 420000) // larger than 5 MiB
	dir := t.TempDir()
	path := filepath.Join(dir, "attachment.bin")
	require.NoError(t, os.WriteFile(path, body, 0o600))
	t.Setenv("SLACK_MCP_FILE_UPLOAD_PATHS", dir)
	var steps []string
	var received []byte
	var complete url.Values
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		steps = append(steps, r.URL.Path)
		cookie, err := r.Cookie("d")
		require.NoError(t, err)
		require.Equal(t, "xoxd-test-only", cookie.Value)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/files.getUploadURLExternal":
			require.NoError(t, r.ParseForm())
			require.Equal(t, strconv.Itoa(len(body)), r.Form.Get("length"))
			require.Equal(t, "xoxc-test-only", r.Form.Get("token"))
			fmt.Fprintf(w, `{"ok":true,"file_id":"FTEST","upload_url":%q}`, srv.URL+"/upload")
		case "/upload":
			require.NoError(t, r.ParseMultipartForm(1<<20))
			defer r.MultipartForm.RemoveAll()
			f, _, err := r.FormFile("file")
			require.NoError(t, err)
			defer f.Close()
			received, err = io.ReadAll(f)
			require.NoError(t, err)
			fmt.Fprint(w, `OK`)
		case "/files.completeUploadExternal":
			require.NoError(t, r.ParseForm())
			complete = r.PostForm
			fmt.Fprint(w, `{"ok":true,"files":[{"id":"FTEST","title":"Test attachment"}]}`)
		case "/conversations.replies":
			fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"1234567891.123456","thread_ts":"1234567890.123456","files":[{"id":"FTEST"}]}]}`)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	h := workflowHandler(srv.URL)
	args := map[string]any{
		"channel_id": "C123", "file_path": path, "title": "Test attachment",
		"initial_comment": "First line\nSecond line", "thread_ts": "1234567890.123456",
	}
	preview, err := h.FileUploadHandler(context.Background(), newUploadRequest(args))
	require.NoError(t, err)
	var plan uploadResult
	require.NoError(t, json.Unmarshal([]byte(preview.Content[0].(mcp.TextContent).Text), &plan))
	require.Empty(t, steps, "preview must not call an upload API")
	args["dry_run"], args["expected_plan_hash"] = false, plan.PlanHash
	res, err := h.FileUploadHandler(context.Background(), newUploadRequest(args))
	require.NoError(t, err)
	require.Contains(t, res.Content[0].(mcp.TextContent).Text, "FTEST")
	require.Equal(t, []string{"/files.getUploadURLExternal", "/upload", "/files.completeUploadExternal", "/conversations.replies"}, steps)
	require.Equal(t, sha256.Sum256(body), sha256.Sum256(received))
	require.Equal(t, "C123", complete.Get("channel_id"))
	require.Equal(t, "1234567890.123456", complete.Get("thread_ts"))
	require.Equal(t, "First line\nSecond line", complete.Get("initial_comment"))
	var files []slack.FileSummary
	require.NoError(t, json.Unmarshal([]byte(complete.Get("files")), &files))
	require.Len(t, files, 1)
	require.Equal(t, "FTEST", files[0].ID)
	require.Equal(t, "Test attachment", files[0].Title)
}

func TestUnitWorkflowBroadcastRequest(t *testing.T) {
	t.Setenv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
	t.Setenv("SLACK_MCP_ADD_MESSAGE_MARK", "")
	for _, tc := range []struct {
		name, thread string
		broadcast    bool
		want         string
	}{
		{"thread broadcast", "1234567890.123456", true, "true"},
		{"ordinary thread reply", "1234567890.123456", false, ""},
		{"top-level ignores broadcast", "", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var form url.Values
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "/chat.postMessage", r.URL.Path)
				require.NoError(t, r.ParseForm())
				form = r.PostForm
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"ok":true,"channel":"C123","ts":"1234567891.123456"}`)
			}))
			defer srv.Close()
			req := newUploadRequest(map[string]any{"channel_id": "C123", "text": "Hello", "thread_ts": tc.thread, "reply_broadcast": tc.broadcast})
			req.Params.Name = "conversations_add_message"
			_, err := workflowHandler(srv.URL).ConversationsAddMessageHandler(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Equal(t, tc.want, form.Get("reply_broadcast"))
			require.Equal(t, tc.thread, form.Get("thread_ts"))
		})
	}
}

func TestUnitWorkflowDownloadLargeFileToDisk(t *testing.T) {
	t.Setenv("SLACK_MCP_ATTACHMENT_TOOL", "true")
	body := bytes.Repeat([]byte("download\x00"), 700000)
	dir := t.TempDir()
	t.Setenv("SLACK_MCP_ATTACHMENT_DIR", dir)
	t.Setenv("SLACK_MCP_ATTACHMENT_MAX_BYTES", strconv.Itoa(len(body)))
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NotEmpty(t, r.Header.Get("Cookie"))
		switch r.URL.Path {
		case "/files.info":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"ok":true,"file":{"id":"FTEST","name":"large.bin","size":%d,"mimetype":"application/octet-stream","url_private_download":%q}}`, len(body), srv.URL+"/download")
		case "/download":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	req := newUploadRequest(map[string]any{"file_id": "FTEST"})
	req.Params.Name = "attachment_get_data"
	res, err := workflowHandler(srv.URL).FilesGetHandler(context.Background(), req)
	require.NoError(t, err)
	metadata := res.Content[0].(mcp.TextContent).Text
	require.Less(t, len(metadata), 1024, "bytes must not be returned inside the MCP response")
	var info struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	require.NoError(t, json.Unmarshal([]byte(metadata), &info))
	require.Equal(t, int64(len(body)), info.Size)
	got, err := os.ReadFile(info.Path)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(body), sha256.Sum256(got))
}

func TestUnitWorkflowUploadEffectiveSelection(t *testing.T) {
	for _, tc := range []struct {
		name, config, selection, envSelection, wantError string
	}{
		{"CLI selection", "", "file_upload", "", ""},
		{"exact env selection", "", "", "file_upload_other", "disabled"},
		{"selection excludes upload", "true", "channels_list", "file_upload", "disabled"},
		{"channel policy survives selection", "C999", "file_upload", "", "not allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SLACK_MCP_FILE_UPLOAD_TOOL", tc.config)
			t.Setenv("SLACK_MCP_ENABLED_TOOLS", tc.envSelection)
			h := newUploadHandler()
			if tc.selection != "" {
				h.enabledTools = strings.Split(tc.selection, ",")
			}
			params, err := h.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
				"channel_id": "C123", "content": "test", "filename": "test.txt",
			}))
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
				require.Equal(t, "test", string(params.data))
			}
		})
	}
}
