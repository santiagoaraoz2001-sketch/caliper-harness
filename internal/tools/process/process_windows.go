//go:build windows

package process

import (
	"context"
	"os/exec"
	"time"
)

// Command bounds one ordinary subprocess for a tool call. Windows has no
// process groups: cancellation stops the leader itself, and grandchildren
// that detach survive it. Detached sessions are outside this ownership
// boundary; this is not a process sandbox.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Cancel = func() error {
		// Kill reports os.ErrProcessDone when the leader already exited;
		// that is the settled state, not a failure.
		return cmd.Process.Kill()
	}
	// A detached child can retain an inherited output pipe. Bound pipe cleanup
	// after cancellation/leader exit rather than keeping the tool lease forever.
	cmd.WaitDelay = time.Second
	return cmd
}

// Run settles the command and its output pipes. The leader cannot outlive
// the call; detached grandchildren can, since Windows has no group kill.
func Run(cmd *exec.Cmd) error {
	return cmd.Run()
}
