//go:build !windows && !linux && !darwin

package serve

import (
	"fmt"
	"runtime"
)

// loopbackListeners has no implementation on this platform; configured
// and user-registered endpoints still probe normally.
func loopbackListeners() ([]Listener, error) {
	return nil, fmt.Errorf("listener discovery unsupported on %s", runtime.GOOS)
}
