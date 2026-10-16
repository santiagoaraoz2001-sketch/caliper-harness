//go:build unix

// Package process owns one ordinary subprocess group for a tool call.
package process

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Command cancels inherited children as well as their shell leader. It never
// searches for processes by name or touches another group. Detached sessions
// are outside this ownership boundary; this is not a process sandbox.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	// A detached child can retain an inherited output pipe. Bound pipe cleanup
	// after cancellation/leader exit rather than keeping the tool lease forever.
	cmd.WaitDelay = time.Second
	return cmd
}

// Run settles the command and its output pipes, then kills any ordinary
// children left behind after the leader exits. They cannot outlive the call.
func Run(cmd *exec.Cmd) error {
	err := cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return err
}
