package launch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// Models/judge are labelled component doubles; store, Bash, validation and receipts are real.
func TestNativeTraceRequiredCommandAdmission(t *testing.T) {
	for _, surface := range []string{"ready", "launch", "resume"} {
		t.Run(surface, func(t *testing.T) {
			s, aid, ids := setup(t, 1)
			d := depsFor(s, &echoModel{})
			d.Wf.VerifyFollowup = true
			if e := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.check_command=''`, map[string]any{"id": ids[0]}); e != nil {
				t.Fatal(e)
			}
			judges, workers := 0, 0
			d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
				judges++
				return decision.Verdict{Option: servesGoal, Confidence: 1}, nil
			}
			d.NewModel = func() agent.Model { workers++; return &echoModel{} }
			if surface != "launch" {
				for _, state := range []string{plan.ApproachRunning, plan.ApproachPaused} {
					if _, e := fixtureApproachTransition(s, aid, state); e != nil {
						t.Fatal(e)
					}
				}
			}
			before, _ := plan.GetApproach(s, aid)
			var err error
			switch surface {
			case "ready":
				_, err = Ready(t.Context(), d, aid)
			case "launch":
				_, err = Launch(t.Context(), d, aid)
			case "resume":
				_, err = Resume(t.Context(), d, aid)
			}
			task, _ := plan.GetTask(s, ids[0])
			after, _ := plan.GetApproach(s, aid)
			if err == nil || !strings.Contains(err.Error(), "check_command") || !strings.Contains(err.Error(), "graphql mutation setTaskBlock") || !strings.Contains(err.Error(), "workflow_block when advertised") || judges != 0 || workers != 0 || task.Attempts != 0 || after.State != before.State {
				t.Fatalf("blank required command admitted: err=%v judges=%d workers=%d task=%+v before=%s after=%s", err, judges, workers, task, before.State, after.State)
			}
			// Explicit legal block repair, never command inference from prose.
			if surface == "launch" {
				for _, state := range []string{plan.ApproachRunning, plan.ApproachPaused} {
					if _, e := fixtureApproachTransition(s, aid, state); e != nil {
						t.Fatal(e)
					}
				}
			}
			_, err = plan.SetTaskBlock(s, task.ID, task.Current, task.Target, task.Sketch, task.Acceptance, task.Tests, task.Verify, "printf DECLARED_CHECK")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Ready(t.Context(), d, aid); err != nil || judges != 1 {
				t.Fatalf("explicit repair refused: %v judges=%d", err, judges)
			}
		})
	}
}

type traceSession struct{ dir string }

func (s traceSession) Append(_ context.Context, id, kind, text string) error {
	return session.Append(s.dir, id, session.Event{Kind: kind, Text: text})
}
func TestNativeTraceCorrectedOwningReference(t *testing.T) {
	s, aid, ids := setup(t, 1)
	dir := t.TempDir()
	d := depsFor(s, nil)
	d.Workspace = bindFixtureProject(t, s, aid, dir)
	d.Wf.VerifyFollowup = true
	d.NewModel = func() agent.Model { return &owningScript{} }
	d.NewRegistry = func() *tool.Registry {
		r := tool.NewRegistry()
		r.Register(bash.NewAt(config.Defaults().Tools.Bash, dir))
		return r
	}
	command := "test \"$(cat artifact.txt)\" = artifact"
	declareFixtureCheck(t, s, aid, ids, command)
	result, err := New(d).Execute(t.Context(), tool.Args{"approach": aid})
	if err != nil || result.Workflow == nil || !result.Workflow.Qualified {
		t.Fatalf("fixture did not actually qualify: %+v %v", result, err)
	}
	p, _ := plan.ApproachPlan(s, aid)
	reg := tool.NewRegistry()
	reg.Register(New(d))
	for _, def := range NewBuild(d) {
		reg.Register(def)
	}
	parent := &agent.Runner{Registry: reg, MaxSteps: 4, CodingRequired: true, Workspace: dir, CheckCommand: command, SessionID: "trace", Session: traceSession{filepath.Join(dir, "sessions")}}
	task, _ := plan.GetTask(s, ids[0])
	parent.Continuation.Coding = &session.CodingState{Attempt: 1, TaskID: task.ID, Workspace: dir, Command: command, AcceptanceCommand: task.AcceptanceCommand}
	reg.Register(parent.CheckDefinition(bash.NewAt(config.Defaults().Tools.Bash, dir), task.AcceptanceCommand))
	parent.Exec = parent.CodingExec(agent.Direct)
	parent.Model = &loopModel{replies: []string{"```tool\ntool: launch\napproach: " + p.ID + "\n```", "```tool\ntool: workflow_status\napproach: " + aid + "\n```", "```tool\ntool: check\ncmd: " + command + "\n```", "Observed checks passed."}}
	_, _, err = parent.Run(t.Context(), "owning fixture parent")
	if err != nil || parent.Outcome.Status != outcome.Completed || !parent.Continuation.Coding.Passed() {
		t.Fatalf("corrected real ref retains ghost: %+v err=%v", parent.Outcome, err)
	}
	events, err := session.Read(filepath.Join(dir, "sessions"), "trace")
	if err != nil {
		t.Fatal(err)
	}
	actual := false
	for _, event := range events {
		if event.Kind == "coding" && strings.Contains(event.Text, "\"exit_code\":0") {
			actual = true
		}
	}
	if !actual {
		t.Fatal("missing actual durable ordinary check")
	}
}

func TestNativeTraceChildFacts(t *testing.T) {
	s, aid, ids := setup(t, 1)
	dir := t.TempDir()
	command := "test \"$(cat artifact.txt)\" = artifact"
	declareFixtureCheck(t, s, aid, ids, command)
	d := depsFor(s, nil)
	d.Workspace = bindFixtureProject(t, s, aid, dir)
	d.Wf.VerifyFollowup = true
	d.Behavior = "Include TRACE_BEHAVIOR in the final report."
	var models []*loopModel
	d.NewModel = func() agent.Model {
		m := &loopModel{replies: []string{"```tool\ntool: bash\ncmd: printf artifact > artifact.txt\n```", "artifact created"}}
		if len(models) == 2 {
			m.replies = []string{"PASS: actual declared fixture check"}
		}
		models = append(models, m)
		return m
	}
	d.NewRegistry = func() *tool.Registry {
		r := tool.NewRegistry()
		r.Register(bash.NewAt(config.Defaults().Tools.Bash, dir))
		return r
	}
	if _, err := New(d).Execute(t.Context(), tool.Args{"approach": aid}); err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 || len(models[1].seen) != 0 {
		t.Fatalf("missing worker/reviewer: %d", len(models))
	}
	for i, m := range []*loopModel{models[0], models[2]} {
		tx := m.seen[0]
		joined := ""
		for _, msg := range tx {
			joined += msg.Content + "\n"
		}
		if !strings.Contains(joined, "workspace: "+d.Workspace) || !strings.Contains(joined, "check_command: "+command) || strings.Count(joined, "TRACE_BEHAVIOR") != 1 {
			t.Errorf("role%d lost admitted facts: %s", i, joined)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "artifact.txt"))
	if err != nil || string(b) != "artifact" {
		t.Fatalf("actual artifact: %q %v", b, err)
	}
}

func TestNativeTraceBlockMissingCommandNoEffects(t *testing.T) {
	s, aid, ids := setup(t, 1)
	for _, state := range []string{plan.ApproachRunning, plan.ApproachPaused} {
		if _, e := fixtureApproachTransition(s, aid, state); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.check_command=''`, map[string]any{"id": ids[0]}); e != nil {
		t.Fatal(e)
	}
	d := depsFor(s, nil)
	d.Wf.VerifyFollowup = true
	var def tool.Definition
	for _, candidate := range NewBuild(d) {
		if candidate.Name == "workflow_block" {
			def = candidate
		}
	}
	slots := map[string]string{"task": ids[0], "current": "new fact", "target": "artifact", "sketch": "write artifact", "acceptance": "check exits0", "tests": "run check", "verify": "independent check"}
	args, q := tool.Validate(def, slots)
	if q != "" {
		t.Fatal(q)
	}
	_, err := def.Execute(t.Context(), args)
	after, _ := plan.GetTask(s, ids[0])
	if err == nil || !strings.Contains(err.Error(), "check_command") || after.Current != "now" {
		t.Fatalf("missing declaration published block: %+v %v", after, err)
	}
	slots["check_command"] = "printf EXACT_CHECK"
	args, q = tool.Validate(def, slots)
	if q != "" {
		t.Fatal(q)
	}
	if _, e := def.Execute(t.Context(), args); e != nil {
		t.Fatal(e)
	}
	delete(slots, "check_command")
	slots["current"] = "later fact"
	args, q = tool.Validate(def, slots)
	if q != "" {
		t.Fatal(q)
	}
	if _, e := def.Execute(t.Context(), args); e != nil {
		t.Fatal(e)
	}
	after, _ = plan.GetTask(s, ids[0])
	if after.CheckCommand != "printf EXACT_CHECK" || after.AcceptanceCommand != "printf FIXTURE_ACCEPTANCE" || after.Current != "later fact" {
		t.Fatalf("omission lost existing command: %+v", after)
	}
}
