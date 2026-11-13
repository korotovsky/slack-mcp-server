package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/require"
)

func decodeUploadResult(t *testing.T, result *mcp.CallToolResult) uploadResult {
	t.Helper()
	var out uploadResult
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &out))
	return out
}

func TestUnitUploadSizeConfiguration(t *testing.T) {
	t.Setenv("SLACK_MCP_FILE_UPLOAD_MAX_BYTES", "")
	limit, err := UploadMaxBytes()
	require.NoError(t, err)
	require.Equal(t, int64(100<<20), limit)
	t.Setenv("SLACK_MCP_FILE_UPLOAD_MAX_BYTES", "536870912")
	limit, err = UploadMaxBytes()
	require.NoError(t, err)
	require.Equal(t, int64(512<<20), limit)
	t.Setenv("SLACK_MCP_FILE_UPLOAD_TOOL", "true")
	t.Setenv("SLACK_MCP_FILE_UPLOAD_MAX_BYTES", "3")
	_, err = newUploadHandler().parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{"channel_id": "C123", "content": "four", "filename": "test.txt"}))
	require.ErrorContains(t, err, "exceeds")
	for _, invalid := range []string{"0", "-1", "2GB", "1073741825"} {
		t.Setenv("SLACK_MCP_FILE_UPLOAD_MAX_BYTES", invalid)
		_, err := UploadMaxBytes()
		require.Error(t, err, invalid)
	}
}

