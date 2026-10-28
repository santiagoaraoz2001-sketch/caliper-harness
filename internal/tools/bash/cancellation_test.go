package bash

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/tool"
)

func TestCancellationRetainsCappedOutput(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "timeout"}[timeout], func(t *testing.T) {
			cfg := testCfg()
			cfg.MaxBytes = 32
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			command := "printf 'RETAINED-abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz'; sleep 5"
			var res tool.Result
			var err error
			if timeout {
				res, err = New(cfg).Execute(ctx, tool.Args{"cmd": command, "timeout": int64(1)})
			} else {
				// Cancellation must follow output, not an assumed shell startup time.
				// A relative sentinel avoids Windows drive-path shell quoting.
				dir := t.TempDir()
				command = strings.Replace(command, "; sleep 5", "; : > ready; sleep 5", 1)
				done := make(chan struct{})
				go func() {
					defer close(done)
					res, err = NewAt(cfg, dir).Execute(ctx, tool.Args{"cmd": command, "timeout": int64(cfg.MaxTimeout)})
				}()
				tick := time.NewTicker(10 * time.Millisecond)
				defer tick.Stop()
				deadline := time.NewTimer(time.Duration(cfg.MaxTimeout) * time.Second)
				defer deadline.Stop()
			waitForOutput:
				for {
					select {
					case <-done:
						t.Fatalf("shell stopped before output readiness: %+v, %v", res, err)
					case <-deadline.C:
						cancel()
						<-done
						t.Fatalf("shell did not signal output readiness: %+v, %v", res, err)
					case <-tick.C:
						if _, statErr := os.Stat(filepath.Join(dir, "ready")); statErr == nil {
							cancel()
							break waitForOutput
						}
					}
				}
				<-done
			}
			cause := context.Canceled
			if timeout {
				cause = context.DeadlineExceeded
			}
			if !errors.Is(err, cause) || !res.Failed || !res.Truncated || !strings.HasPrefix(res.Text, "RETAINED-") || strings.Contains(res.Text, "abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz") {
				t.Fatalf("partial capped result lost: %+v, %v", res, err)
			}
			if strings.Contains(err.Error(), "(max ") != timeout {
				t.Fatalf("timeout recovery advice must describe a deadline, not user cancellation: %v", err)
			}
		})
	}
}
