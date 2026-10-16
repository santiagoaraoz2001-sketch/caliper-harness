//go:build !darwin

package serve

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
)

// MLX is Apple-only. Off darwin these entry points exist only so shared
// callers compile; every path into them is unreachable because no
// catalog, recipe, or discovery row carries the mlx runtime here. If one
// is reached anyway, it refuses by name instead of emulating.
func PrepareMLX(r Recipe, bootstrap, path string) (Recipe, error) {
	return Recipe{}, fmt.Errorf("MLX serving is macOS-only; this is a %s build", runtime.GOOS)
}

func OwnedMLXReady(ctx context.Context, client *http.Client, p *Proc) (Health, error) {
	return Health{}, fmt.Errorf("MLX serving is macOS-only; this is a %s build", runtime.GOOS)
}
