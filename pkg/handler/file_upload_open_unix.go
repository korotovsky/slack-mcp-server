//go:build !windows

package handler

import "syscall"

// openUploadExtraFlags hardens the upload open() against a file swapped in
// between validation and read: O_NOFOLLOW refuses a symlink in the final path
// component, O_NONBLOCK keeps a FIFO from blocking the handler forever.
const openUploadExtraFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK
