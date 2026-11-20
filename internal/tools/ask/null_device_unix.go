//go:build linux || darwin

package ask

import (
	"os"
	"syscall"
)

// An inherited null descriptor can belong to a different device filesystem
// from /dev/null. Rdev identifies the character device across those mounts.
func sameNullDevice(input, null os.FileInfo) bool {
	if input.Mode()&os.ModeCharDevice == 0 || null.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	inputStat, okInput := input.Sys().(*syscall.Stat_t)
	nullStat, okNull := null.Sys().(*syscall.Stat_t)
	return okInput && okNull && inputStat.Rdev == nullStat.Rdev
}
