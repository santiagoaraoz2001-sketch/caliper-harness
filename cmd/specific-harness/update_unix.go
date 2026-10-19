//go:build !windows

package main

import (
	"os"
	"specific-harness/internal/update"
	"syscall"
)

// Replace this launcher process so terminal signals and the final exit status
// belong directly to the selected Caliper, with no waiting shell underneath it.
func runSelected(s update.Selection) (int, error) {
	return 1, syscall.Exec(s.Executable(), append([]string{s.Executable()}, os.Args[1:]...), s.Env(os.Environ()))
}
