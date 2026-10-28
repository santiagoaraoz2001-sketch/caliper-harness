//go:build !windows

package pathutil

import "path/filepath"

func EvalSymlinks(path string) (string, error) { return filepath.EvalSymlinks(path) }
