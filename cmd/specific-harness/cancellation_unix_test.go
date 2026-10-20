//go:build !windows

package main

import (
	"os"
	"os/exec"
)

func prepareCancellationProcess(*exec.Cmd) {}

func interruptCancellationProcess(cmd *exec.Cmd) error {
	return cmd.Process.Signal(os.Interrupt)
}
