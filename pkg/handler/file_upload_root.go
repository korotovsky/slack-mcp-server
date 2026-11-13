package handler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Open relative to an allowed directory handle. O_NOFOLLOW alone protects only
// the final component; os.Root also prevents ancestor symlink swaps escaping it.
func openUploadFileInRoots(path string, allowed []string) (*os.File, int64, error) {
	limit, err := UploadMaxBytes()
	if err != nil {
		return nil, 0, err
	}
	for _, directory := range allowed {
		if !filepath.IsAbs(directory) {
			continue
		}
		rootPath, err := filepath.EvalSymlinks(directory)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootPath, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			return nil, 0, err
		}
		file, err := root.OpenFile(rel, os.O_RDONLY|openUploadExtraFlags, 0)
		_ = root.Close()
		if err != nil {
			return nil, 0, err
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
			_ = file.Close()
			if err != nil {
				return nil, 0, err
			}
			return nil, 0, fmt.Errorf("upload source must be a non-empty regular file of at most %d bytes", limit)
		}
		return file, info.Size(), nil
	}
	return nil, 0, errors.New("upload source is outside the allowed directories")
}
