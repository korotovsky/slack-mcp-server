package handler

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// setEnvUpload sets an env var and returns a cleanup func restoring the old value.
func setEnvUpload(t *testing.T, key, value string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	require.NoError(t, os.Setenv(key, value))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func newUploadHandler() *ConversationsHandler {
	return &ConversationsHandler{logger: zap.NewNop()}
}

// newUploadRequest builds a CallToolRequest with the given string arguments.
func newUploadRequest(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = "file_upload"
	req.Params.Arguments = args
	return req
}

func TestUnitResolveUploadFilePath(t *testing.T) {
	root := t.TempDir()
	// Resolve the temp dir itself, macOS/CI may hand back a symlinked path.
	root, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)

	allowed := filepath.Join(root, "allowed")
	require.NoError(t, os.MkdirAll(allowed, 0o755))
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o755))

	goodFile := filepath.Join(allowed, "shot.png")
	require.NoError(t, os.WriteFile(goodFile, []byte("png"), 0o644))

	secretFile := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secretFile, []byte("secret"), 0o644))

	// A symlink that lives inside the allowlist but points outside of it.
	escapingLink := filepath.Join(allowed, "escape.txt")
	require.NoError(t, os.Symlink(secretFile, escapingLink))

	t.Run("rejects when allowlist is empty", func(t *testing.T) {
		_, err := resolveUploadFilePath(goodFile, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SLACK_MCP_FILE_UPLOAD_PATHS")
	})

	t.Run("rejects relative path", func(t *testing.T) {
		_, err := resolveUploadFilePath("relative/shot.png", []string{allowed})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "absolute")
	})

	t.Run("accepts file inside allowed root", func(t *testing.T) {
		got, err := resolveUploadFilePath(goodFile, []string{allowed})
		require.NoError(t, err)
		assert.Equal(t, goodFile, got)
	})

	t.Run("accepts file when one of several roots matches", func(t *testing.T) {
		got, err := resolveUploadFilePath(goodFile, []string{"/nonexistent", allowed})
		require.NoError(t, err)
		assert.Equal(t, goodFile, got)
	})

	t.Run("rejects file outside allowed root", func(t *testing.T) {
		_, err := resolveUploadFilePath(secretFile, []string{allowed})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed")
	})

	t.Run("rejects path traversal escaping the root", func(t *testing.T) {
		traversal := filepath.Join(allowed, "..", "outside", "secret.txt")
		_, err := resolveUploadFilePath(traversal, []string{allowed})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed")
	})

	t.Run("rejects symlink pointing outside the root", func(t *testing.T) {
		_, err := resolveUploadFilePath(escapingLink, []string{allowed})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed")
	})

	t.Run("rejects sibling directory sharing a name prefix", func(t *testing.T) {
		// /tmp/x/allowed must not authorise /tmp/x/allowed-evil
		evil := filepath.Join(root, "allowed-evil")
		require.NoError(t, os.MkdirAll(evil, 0o755))
		evilFile := filepath.Join(evil, "f.txt")
		require.NoError(t, os.WriteFile(evilFile, []byte("x"), 0o644))

		_, err := resolveUploadFilePath(evilFile, []string{allowed})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed")
	})

	t.Run("rejects directory", func(t *testing.T) {
		_, err := resolveUploadFilePath(allowed, []string{allowed})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "regular file")
	})

	t.Run("rejects missing file", func(t *testing.T) {
		_, err := resolveUploadFilePath(filepath.Join(allowed, "nope.txt"), []string{allowed})
		require.Error(t, err)
	})

	t.Run("root slash allows any absolute path", func(t *testing.T) {
		got, err := resolveUploadFilePath(secretFile, []string{"/"})
		require.NoError(t, err)
		assert.Equal(t, secretFile, got)
	})
}

func TestUnitParseUploadPathsEnv(t *testing.T) {
	t.Run("empty when unset", func(t *testing.T) {
		assert.Empty(t, parseUploadPathsEnv(""))
	})

	t.Run("splits and trims csv", func(t *testing.T) {
		assert.Equal(t, []string{"/tmp", "/home/max/Pictures"},
			parseUploadPathsEnv(" /tmp , /home/max/Pictures "))
	})

	t.Run("drops empty entries", func(t *testing.T) {
		assert.Equal(t, []string{"/tmp"}, parseUploadPathsEnv("/tmp,,  ,"))
	})
}

