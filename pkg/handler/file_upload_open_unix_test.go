//go:build !windows

package handler

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnitOpenUploadFileRefusesFifo(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}

	// Without O_NONBLOCK the open would hang forever waiting for a writer,
	// which would wedge the tool call with no way to cancel it.
	done := make(chan error, 1)
	go func() {
		f, _, err := openUploadFile(fifo)
		if f != nil {
			f.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
	case <-time.After(5 * time.Second):
		t.Fatal("openUploadFile blocked on a FIFO")
	}
}

func TestUnitOpenUploadFileRefusesDevice(t *testing.T) {
	if _, err := os.Stat("/dev/zero"); err != nil {
		t.Skip("/dev/zero unavailable")
	}
	f, _, err := openUploadFile("/dev/zero")
	if f != nil {
		f.Close()
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}
