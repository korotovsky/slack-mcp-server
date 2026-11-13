package handler

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

const (
	// attachmentDownloadTimeout bounds a single attachment_get_data download.
	// Large files are streamed, so the HTTP client's 30s request timeout does
	// not apply to them; this does.
	attachmentDownloadTimeout = 10 * time.Minute

	// maxAttachmentNameBytes leaves room under the usual 255-byte NAME_MAX for
	// the ".<name>.<random>.partial" name used while a download is in flight.
	maxAttachmentNameBytes = 200
)

// AttachmentMaxBytes is the largest file attachment_get_data will fetch:
// SLACK_MCP_ATTACHMENT_MAX_BYTES when set, 5 MiB otherwise.
func AttachmentMaxBytes() (int64, error) {
	v := strings.TrimSpace(os.Getenv("SLACK_MCP_ATTACHMENT_MAX_BYTES"))
	if v == "" {
		return maxFileSizeBytes, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("SLACK_MCP_ATTACHMENT_MAX_BYTES must be a positive number of bytes, got %q", v)
	}
	return n, nil
}

// fileDownloader is the slice of the Slack API a download needs.
type fileDownloader interface {
	GetFileContext(ctx context.Context, downloadURL string, writer io.Writer) error
}

// downloadAttachmentToDir streams a file into dir and returns its path and
// size. The bytes go to a hidden ".partial" file first and are renamed into
// place only once complete, so a reader never sees a truncated file and a
// failed download leaves nothing behind. Downloading the same file again
// replaces the earlier copy.
func downloadAttachmentToDir(ctx context.Context, api fileDownloader, dir string, file *slack.File, downloadURL string, maxBytes int64) (string, int64, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, fmt.Errorf("attachment directory: %w", err)
	}
	name := attachmentFileName(file)
	final := filepath.Join(dir, name)
	partial := filepath.Join(dir, "."+name+"."+rand.Text()+".partial")

	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", 0, err
	}
	w := &limitedWriter{w: f, limit: maxBytes}
	err = api.GetFileContext(ctx, downloadURL, w)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(partial, final)
	}
	if err != nil {
		_ = os.Remove(partial)
		return "", 0, err
	}
	return final, w.written, nil
}

// attachmentFileName is "<file ID>-<file name>", made safe to use as a single
// path component: separators, characters Windows rejects and control
// characters become "_", and overlong names are shortened keeping the
// extension. The file ID prefix keeps names unique and never starts with ".".
func attachmentFileName(file *slack.File) string {
	name := file.ID
	if file.Name != "" {
		name += "-" + file.Name
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.ToValidUTF8(name, "_"))
	if name == "" || name == "." || name == ".." {
		name = "attachment"
	}
	if len(name) <= maxAttachmentNameBytes {
		return name
	}
	ext := filepath.Ext(name)
	if len(ext) > 16 {
		ext = ""
	}
	stem := name[:maxAttachmentNameBytes-len(ext)]
	for !utf8.ValidString(stem) {
		stem = stem[:len(stem)-1]
	}
	return stem + ext
}

// limitedWriter fails once more than limit bytes have been written, so a
// download cannot outgrow the size Slack reported for the file.
type limitedWriter struct {
	w       io.Writer
	limit   int64
	written int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.written+int64(len(p)) > l.limit {
		return 0, fmt.Errorf("file exceeds maximum allowed size of %d bytes", l.limit)
	}
	n, err := l.w.Write(p)
	l.written += int64(n)
	return n, err
}

// formatBytes renders a byte count for tool descriptions: "5 MiB", "1 GiB".
func formatBytes(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%d GiB", n>>30)
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// AttachmentToolDescription describes attachment_get_data as configured by
// SLACK_MCP_ATTACHMENT_DIR and SLACK_MCP_ATTACHMENT_MAX_BYTES.
func AttachmentToolDescription() string {
	desc := "Download an attachment's content by file ID. Returns file metadata and content (text files as-is, images as image content, other binary files as base64)."
	if os.Getenv("SLACK_MCP_ATTACHMENT_DIR") != "" {
		desc = "Download an attachment by file ID into a local file. Returns file metadata and the local path of the downloaded file, not its content."
	}
	if maxBytes, err := AttachmentMaxBytes(); err == nil {
		desc += " Maximum file size is " + formatBytes(maxBytes) + "."
	}
	return desc
}
