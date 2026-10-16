//go:build unix

package search

import (
	"errors"
	"syscall"
)

// illegalFilename reports whether err refuses a non-UTF8 name at creation:
// the filesystem cannot represent it, so the native byte route is
// unexercised on this machine.
func illegalFilename(err error) bool {
	return errors.Is(err, syscall.EILSEQ)
}