func TestUnitParseParamsToolFileUpload(t *testing.T) {
	ch := newUploadHandler()

	t.Run("disabled by default", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "")
		setEnvUpload(t, "SLACK_MCP_ENABLED_TOOLS", "")

		_, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id": "C123",
			"content":    "hello",
			"filename":   "a.txt",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SLACK_MCP_FILE_UPLOAD_TOOL")
	})

	t.Run("requires channel_id", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")

		_, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"content":  "hello",
			"filename": "a.txt",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "channel_id")
	})

	t.Run("requires exactly one content source", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")

		_, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id": "C123",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exactly one")

		_, err = ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id": "C123",
			"content":    "hello",
			"file_path":  "/tmp/a.txt",
			"filename":   "a.txt",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exactly one")
	})

	t.Run("requires filename for inline content", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")

		_, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id": "C123",
			"content":    "hello",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "filename")
	})

	t.Run("accepts inline text content", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")

		params, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id":      "C123",
			"content":         "log line",
			"filename":        "build.log",
			"initial_comment": "вот лог",
			"thread_ts":       "1234567890.123456",
		}))
		require.NoError(t, err)
		assert.Equal(t, "C123", params.channel)
		assert.Equal(t, "build.log", params.filename)
		assert.Equal(t, []byte("log line"), params.data)
		assert.Equal(t, "вот лог", params.initialComment)
		assert.Equal(t, "1234567890.123456", params.threadTs)
	})

	t.Run("decodes base64 content", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")

		raw := []byte{0x89, 'P', 'N', 'G'}
		params, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id":     "C123",
			"content_base64": base64.StdEncoding.EncodeToString(raw),
			"filename":       "shot.png",
		}))
		require.NoError(t, err)
		assert.Equal(t, raw, params.data)
		assert.Equal(t, "shot.png", params.filename)
	})

	t.Run("rejects malformed base64", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")

		_, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id":     "C123",
			"content_base64": "!!!not-base64!!!",
			"filename":       "shot.png",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "base64")
	})

	t.Run("strips directories from filename", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")

		params, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id": "C123",
			"content":    "x",
			"filename":   "../../etc/passwd",
		}))
		require.NoError(t, err)
		assert.Equal(t, "passwd", params.filename)
	})

	t.Run("rejects content larger than the limit", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")

		_, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id": "C123",
			"content":    strings.Repeat("a", maxUploadFileSizeBytes+1),
			"filename":   "big.txt",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds")
	})

	t.Run("honours channel allowlist in the env flag", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "C999")

		_, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id": "C123",
			"content":    "x",
			"filename":   "a.txt",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed")

		params, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id": "C999",
			"content":    "x",
			"filename":   "a.txt",
		}))
		require.NoError(t, err)
		assert.Equal(t, "C999", params.channel)
	})

	t.Run("reads file from an allowed path", func(t *testing.T) {
		dir := t.TempDir()
		dir, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)
		p := filepath.Join(dir, "report.csv")
		require.NoError(t, os.WriteFile(p, []byte("a,b\n1,2\n"), 0o644))

		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_PATHS", dir)

		params, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id": "C123",
			"file_path":  p,
		}))
		require.NoError(t, err)
		t.Cleanup(func() { params.file.Close() })
		assert.Equal(t, "report.csv", params.filename, "filename defaults to the basename")
		assert.Equal(t, p, params.filePath)
		assert.Equal(t, int64(8), params.size)
		require.NotNil(t, params.file, "file must be opened during validation, not later")
	})

	t.Run("rejects invalid thread_ts", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")

		_, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id": "C123",
			"thread_ts":  "not-a-timestamp",
			"content":    "x",
			"filename":   "a.txt",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "thread_ts")
	})

	t.Run("treats empty base64 as no source at all", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")

		_, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id":     "C123",
			"content_base64": "",
			"filename":       "empty.png",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exactly one")
	})

	t.Run("requires filename for base64 content", func(t *testing.T) {
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")

		_, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id":     "C123",
			"content_base64": base64.StdEncoding.EncodeToString([]byte("x")),
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "filename")
	})

	t.Run("rejects an empty file on disk", func(t *testing.T) {
		dir := t.TempDir()
		dir, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)
		p := filepath.Join(dir, "empty.log")
		require.NoError(t, os.WriteFile(p, nil, 0o644))

		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")
		setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_PATHS", dir)

		_, err = ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
			"channel_id": "C123",
			"file_path":  p,
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty")
	})
}

func TestUnitOpenUploadFile(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	t.Run("opens a regular file and reports its size", func(t *testing.T) {
		p := filepath.Join(dir, "ok.txt")
		require.NoError(t, os.WriteFile(p, []byte("12345"), 0o644))

		f, size, err := openUploadFile(p)
		require.NoError(t, err)
		defer f.Close()
		assert.Equal(t, int64(5), size)
	})

	t.Run("refuses to follow a symlink", func(t *testing.T) {
		// Guards the TOCTOU window: resolveUploadFilePath hands over a resolved
		// path, so anything still a symlink at open time was swapped in.
		target := filepath.Join(dir, "target.txt")
		require.NoError(t, os.WriteFile(target, []byte("secret"), 0o644))
		link := filepath.Join(dir, "swapped.txt")
		require.NoError(t, os.Symlink(target, link))

		_, _, err := openUploadFile(link)
		require.Error(t, err)
	})

	t.Run("refuses a directory", func(t *testing.T) {
		_, _, err := openUploadFile(dir)
		require.Error(t, err)
	})
}

func TestUnitParseParamsToolFileUploadRejectsPathOutsideAllowlist(t *testing.T) {
	ch := newUploadHandler()

	allowedDir := t.TempDir()
	otherDir := t.TempDir()
	p := filepath.Join(otherDir, "secret.env")
	require.NoError(t, os.WriteFile(p, []byte("TOKEN=1"), 0o644))

	setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_TOOL", "true")
	setEnvUpload(t, "SLACK_MCP_FILE_UPLOAD_PATHS", allowedDir)

	_, err := ch.parseParamsToolFileUpload(context.Background(), newUploadRequest(map[string]any{
		"channel_id": "C123",
		"file_path":  p,
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
}
