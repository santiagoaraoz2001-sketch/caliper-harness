package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/auth"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"specific-harness/internal/tools/files"
)

// Models and decision verdicts are scripted; owning Store, executor, approvals,
// source Reads and paired shell commands are real component controls.
type roleHandoffModel func([]llm.Message) (string, error)

func (m roleHandoffModel) Chat(_ context.Context, messages []llm.Message) (string, string, llm.Usage, error) {
	text, err := m(messages)
	return text, "role-component", llm.Usage{}, err
}

func roleHandoffFixture(t *testing.T, n int, model agent.Model) (Deps, string, []string, string) {
	t.Helper()
	s, aid, ids := setup(t, n)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.ApproachPlan(s, aid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.BindProject(context.Background(), s, p.ID, root); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionPlan(s, p.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	d := depsFor(s, model)
	d.Workspace = root
	d.NewRegistry = func() *tool.Registry {
		reg := tool.NewRegistry()
		reg.Register(bash.NewAt(config.Defaults().Tools.Bash, root))
		w := &files.Workspace{Path: root, Caps: config.Defaults().Tools.Bash}
		for _, def := range w.Definitions() {
			reg.Register(def)
		}
		return reg
	}
	// The original control must reach its scheduling/sink/gate seam even while
	// current role templates add new parent fields supplied by the final patch.
	dir := t.TempDir()
	writeLegacyLaunchCatalog(t, dir, map[string]string{"templates.subagent": "TASK {{.Title}}\n{{.Prompt}}", "templates.review": "Review {{.Title}}\n{{.Prompt}}"})
	d.TemplatesDir = dir
	return d, aid, ids, root
}

func TestRoleSequentialChecksAndReviewBeforeNextWorker(t *testing.T) {
	var d Deps
	var ids []string
	model := roleHandoffModel(func(messages []llm.Message) (string, error) {
		request := fixtureRoleRequest(messages)
		if strings.HasPrefix(request, "Review ") {
			return "## PASS\n\nComponent review of actual paired checks.", nil
		}
		if strings.HasPrefix(request, "TASK t1") {
			first, err := plan.GetTask(d.Store, ids[0])
			if err != nil || !first.Qualified() {
				t.Errorf("task2 started before task1 actual paired check and review: %+v %v", first, err)
			}
		}
		return "component artifact", nil
	})
	d, aid, ids, _ := roleHandoffFixture(t, 2, model)
	results, err := Launch(t.Context(), d, aid)
	if err != nil || len(results) != 2 || !results[0].Qualified() || !results[1].Qualified() {
		t.Fatalf("sequential qualified handoff: %+v %v", results, err)
	}
}

func TestRoleSequentialRejectedOrUnverifiedHoldsNextQueued(t *testing.T) {
	for _, verdict := range []string{"FAIL: concrete component finding", "UNVERIFIED: no independent verdict"} {
		t.Run(verdict, func(t *testing.T) {
			workers := 0
			model := roleHandoffModel(func(messages []llm.Message) (string, error) {
				if strings.HasPrefix(messages[len(messages)-1].Content, "Review ") {
					return verdict, nil
				}
				workers++
				return "component artifact", nil
			})
			d, aid, ids, _ := roleHandoffFixture(t, 2, model)
			results, err := Launch(t.Context(), d, aid)
			next, getErr := plan.GetTask(d.Store, ids[1])
			if err != nil || getErr != nil || workers != 1 || len(results) != 2 || !results[1].Skipped || next.State != plan.TaskQueued || next.Attempts != 0 || next.Result != "" || results[0].Qualified() {
				t.Fatalf("unqualified predecessor started ordered work: workers%d next%+v results%+v err%v/%v", workers, next, results, err, getErr)
			}
		})
	}
}

func TestRoleAllParentApprovalDoesNotGrantWorkerOrHostCheck(t *testing.T) {
	workerCalls := 0
	model := roleHandoffModel(func(messages []llm.Message) (string, error) {
		workerCalls++
		if workerCalls == 1 {
			return "```tool\ntool: bash\ncmd: touch worker-effect\n```", nil
		}
		return "component response after actual denial", nil
	})
	d, aid, _, root := roleHandoffFixture(t, 1, model)
	d.AuthMode = config.AuthAll
	parents, childBash, checks := 0, 0, 0
	d.Approve = func(context.Context, string) bool { parents++; return true }
	gate := &auth.Gate{Mode: config.AuthAll, AskFunc: func(_ context.Context, prompt string) bool {
		if strings.Contains(prompt, "worker-effect") {
			childBash++
		}
		if strings.Contains(prompt, `"tool": "check"`) {
			checks++
			if !strings.Contains(prompt, "FIXTURE_SUITE") || !strings.Contains(prompt, "FIXTURE_ACCEPTANCE") {
				t.Error("actual host-check approval omitted an episode command")
			}
		}
		return false
	}}
	d.ReviewGate = gate.Wrap
	result, err := New(d).Execute(t.Context(), tool.Args{"approach": aid})
	_, effectErr := os.Stat(filepath.Join(root, "worker-effect"))
	if err != nil || parents != 1 || childBash != 1 || checks != 1 || !os.IsNotExist(effectErr) || result.Workflow == nil || result.Workflow.Qualified {
		t.Fatalf("parent grant bypassed actual child/check denial: parents%d child%d check%d result%+v err%v effect%v", parents, childBash, checks, result, err, effectErr)
	}
}

func TestRoleWorkerDurableRawCallsAndSnapshotsSurviveSettlement(t *testing.T) {
	model := &loopModel{replies: []string{"```tool\ntool: read\npath: source.go\n```", "```tool\ntool: read\npath: source.go\n```", "component artifact"}}
	d, aid, ids, root := roleHandoffFixture(t, 1, model)
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("package fixture\nfunc Work() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	results, err := Launch(t.Context(), d, aid)
	actual, getErr := plan.GetTask(d.Store, ids[0])
	if err != nil || getErr != nil || len(results) != 1 {
		t.Fatalf("actual worker settlement %+v %v/%v", results, err, getErr)
	}
	var records []struct {
		Kind            string
		ExecutionEvents []struct{ Kind, Text string }
		Transcript      []llm.Message
	}
	if err := json.Unmarshal([]byte(actual.AttemptLog), &records); err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Kind != "execution" {
			continue
		}
		calls, snapshots := 0, 0
		for _, event := range record.ExecutionEvents {
			if event.Kind == "tool" && strings.Contains(event.Text, "read") {
				calls++
			}
			if event.Kind == "continuation" {
				snapshots++
			}
		}
		if calls != 2 || snapshots == 0 || len(record.Transcript) == 0 {
			t.Fatalf("runOne discarded durable worker calls/continuations or final transcript: %+v", record)
		}
		return
	}
	t.Fatal(fmt.Sprintf("missing execution record: %s", actual.AttemptLog))
}
