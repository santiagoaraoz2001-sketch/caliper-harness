package bash

import (
	"context"
	"errors"
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
			if !timeout {
				time.AfterFunc(100*time.Millisecond, cancel)
			}
			res, err := New(cfg).Execute(ctx, tool.Args{"cmd": "printf 'RETAINED-abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz'; sleep 5", "timeout": int64(1)})
			cause := context.Canceled
			if timeout {
				cause = context.DeadlineExceeded
			}
			if !errors.Is(err, cause) || !res.Failed || !res.Truncated || !strings.HasPrefix(res.Text, "RETAINED-") || strings.Contains(res.Text, "abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz") {
				t.Fatalf("partial capped result lost: %+v, %v", res, err)
			}
		})
	}
}
