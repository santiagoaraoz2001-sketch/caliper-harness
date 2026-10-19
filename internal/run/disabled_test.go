package run

import (
	"context"
	"io"
	"path/filepath"
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestDisabledDecisionFactorySkipsAdviceAndPreservesApproval(t *testing.T) {
	cfg := lifecycleAuthConfig(t)
	cfg.Decision.Disabled = true
	cfg.Auth.Mode = config.AuthRisky
	s, err := store.Open(filepath.Join(t.TempDir(), "graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	asked := 0
	r, notes, err := New(Deps{Cfg: cfg, Store: s, Server: "http://unused", Model: "component", Window: 32768, In: strings.NewReader(""), Out: io.Discard, AskYesNo: func(context.Context, string) bool { asked++; return false }}, "ordinary coding")
	if err != nil {
		t.Fatal(err)
	}
	if r.Judge != nil || r.ThinkingLoop != (agent.ThinkingLoop{}) || !strings.Contains(strings.Join(notes, " "), "decision model disabled") {
		t.Fatal("disabled inference still configured")
	}
	bash, _ := r.Registry.Lookup("bash")
	result, err := r.Exec(context.Background(), bash, tool.Args{"cmd": "echo not-authorized"})
	if err != nil || !result.Blocked || asked != 1 {
		t.Fatalf("manual policy missing: %+v %v asked%d", result, err, asked)
	}
	d, err := LaunchDeps(cfg, s, "http://unused", "component", 0, 32768, strings.NewReader("n\n"), io.Discard)
	if err != nil || !d.ManualReadiness || d.Judge != nil || d.ThinkingLoop != (agent.ThinkingLoop{}) {
		t.Fatalf("child policy missing: %+v %v", d, err)
	}
}
