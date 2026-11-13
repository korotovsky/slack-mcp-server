package handler

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDownloader writes body to the writer in chunks, then returns failWith.
type fakeDownloader struct {
	body     []byte
	failWith error
}

func (f fakeDownloader) GetFileContext(_ context.Context, _ string, w io.Writer) error {
	for rest := f.body; len(rest) > 0; {
		n := min(len(rest), 4096)
		if _, err := w.Write(rest[:n]); err != nil {
			return err
		}
		rest = rest[n:]
	}
	return f.failWith
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestUnitDownloadAttachmentToDir_WritesFileAndReturnsPath(t *testing.T) {
	dir := t.TempDir()
	body := []byte(strings.Repeat("%PDF binary \x00\x01", 3000))
	file := &slack.File{ID: "F0TEST", Name: "report.pdf"}

	path, size, err := downloadAttachmentToDir(context.Background(), fakeDownloader{body: body}, dir, file, "https://files.example/x", int64(len(body)))

	require.NoError(t, err)
	assert.Equal(t, int64(len(body)), size)
	assert.Equal(t, filepath.Join(dir, "F0TEST-report.pdf"), path)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, []string{"F0TEST-report.pdf"}, dirEntries(t, dir), "no partial file left behind")
}

func TestUnitDownloadAttachmentToDir_ReplacesEarlierCopy(t *testing.T) {
	dir := t.TempDir()
	file := &slack.File{ID: "F0TEST", Name: "a.txt"}
	for _, body := range []string{"first version", "second"} {
		_, _, err := downloadAttachmentToDir(context.Background(), fakeDownloader{body: []byte(body)}, dir, file, "u", 1<<20)
		require.NoError(t, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "F0TEST-a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "second", string(got))
}

func TestUnitDownloadAttachmentToDir_FailuresLeaveNothing(t *testing.T) {
	tests := []struct {
		name     string
		dl       fakeDownloader
		maxBytes int64
	}{
		{"body larger than limit", fakeDownloader{body: make([]byte, 10_000)}, 9_999},
		{"download error midway", fakeDownloader{body: make([]byte, 5_000), failWith: errors.New("connection reset")}, 1 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			_, _, err := downloadAttachmentToDir(context.Background(), tt.dl, dir, &slack.File{ID: "F0TEST", Name: "x.bin"}, "u", tt.maxBytes)
			require.Error(t, err)
			assert.Empty(t, dirEntries(t, dir))
		})
	}
}

func TestUnitAttachmentFileName_IsOneSafePathComponent(t *testing.T) {
	long := strings.Repeat("é", 300) + ".pptx"
	for _, name := range []string{"../../etc/passwd", `a\b:c*d?"e<f>g|h`, "new\nline\x00.txt", "", "..", long} {
		t.Run(name, func(t *testing.T) {
			got := attachmentFileName(&slack.File{ID: "F0TEST", Name: name})
			dir := t.TempDir()
			assert.Equal(t, dir, filepath.Dir(filepath.Join(dir, got)), "stays directly inside the directory: %q", got)
			assert.True(t, strings.HasPrefix(got, "F0TEST"), got)
			assert.LessOrEqual(t, len(got), maxAttachmentNameBytes)
			assert.True(t, utf8.ValidString(got))
			assert.NotContains(t, got, "\x00")
		})
	}
	assert.True(t, strings.HasSuffix(attachmentFileName(&slack.File{ID: "F0TEST", Name: long}), ".pptx"), "extension survives shortening")
}

func TestUnitAttachmentMaxBytes(t *testing.T) {
	t.Setenv("SLACK_MCP_ATTACHMENT_MAX_BYTES", "")
	n, err := AttachmentMaxBytes()
	require.NoError(t, err)
	assert.Equal(t, int64(maxFileSizeBytes), n)

	t.Setenv("SLACK_MCP_ATTACHMENT_MAX_BYTES", "1073741824")
	n, err = AttachmentMaxBytes()
	require.NoError(t, err)
	assert.Equal(t, int64(1<<30), n)

	for _, bad := range []string{"0", "-1", "1GB", "lots"} {
		t.Setenv("SLACK_MCP_ATTACHMENT_MAX_BYTES", bad)
		_, err = AttachmentMaxBytes()
		assert.Error(t, err, bad)
	}
}

func TestUnitAttachmentToolDescription_FollowsConfiguration(t *testing.T) {
	t.Setenv("SLACK_MCP_ATTACHMENT_DIR", "")
	t.Setenv("SLACK_MCP_ATTACHMENT_MAX_BYTES", "")
	assert.Contains(t, AttachmentToolDescription(), "5 MiB")
	assert.NotContains(t, AttachmentToolDescription(), "local path")

	t.Setenv("SLACK_MCP_ATTACHMENT_DIR", t.TempDir())
	t.Setenv("SLACK_MCP_ATTACHMENT_MAX_BYTES", "1073741824")
	assert.Contains(t, AttachmentToolDescription(), "1 GiB")
	assert.Contains(t, AttachmentToolDescription(), "local path")
}
