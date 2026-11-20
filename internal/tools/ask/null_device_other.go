//go:build !linux && !darwin

package ask

import "os"

func sameNullDevice(input, null os.FileInfo) bool { return os.SameFile(input, null) }
