package launch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"strings"
	"testing"
)

func TestOwningInterfacePublicDefinitions(t *testing.T) {
	_, _, d := gateDeps(t, 1)
	reg := tool.NewRegistry()
	for _, def := range NewBuild(d) {
		reg.Register(def)
	}
	for _, name := range []string{"workflow_ready", "workflow_transition"} {
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("public lifecycle missing %s", name)
		}
	}
	defs, err := reg.NativeDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	native, _ := json.Marshal(defs)
	added := 0
	for _, def := range defs {
		if def.Function.Name == "workflow_ready" || def.Function.Name == "workflow_transition" {
			raw, _ := json.Marshal(def)
			added += len(raw)
		}
	}
	t.Logf("workflow build structured schema bytes=%d; two added definitions bytes=%d; byte measure, not tokenizer", len(native), added)

}
func TestOwningInterfaceFullBriefAndOverflow(t *testing.T) {
	_, id, d := gateDeps(t, 2)
	calls := 0
	d.Judge = func(_ context.Context, r decision.Request) (decision.Verdict, error) {
		calls++
		if !strings.Contains(r.Context, "current: now") || !strings.Contains(r.Context, "verify: run it") {
			t.Error("complete brief not judged", r.Context)
		}
		return decision.Verdict{Option: servesGoal, Confidence: 1}, nil
	}
	d.CoherenceMaxChars = 0
	if err := ReadyCheck(context.Background(), d, id); err != nil {
		t.Fatal(err)
	}
	calls = 0
	d.CoherenceMaxChars = 1
	if err := ReadyCheck(context.Background(), d, id); err == nil || calls != 0 {
		t.Errorf("oversize input judged: calls=%d err=%v", calls, err)
	}
}

// The judge and scripted worker/reviewer are component doubles. Bash, checks,
// registry validation, native storage and qualification are actual owners.
type owningScript struct{ called bool }

func (m *owningScript) Chat(_ context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	last := msgs[len(msgs)-1].Content
	if strings.HasPrefix(last, "Review ") {
		return "PASS: declared artifact check", "component", llm.Usage{}, nil
	}
	if !m.called {
		m.called = true
		return "```tool\ntool: bash\ncmd: printf artifact > artifact.txt\n```", "component", llm.Usage{}, nil
	}
	return "Artifact created.", "component", llm.Usage{}, nil
}
func TestOwningRegistryLifecycle(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := depsFor(s, nil)
	d.Workspace = dir
	d.Wf.VerifyFollowup = true
	d.NewModel = func() agent.Model { return &owningScript{} }
	d.NewRegistry = func() *tool.Registry {
		r := tool.NewRegistry()
		r.Register(bash.NewAt(config.Defaults().Tools.Bash, dir))
		return r
	}
	r := tool.NewRegistry()
	for _, def := range NewBuild(d) {
		r.Register(def)
	}
	r.Register(New(d))
	call := func(name string, args map[string]string) tool.Result {
		t.Helper()
		def, ok := r.Lookup(name)
		if !ok {
			t.Fatal(name)
		}
		parsed, repair := tool.Validate(def, args)
		if repair != "" {
			t.Fatal(repair)
		}
		result, e := def.Execute(context.Background(), parsed)
		if e != nil || result.Failed || result.Blocked {
			t.Fatalf("%s: %+v %v", name, result, e)
		}
		return result
	}
	id := func(res tool.Result) string { return lastWord(res.Text) }
	pid := id(call("workflow_plan", map[string]string{"title": "plan", "goal": "create artifact"}))
	if _, err = plan.BindProject(t.Context(), s, pid, dir); err != nil {
		t.Fatal(err)
	}
	call("workflow_transition", map[string]string{"kind": "plan", "id": pid, "to": "active"})
	aid := id(call("workflow_approach", map[string]string{"plan": pid, "title": "approach"}))
	tid := id(call("workflow_task", map[string]string{"approach": aid, "title": "artifact", "prompt": "Create artifact.txt containing artifact"}))
	block := map[string]string{"task": tid, "current": "absent", "target": "artifact", "sketch": "write artifact.txt", "acceptance": "declared check exit code 0", "tests": "test file contents", "verify": "Review artifact and actual declared check", "check_command": "test \"$(cat artifact.txt)\" = artifact"}
	block["acceptance_command"] = `test "$(cat artifact.txt)" = artifact`
	call("workflow_block", block)
	call("workflow_ready", map[string]string{"approach": aid})
	result := call("launch", map[string]string{"approach": aid})
	if result.Workflow == nil || !result.Workflow.Qualified {
		t.Fatalf("unqualified %+v", result)
	}
	raw, e := os.ReadFile(filepath.Join(dir, "artifact.txt"))
	if e != nil || string(raw) != "artifact" {
		t.Fatalf("actual artifact %q %v", raw, e)
	}
	call("workflow_transition", map[string]string{"kind": "plan", "id": pid, "to": "complete"})
	old, _ := plan.GetTask(s, tid)
	call("workflow_transition", map[string]string{"kind": "task", "id": tid, "to": "failed"})
	a, _ := plan.GetApproach(s, aid)
	p, _ := plan.GetPlan(s, pid)
	task, _ := plan.GetTask(s, tid)
	if a.State != plan.ApproachPaused || p.State != plan.PlanActive || task.Attempts != old.Attempts || task.Result != old.Result || task.Qualified() {
		t.Fatalf("rework lost owner state %+v %+v %+v", a, p, task)
	}
	block["acceptance_command"] = `test "$(cat artifact.txt)" = artifact`
	call("workflow_block", block)
	call("workflow_transition", map[string]string{"kind": "task", "id": tid, "to": "queued"})
	call("workflow_ready", map[string]string{"approach": aid})
	call("launch", map[string]string{"approach": aid})
	call("workflow_transition", map[string]string{"kind": "plan", "id": pid, "to": "complete"})
	call("workflow_retire", map[string]string{"target": pid})
	p, _ = plan.GetPlan(s, pid)
	if p.State != plan.PlanRetired {
		t.Fatal(p.State)
	}
	def, _ := r.Lookup("workflow_transition")
	args, _ := tool.Validate(def, map[string]string{"kind": "task", "id": tid, "to": "complete"})
	if _, err := def.Execute(context.Background(), args); err == nil {
		t.Fatal("cross-kind state admitted")
	}
}
