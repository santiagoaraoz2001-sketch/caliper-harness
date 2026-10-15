package tui

import (
	"path/filepath"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"testing"
)

func TestStoreBufferPoolCapturedViews(t *testing.T) {
	cfg := testCfg()
	cfg.Store.Path = filepath.Join(t.TempDir(), "db")
	cfg.Store.BufferPoolBytes = 64 << 20
	s, err := store.OpenWithBufferPool(cfg.Store.Path, uint64(cfg.Store.BufferPoolBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = plan.CreatePlan(s, "configured owner", "goal"); err != nil {
		t.Fatal(err)
	}
	m := New(cfg, "test")
	plansCmd, workflowsCmd := m.refreshPlans(""), m.refreshWorkflows()
	footerCmd := fetchLive(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))
	m.Cfg.Store.BufferPoolBytes = 256 << 20 // queued work retains its captured policy
	if msg := plansCmd().(plansLoadedMsg); msg.err != nil || len(msg.plans) != 1 {
		t.Fatalf("plans: %+v", msg)
	}
	if msg := workflowsCmd().(wfLoadedMsg); msg.err != nil {
		t.Fatalf("workflows: %+v", msg)
	}
	if msg := footerCmd().(statusLiveMsg); !msg.havePlan || msg.planTitle != "configured owner" {
		t.Fatalf("footer: %+v", msg)
	}
	m.Cfg.Store.BufferPoolBytes = 64 << 20
	m.rememberChat([]string{"policy retained κ"})
	rows, err := s.Query("MATCH(m:MemoryEntry) RETURN m.body AS body", nil)
	if err != nil || len(rows) != 1 || rows[0]["body"] != "policy retained κ" {
		t.Fatalf("configured write: %v %v", rows, err)
	}
}
