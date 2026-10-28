// Package pathutil resolves physical filesystem identity consistently across
// platforms, including Windows directory junctions.
package pathutil

import (
	"os"
	"runtime"
)

// IsLinkMode includes Windows reparse directories, which Go represents as
// ModeDir|ModeIrregular rather than ModeSymlink. They are not real child dirs.
func IsLinkMode(mode os.FileMode) bool {
	return mode&os.ModeSymlink != 0 || runtime.GOOS == "windows" && mode&os.ModeIrregular != 0
}
