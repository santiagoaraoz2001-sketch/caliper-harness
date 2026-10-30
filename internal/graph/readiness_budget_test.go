package graph

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/auth"
	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tools/ask"
)

// The former nested-lock failure ignores context cancellation. Isolate each
// regression in a bounded subprocess so a recurrence fails without hanging the
// suite's store cleanup. No model service or native inference is involved.
func TestBoundedReadinessPersistsIndependentHostApproval(t *testing.T) {
	mode := os.Getenv("CALIPER_READINESS_BUDGET_CHILD")
	if mode == "" {
		for _, mode := range []string{"eof", "yes", "cancel", "deadline"} {
			t.Run(mode, func(t *testing.T) {
				input, err := os.Create(filepath.Join(t.TempDir(), "stdin"))
				if err != nil {
					t.Fatal(err)
				}
				defer input.Close()
				if mode == "yes" {
					if _, err := input.WriteString("y\n"); err != nil {
						t.Fatal(err)
					}
					if _, err := input.Seek(0, io.SeekStart); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBoundedReadinessPersistsIndependentHostApproval$", "-test.v")
				cmd.Env = append(os.Environ(), "CALIPER_READINESS_BUDGET_CHILD="+mode)
				cmd.Stdin = input
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("bounded readiness %s: %v\n%s", mode, err, out)
				}
			})
		}
		return
	}

	root := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(plan.WithProject(t.Context(), root))
	defer cancel()
	p, err := plan.CreatePlanContext(ctx, s, "p", "goal")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproachContext(ctx, s, p.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	x, err := plan.CreateTaskContext(ctx, s, a.ID, "task", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.SetTaskBlockContext(ctx, s, x.ID, "current", "target", "method", "assertions", "tests", "verify", "true", "true"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := plan.ReadinessSnapshot(s, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	input, err := ask.NewFileInput(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	sessions := t.TempDir()
	sink := NewSessionLog(New(s, "fixture", sessions))
	const sid = "readiness-budget-owner"
	const brief = "exact displayed readiness brief"
	gate := &auth.Gate{Mode: "yolo", In: input, Out: io.Discard}
	gate.Record = func(recordCtx context.Context, event auth.ApprovalEvent) error {
		if plan.ProjectRoot(recordCtx) != root || recordCtx.Err() != nil {
			return errors.New("recording lost scope or retained cancellation")
		}
		if err := sink.Append(recordCtx, sid, "workspace", root); err != nil {
			return err
		}
		event.Store, event.Workspace, event.Session = s.Identity(), root, sid
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		return sink.Append(recordCtx, sid, "approval", string(raw))
	}
	if mode == "cancel" {
		gate.AskFunc = func(context.Context, string) bool { cancel(); return true }
	}
	api := NewWithReady(s, "fixture", sessions, func(checkCtx context.Context, id string) (*plan.Approach, error) {
		return plan.Ready(checkCtx, s, id, func(checkCtx context.Context, _ *plan.Plan, _ []*plan.Task) error {
			if mode == "deadline" {
				// The deadline applies to the current operation, not store setup.
				var stop context.CancelFunc
				checkCtx, stop = context.WithTimeout(checkCtx, 20*time.Millisecond)
				defer stop()
				gate.AskFunc = func(c context.Context, _ string) bool { <-c.Done(); return true }
			}
			approved, err := gate.ConfirmReadiness(checkCtx, id, snapshot, brief, "approve?")
			if err != nil {
				return err
			}
			if !approved {
				return errors.New("readiness denied")
			}
			return nil
		})
	})
	scoped, err := api.Scoped(root, sid, true)
	if err != nil {
		t.Fatal(err)
	}
	limits := config.Defaults().Tools.GraphQL
	limits.MaxRows = 1 // two host log writes must not consume the model row budget
	out, operationErr := scoped.ExecBounded(ctx, `mutation($id:ID!){transitionApproach(id:$id,to:"ready"){id state}}`, "", map[string]any{"id": a.ID}, limits)
	if (operationErr == nil) != (mode == "yes") {
		t.Fatalf("mode=%s err=%v result=%s", mode, operationErr, out)
	}
	if mode == "cancel" && !errors.Is(operationErr, context.Canceled) {
		t.Fatal("explicit cancellation lost", operationErr)
	}
	if mode == "deadline" && (operationErr == nil || !errors.Is(operationErr, context.DeadlineExceeded) && !strings.Contains(operationErr.Error(), context.DeadlineExceeded.Error())) {
		t.Fatal("deadline lost", operationErr)
	}
	var result Result
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Effects) != 1 || result.Effects[0].Field != "transitionApproach" {
		t.Fatalf("nested host events polluted model effects: %s", out)
	}
	events, err := session.Read(sessions, sid)
	if err != nil || len(events) != 2 || events[0].Kind != "workspace" || events[1].Kind != "approval" {
		t.Fatalf("receipt not durable before return: %+v %v", events, err)
	}
	var receipt auth.ApprovalEvent
	if err := json.Unmarshal([]byte(events[1].Text), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Approved != (mode == "yes") || receipt.Cancelled != (mode == "cancel" || mode == "deadline") || receipt.Snapshot != snapshot || receipt.Brief != brief || receipt.ID != a.ID || receipt.Kind != "readiness" || receipt.Scope != "approach" || receipt.Store != s.Identity() || receipt.Workspace != root || receipt.Session != sid {
		t.Fatalf("wrong durable owner decision: %+v", receipt)
	}
	got, err := plan.GetApproach(s, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := plan.ApproachDraft
	if mode == "yes" {
		want = plan.ApproachReady
	}
	if got.State != want {
		t.Fatalf("state=%s want=%s", got.State, want)
	}
	task, err := plan.GetTask(s, x.ID)
	if err != nil || task.State != plan.TaskQueued || task.Attempts != 0 {
		t.Fatalf("approval claimed task: %+v %v", task, err)
	}
	// Raw operations must still obey the caller's own cancellation/deadline.
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if _, err := api.Exec(cancelled, `{version}`, "", nil); !errors.Is(err, context.Canceled) {
		t.Fatal("raw cancellation bypassed", err)
	}
	expired, stopDeadline := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stopDeadline()
	if _, err := api.Exec(expired, `{version}`, "", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("raw deadline bypassed", err)
	}
	t.Logf("%s: durable readiness receipt, state %s, no claim or outer budget contamination", mode, want)
}
