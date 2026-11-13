//go:build windows

package handler

// Windows has no O_NOFOLLOW/O_NONBLOCK; the post-open IsRegular check still
// rejects anything that is not a plain file.
const openUploadExtraFlags = 0