func TestUnitBatchUploadPreviewSendAndPaginatedVerification(t *testing.T) {
	t.Setenv("SLACK_MCP_FILE_UPLOAD_TOOL", "C123")
	t.Setenv("SLACK_MCP_ENABLED_TOOLS", "")
	dir := t.TempDir()
	t.Setenv("SLACK_MCP_FILE_UPLOAD_PATHS", dir)
	t.Setenv("SLACK_MCP_FILE_UPLOAD_STAGING_DIR", filepath.Join(dir, "staging"))
	source := filepath.Join(dir, "first.txt")
	require.NoError(t, os.WriteFile(source, []byte("original"), 0o600))
	var summaries []slack.FileSummary
	counts := map[string]int{}
	contents := map[string]string{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counts[r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/files.getUploadURLExternal":
			// Changing the original after snapshotting cannot alter confirmed bytes.
			require.NoError(t, os.WriteFile(source, []byte("modified"), 0o600))
			id := fmt.Sprintf("F%d", counts[r.URL.Path])
			fmt.Fprintf(w, `{"ok":true,"file_id":%q,"upload_url":%q}`, id, server.URL+"/upload/"+id)
		case strings.HasPrefix(r.URL.Path, "/upload/"):
			require.NoError(t, r.ParseMultipartForm(1<<20))
			defer r.MultipartForm.RemoveAll()
			file, _, err := r.FormFile("file")
			require.NoError(t, err)
			defer file.Close()
			data, err := io.ReadAll(file)
			require.NoError(t, err)
			contents[filepath.Base(r.URL.Path)] = string(data)
			fmt.Fprint(w, "OK")
		case r.URL.Path == "/files.completeUploadExternal":
			require.NoError(t, r.ParseForm())
			require.Equal(t, "C123", r.Form.Get("channel_id"))
			require.Equal(t, "Two attachments\nOne message", r.Form.Get("initial_comment"))
			require.NoError(t, json.Unmarshal([]byte(r.Form.Get("files")), &summaries))
			fmt.Fprint(w, `{"ok":true,"files":[{"id":"F1"},{"id":"F2"}]}`)
		case r.URL.Path == "/conversations.history":
			require.NoError(t, r.ParseForm())
			if r.Form.Get("cursor") == "" {
				fmt.Fprint(w, `{"ok":true,"has_more":true,"messages":[],"response_metadata":{"next_cursor":"page2"}}`)
			} else {
				require.Equal(t, "page2", r.Form.Get("cursor"))
				fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"1234567891.123456","files":[{"id":"F1"},{"id":"F2"}]}]}`)
			}
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	handler := workflowHandler(server.URL)
	args := map[string]any{"channel_id": "#general", "initial_comment": "Two attachments\nOne message", "files": []map[string]any{
		{"file_path": source, "title": "First"}, {"content": "second", "filename": "second.txt", "title": "Second"},
	}}
	preview, err := handler.FileUploadHandler(context.Background(), newUploadRequest(args))
	require.NoError(t, err)
	plan := decodeUploadResult(t, preview)
	require.Empty(t, counts, "preview must make no upload/share requests")
	require.Equal(t, "preview", plan.Status)
	require.Equal(t, "C123", plan.Channel)
	require.Len(t, plan.Files, 2)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256([]byte("original"))), plan.Files[0].SHA256)
	_, err = os.Stat(filepath.Join(dir, "staging"))
	require.ErrorIs(t, err, os.ErrNotExist, "preview must not create staging files")
	args["dry_run"], args["expected_plan_hash"] = false, plan.PlanHash
	result, err := handler.FileUploadHandler(context.Background(), newUploadRequest(args))
	require.NoError(t, err)
	require.False(t, result.IsError)
	posted := decodeUploadResult(t, result)
	require.Equal(t, "posted", posted.Status)
	require.True(t, posted.Verified)
	require.Equal(t, "1234567891.123456", posted.MessageTS)
	require.Equal(t, 1, counts["/files.completeUploadExternal"], "all files must share one post")
	require.Equal(t, "original", contents["F1"], "upload the confirmed snapshot")
	require.Equal(t, "second", contents["F2"])
	require.Len(t, summaries, 2)
	require.Equal(t, "First", summaries[0].Title)
	require.Equal(t, "Second", summaries[1].Title)
	require.Empty(t, dirEntries(t, filepath.Join(dir, "staging")))
}

func TestUnitUploadAmbiguousSharingIsNeverRetried(t *testing.T) {
	for _, phase := range []string{"complete", "verify"} {
		t.Run(phase, func(t *testing.T) {
			t.Setenv("SLACK_MCP_FILE_UPLOAD_TOOL", "true")
			counts := map[string]int{}
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				counts[r.URL.Path]++
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/files.getUploadURLExternal":
					fmt.Fprintf(w, `{"ok":true,"file_id":"F1","upload_url":%q}`, server.URL+"/upload")
				case "/upload":
					_, _ = io.Copy(io.Discard, r.Body)
					fmt.Fprint(w, "OK")
				case "/files.completeUploadExternal":
					if phase == "complete" {
						fmt.Fprint(w, `{"ok":false,"error":"internal_error"}`)
					} else {
						fmt.Fprint(w, `{"ok":true,"files":[{"id":"F1"}]}`)
					}
				case "/conversations.history":
					fmt.Fprint(w, `{"ok":false,"error":"not_in_channel"}`)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
				}
			}))
			defer server.Close()
			handler := workflowHandler(server.URL)
			args := map[string]any{"channel_id": "C123", "content": "file", "filename": "file.txt", "initial_comment": "Attachment"}
			preview, err := handler.FileUploadHandler(context.Background(), newUploadRequest(args))
			require.NoError(t, err)
			args["dry_run"], args["expected_plan_hash"] = false, decodeUploadResult(t, preview).PlanHash
			result, err := handler.FileUploadHandler(context.Background(), newUploadRequest(args))
			require.NoError(t, err)
			require.True(t, result.IsError)
			state := decodeUploadResult(t, result)
			require.True(t, state.DoNotRetry)
			require.False(t, state.Verified)
			if phase == "complete" {
				require.Equal(t, "sharing_unknown", state.Status)
			} else {
				require.Equal(t, "posted_unverified", state.Status)
			}
			require.Equal(t, 1, counts["/files.completeUploadExternal"])
			require.Equal(t, 1, counts["/files.getUploadURLExternal"])
		})
	}
}

func TestUnitUploadVerificationWaitsForSharingAndChecksTheWholeBatch(t *testing.T) {
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		w.Header().Set("Content-Type", "application/json")
		if reads == 1 {
			// A partial batch is not a verified delivery.
			fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"1234567891.123456","files":[{"id":"F1"}]}]}`)
		} else {
			fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"1234567891.123456","files":[{"id":"F1"},{"id":"F2"}]}]}`)
		}
	}))
	defer server.Close()
	message, err := workflowHandler(server.URL).verifyUploadedMessage(context.Background(), "C123", "", []string{"F1", "F2"})
	require.NoError(t, err)
	require.Equal(t, 2, reads)
	require.Equal(t, "1234567891.123456", message.Timestamp)
}

func TestUnitUploadValidationAndPlanChanges(t *testing.T) {
	t.Setenv("SLACK_MCP_FILE_UPLOAD_TOOL", "true")
	for _, tc := range []struct {
		name  string
		extra map[string]any
		want  string
	}{
		{"no caption", map[string]any{"initial_comment": "  "}, "initial_comment"},
		{"empty files", map[string]any{"files": []any{}, "content": nil}, "cannot be combined"},
		{"too many", map[string]any{"files": make([]map[string]any, 11)}, "at most 10"},
		{"bad dry run", map[string]any{"dry_run": "false"}, "boolean"},
		{"unconfirmed send", map[string]any{"dry_run": false}, "expected_plan_hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"channel_id": "C123", "initial_comment": "Caption"}
			if _, multi := tc.extra["files"]; !multi {
				args["content"], args["filename"] = "file", "file.txt"
			}
			for key, value := range tc.extra {
				args[key] = value
			}
			_, err := workflowHandler("http://127.0.0.1:1").FileUploadHandler(context.Background(), newUploadRequest(args))
			require.ErrorContains(t, err, tc.want)
		})
	}
	args := map[string]any{"channel_id": "C123", "content": "before", "filename": "file.txt", "initial_comment": "Caption"}
	handler := workflowHandler("http://127.0.0.1:1")
	preview, err := handler.FileUploadHandler(context.Background(), newUploadRequest(args))
	require.NoError(t, err)
	args["dry_run"], args["expected_plan_hash"] = false, decodeUploadResult(t, preview).PlanHash
	for _, field := range []string{"content", "initial_comment", "channel_id", "title", "thread_ts"} {
		changed := make(map[string]any)
		for key, value := range args {
			changed[key] = value
		}
		changed[field] = "changed"
		if field == "thread_ts" {
			changed[field] = "1234567890.123456"
		}
		_, err := handler.FileUploadHandler(context.Background(), newUploadRequest(changed))
		require.ErrorContains(t, err, "expected_plan_hash", field)
	}
}

func TestUnitDownloadIntegrityAndCollisionSafety(t *testing.T) {
	dir := t.TempDir()
	file := &slack.File{ID: "F1", Name: "file.txt", Size: 3}
	path, _, hash, err := downloadAttachmentWithDigest(context.Background(), fakeDownloader{body: []byte("abc")}, dir, file, "unused", 10)
	require.NoError(t, err)
	require.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", hash)
	for _, body := range [][]byte{[]byte("ab"), []byte("abcd")} {
		_, _, _, err := downloadAttachmentWithDigest(context.Background(), fakeDownloader{body: body}, dir, file, "unused", 10)
		require.Error(t, err)
	}
	require.Len(t, dirEntries(t, dir), 1, "failed transfers leave no published or partial files")
	const n = 8
	var wg sync.WaitGroup
	paths := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, _, _, err := downloadAttachmentWithDigest(context.Background(), fakeDownloader{body: []byte("xyz")}, dir, file, "unused", 10)
			require.NoError(t, err)
			paths <- p
		}()
	}
	wg.Wait()
	close(paths)
	unique := map[string]bool{}
	for p := range paths {
		unique[p] = true
	}
	require.Len(t, unique, n)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, bytes.Equal([]byte("abc"), data), "existing bytes must remain untouched")
	require.Len(t, dirEntries(t, dir), n+1)
}

func TestUnitDownloadPublicationWithoutHardLinkSupport(t *testing.T) {
	dir := t.TempDir()
	partial := filepath.Join(dir, ".partial")
	existing := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(partial, []byte("new"), 0o600))
	require.NoError(t, os.WriteFile(existing, []byte("existing"), 0o600))
	final, err := publishAttachment(context.Background(), partial, dir, "file.txt", func(string, string) error { return errors.ErrUnsupported })
	require.NoError(t, err)
	require.NotEqual(t, existing, final)
	data, err := os.ReadFile(final)
	require.NoError(t, err)
	require.Equal(t, "new", string(data))
	data, err = os.ReadFile(existing)
	require.NoError(t, err)
	require.Equal(t, "existing", string(data))
}
