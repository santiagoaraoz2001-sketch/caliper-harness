//go:build !windows && !linux && !darwin

package serve

import (
	"context"
	"fmt"
	"runtime"
)

// loopbackListeners has no implementation on this platform; configured
// and user-registered endpoints still probe normally.
func loopbackListeners(ctx context.Context) ([]Listener, error) {
	return nil, fmt.Errorf("listener discovery unsupported on %s", runtime.GOOS)
}
