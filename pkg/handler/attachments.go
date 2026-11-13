package handler

import (
	"context"
	"crypto/sha256"
	"errors"
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

// downloadAttachmentToDir is retained for callers that only need a path.
func downloadAttachmentToDir(ctx context.Context, api fileDownloader, dir string, file *slack.File, downloadURL string, maxBytes int64) (string, int64, error) {
	path, size, _, err := downloadAttachmentWithDigest(ctx, api, dir, file, downloadURL, maxBytes)
	return path, size, err
}

// downloadAttachmentWithDigest publishes only complete, verified bytes and
// never replaces an existing file, including under concurrent downloads.
func downloadAttachmentWithDigest(ctx context.Context, api fileDownloader, dir string, file *slack.File, downloadURL string, maxBytes int64) (string, int64, string, error) {
	if file.Size < 0 || int64(file.Size) > maxBytes {
		return "", 0, "", fmt.Errorf("invalid or oversized attachment: %d bytes", file.Size)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, "", fmt.Errorf("attachment directory: %w", err)
	}
	name := attachmentFileName(file)
	f, err := os.CreateTemp(dir, ".attachment-*.partial")
	if err != nil {
		return "", 0, "", err
	}
	partial := f.Name()
	defer os.Remove(partial)
	hash := sha256.New()
	w := &limitedWriter{w: io.MultiWriter(f, hash), limit: int64(file.Size)}
	err = api.GetFileContext(ctx, downloadURL, w)
	if err == nil && w.written != int64(file.Size) {
		err = fmt.Errorf("attachment size mismatch: received %d bytes, Slack reported %d", w.written, file.Size)
	}
	if err == nil {
		err = ctx.Err()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", 0, "", err
	}
	final, err := publishAttachment(ctx, partial, dir, name, os.Link)
	if err != nil {
		return "", 0, "", err
	}
	return final, w.written, fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func publishAttachment(ctx context.Context, partial, dir, name string, link func(string, string) error) (string, error) {
	// Hard-link publication is atomic and fails when the destination exists.
	// Checking existence before Rename would leave an overwrite race.
	for index := 0; ; index++ {
		candidate := name
		if index > 0 {
			ext := filepath.Ext(name)
			candidate = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), index, ext)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		final := filepath.Join(dir, candidate)
		if err := link(partial, final); err == nil {
			return final, nil
		} else if !errors.Is(err, os.ErrExist) {
			// Some FUSE/network filesystems do not support hard links. Reserve
			// a private, unique child directory and atomically rename into it.
			// No existing user path is replaced in this fallback either.
			reserved, reserveErr := os.MkdirTemp(dir, "download-*")
			if reserveErr != nil {
				return "", fmt.Errorf("reserve attachment directory: %w", reserveErr)
			}
			final = filepath.Join(reserved, name)
			if cancelErr := ctx.Err(); cancelErr != nil {
				_ = os.Remove(reserved)
				return "", cancelErr
			}
			if renameErr := os.Rename(partial, final); renameErr != nil {
				_ = os.Remove(reserved)
				return "", fmt.Errorf("publish attachment in reserved directory: %w", renameErr)
			}
			return final, nil
		}
	}
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
	if int64(len(p)) > l.limit-l.written {
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
	desc += " Verifies Slack's exact byte count and returns SHA-256. Disk downloads never overwrite existing files."
	return desc
}
