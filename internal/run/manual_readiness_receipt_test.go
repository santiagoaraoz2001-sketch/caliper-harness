package run

import (
	"context"
	"encoding/json"
	"io"
	"specific-harness/internal/auth"
	"specific-harness/internal/launch"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"strings"
	"testing"
)

func TestPublicManualReadinessDenialHasOwningDurableReceipt(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	s := openAssignedStore(t, root)
	task := assignedFixtureTask(t, s, "true", "true")
	a, err := plan.TaskApproach(s, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := plan.ReadinessSnapshot(s, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := lifecycleAuthConfig(t)
	cfg.Templates.Path = assignedTemplates()
	cfg.Decision.Disabled = true
	calls := 0
	d, err := LaunchDeps(cfg, s, "http://unused", "fixture", 0, 32768, strings.NewReader(""), io.Discard, func(context.Context, string) bool { calls++; return false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = launch.CheckReady(t.Context(), d, a.ID); err == nil || calls != 1 {
		t.Fatalf("denial err=%v calls=%d", err, calls)
	}
	ids, err := session.List(cfg.Sessions.Path)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, id := range ids {
		events, err := session.Read(cfg.Sessions.Path, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Kind != "approval" {
				continue
			}
			var receipt auth.ApprovalEvent
			if err := json.Unmarshal([]byte(event.Text), &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.Kind == "readiness" {
				found++
				if receipt.Approved || receipt.Cancelled || receipt.Snapshot != before || receipt.ID != a.ID || receipt.Workspace != root || receipt.Session != id || receipt.Store != s.Identity() {
					t.Fatalf("wrong owner %+v", receipt)
				}
			}
		}
	}
	if found != 1 {
		t.Fatalf("readiness receipts=%d", found)
	}
	current, err := plan.GetTask(s, task.ID)
	if err != nil || current.Attempts != 0 || current.State != plan.TaskQueued {
		t.Fatal("denial changed task", err)
	}
}
