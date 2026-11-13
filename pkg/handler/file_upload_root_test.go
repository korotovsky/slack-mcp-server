package handler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnitUploadAncestorSwapCannotEscapeAllowedRoot(t *testing.T) {
	dir := t.TempDir()
	allowed, outside := filepath.Join(dir, "allowed"), filepath.Join(dir, "outside")
	inside := filepath.Join(allowed, "subdir")
	require.NoError(t, os.MkdirAll(inside, 0o700))
	require.NoError(t, os.MkdirAll(outside, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(inside, "file.txt"), []byte("approved"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file.txt"), []byte("outside"), 0o600))
	resolved, err := resolveUploadFilePath(filepath.Join(inside, "file.txt"), []string{allowed})
	require.NoError(t, err)
	require.NoError(t, os.Rename(inside, filepath.Join(allowed, "original")))
	require.NoError(t, os.Symlink(outside, inside))
	file, _, err := openUploadFileInRoots(resolved, []string{allowed})
	if file != nil {
		_ = file.Close()
	}
	require.Error(t, err, "a directory swapped after validation must not authorize outside bytes")
}
