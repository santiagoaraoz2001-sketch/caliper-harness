//go:build windows

package search

import (
	"errors"
	"syscall"
)

// ERROR_INVALID_NAME ("the filename, directory name, or volume label syntax
// is incorrect") is not named by the standard library.
const errorInvalidName = syscall.Errno(123)

// illegalFilename reports whether err refuses a non-UTF8 name at creation:
// the filesystem cannot represent it, so the native byte route is
// unexercised on this machine. Windows maps undecodable names into its
// ANSI codepage instead, so creation usually succeeds and the test below
// exercises the tools' refusal instead.
func illegalFilename(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == errorInvalidName
}
