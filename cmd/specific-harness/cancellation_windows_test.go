//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func prepareCancellationProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func interruptCancellationProcess(cmd *exec.Cmd) error {
	// Go maps CTRL_BREAK to os.Interrupt. Process.Signal(os.Interrupt) is
	// unsupported on Windows; target only this fixture's child process group.
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
}
