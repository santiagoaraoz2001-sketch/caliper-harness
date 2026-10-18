package run

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/launch"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

// Explicit PAT controls select a real owning scope; ordinary coding fixtures stay unscoped.
func selectedFixtureScope(t *testing.T, s *store.Store, ref string) *session.WorkflowScope {
	t.Helper()
	scope, err := launch.SelectScope(s, ref)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestAssignedDraftEffectRequiresClaim(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Defaults()
	cfg.Model.Runtime = "openai"
	cfg.Model.ToolProtocol = "structured"
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	srv := codingFixture(t, "structured", []codingCall{{"bash", map[string]any{"cmd": "printf forbidden > marker"}}})
	cfg.Model.Server = srv.URL
	bindDecisionFixture(t, &cfg, nil)
	s, e := store.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r, _, e := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, "root")}, Store: s, Cfg: cfg, Server: srv.URL, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "assigned", Model: "fixture", MaxSteps: 3, Window: 32768}, "investigate and repair")
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"read", "find_file", "find_dir", "grep", "help", "plan", "graphql"} {
		if _, ok := r.Registry.Lookup(name); !ok {
			t.Fatalf("draft discovery/bookkeeping missing %s", name)
		}
	}
	r.Run(context.Background(), "investigate and repair")
	if marker, err := os.ReadFile("marker"); !os.IsNotExist(err) {
		t.Fatalf("draft project effect executed without claim: %q %v", marker, err)
	}
	if r.Continuation.Assignment != nil || r.Continuation.Coding != nil && r.Continuation.Coding.Attempt != 0 {
		t.Fatal("ordinary draft effect acquired Task claim authority")
	}
}

// Model/judgment doubles only; owning Kuzu, file/Bash/check/session operations are real.
func bindDecisionFixture(t *testing.T, cfg *config.Config, observe func(decision.Request) int, confidence ...float64) {
	t.Helper()
	cfg.Decision.Model, cfg.Decision.Runtime, cfg.Decision.Device = "component", "laya", "cpu"
	selection := decision.SelectionFor(cfg.Decision)
	conf := 1.0
	if len(confidence) > 0 {
		conf = confidence[0]
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "pid": 1, "loaded": true, "ready": true, "loading": false, "model": "component", "active": decision.Identity{Model: "component", Runtime: "laya", Source: "component", Path: "/component", Device: "cpu", NativeDevice: "cpu", Architecture: "component", Parameters: 1, Selection: selection}, "backends": []string{"cpu"}})
		case "/judge":
			var request decision.Request
			if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
				t.Error(e)
				return
			}
			pick := 0
			if observe != nil {
				pick = observe(request)
			}
			json.NewEncoder(w).Encode(map[string]any{"pick": pick, "option": request.Options[pick], "confidence": conf, "model": "component", "device": "cpu"})
		default:
			t.Errorf("unbound decision route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	cfg.Decision.Server = srv.URL
	t.Cleanup(srv.Close)
}
func assignedFixtureTask(t *testing.T, s *store.Store, command string, behavior ...string) *plan.Task {
	t.Helper()
	root, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	ctx := plan.WithProject(context.Background(), root)
	p, e := plan.CreatePlanContext(ctx, s, "owning fixture", "perform actual declared operation")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = plan.TransitionPlan(s, p.ID, plan.PlanActive); e != nil {
		t.Fatal(e)
	}
	a, e := plan.CreateApproach(s, p.ID, "one strategy")
	if e != nil {
		t.Fatal(e)
	}
	task, e := plan.CreateTask(s, a.ID, "actual operation", "perform requested operation")
	if e != nil {
		t.Fatal(e)
	}
	acceptance := command
	if len(behavior) == 1 {
		acceptance = behavior[0]
	}
	task, e = plan.SetTaskBlock(s, task.ID, "initial", "declared operation complete", "use registered tools", "actual check exit0", "declared command", "independently inspect actual check and return PASS or FAIL", command, acceptance)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = plan.Ready(ctx, s, a.ID, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); e != nil {
		t.Fatal(e)
	}
	return task
}

type settlementEvidenceSink struct {
	*codingSink
	reject bool
}

func (s *settlementEvidenceSink) Append(ctx context.Context, id, kind, text string) error {
	if s.reject && kind == "evidence" && strings.HasPrefix(text, "Assigned task settlement:") {
		return fmt.Errorf("settlement evidence record failure")
	}
	return s.codingSink.Append(ctx, id, kind, text)
}
func TestAssignedMainClaimsChecksReviewsAndRestores(t *testing.T) {
	for _, mode := range []string{"ordinary", "pendingFinal", "repairFinal", "recordFailure"} {
		t.Run(mode, func(t *testing.T) {
			pendingFinal := mode == "pendingFinal"
			finalJudgments := 0
			dir := t.TempDir()
			t.Chdir(dir)
			s, e := store.Open(filepath.Join(dir, "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			cfg := config.Defaults()
			cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Recall.TopN = 0
			cfg.Templates.Path = assignedTemplates()
			cfg.Coding.CheckCommand = `test "$(cat marker)" = α`
			task := assignedFixtureTask(t, s, cfg.Coding.CheckCommand)
			if _, e = memory.PutKnowledge(context.Background(), s, dir, "prior-author", "old/unrelated", strings.Repeat("z", 1000000), "", "", ""); e != nil {
				t.Fatal(e)
			}
			var contexts []string
			bindDecisionFixture(t, &cfg, func(q decision.Request) int {
				contexts = append(contexts, q.Context)
				var input struct{ NormalFinal bool }
				_ = json.Unmarshal([]byte(q.Context), &input)
				if input.NormalFinal {
					finalJudgments++
				}
				if input.NormalFinal && (pendingFinal || mode == "repairFinal" && finalJudgments == 1) {
					return 1
				}
				return 0
			})
			srv := codingFixture(t, "structured", []codingCall{{"task_start", map[string]any{"task": task.ID}}, {"bash", map[string]any{"cmd": "printf α > marker"}}, {"check", map[string]any{"timeout": 2}}})
			cfg.Model.Server = srv.URL
			sink := &settlementEvidenceSink{codingSink: &codingSink{dir: filepath.Join(dir, "sessions")}, reject: mode == "recordFailure"}
			scope, e := launch.SelectScope(s, task.ID)
			if e != nil {
				t.Fatal(e)
			}
			r, _, e := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: scope}, Cfg: cfg, Store: s, Session: sink, SessionID: "main", Server: srv.URL, Model: "fixture", Window: 32768, MaxSteps: 8}, "perform actual operation")
			if e != nil {
				t.Fatal(e)
			}
			if pendingFinal {
				r.MaxSteps = 4
			}
			_, _, e = r.Run(context.Background(), "perform actual operation")
			want := outcome.Completed
			if pendingFinal {
				want = outcome.Stopped
				if !strings.Contains(r.Outcome.Reason, "bookkeeping repair pending; step budget exhausted") {
					t.Fatal(r.Outcome)
				}
			}
			if mode == "recordFailure" {
				want = outcome.Failed
				if e == nil || !strings.Contains(e.Error(), "settlement evidence record failure") {
					t.Fatalf("record failure not loud: %+v %v", r.Outcome, e)
				}
			} else if e != nil {
				t.Fatal(e)
			}
			if r.Outcome.Status != want {
				t.Fatalf("main %+v %v", r.Outcome, e)
			}
			actual, e := plan.GetTask(s, task.ID)
			if e != nil || !actual.Qualified() || actual.Attempts != 1 || actual.Result != "PASS complete" {
				t.Fatalf("actual %+v %v", actual, e)
			}
			coding := r.Continuation.Coding
			if coding.AcceptanceCommand != task.AcceptanceCommand || coding.Receipt == nil || coding.Receipt.Acceptance == nil || coding.Receipt.Acceptance.Command != task.AcceptanceCommand || coding.Receipt.Acceptance.ExitCode == nil || *coding.Receipt.Acceptance.ExitCode != 0 {
				t.Fatalf("frozen main behavioral acceptance was not executed: %+v", coding)
			}
			receipt := fmt.Sprintf("Assigned task settlement: task=%s attempt=1 qualified=true", task.ID)
			receiptCount := 0
			for _, entry := range r.Continuation.Entries {
				if strings.HasPrefix(entry.Message.Content, receipt) {
					if entry.Kind != "evidence" || entry.Message.Role != "user" {
						t.Fatal("settlement is not protected factual evidence")
					}
					receiptCount++
				}
			}
			if mode == "recordFailure" {
				if receiptCount != 0 {
					t.Fatal("failed durable receipt was added to continuation")
				}
			} else if receiptCount != 1 {
				t.Fatalf("actual qualification missing/duplicated: %d", receiptCount)
			}
			if mode == "repairFinal" && finalJudgments < 2 {
				t.Fatal("repairFinal reentry not exercised")
			}
			for _, c := range contexts {
				if strings.Contains(c, "old/unrelated") || strings.Contains(c, strings.Repeat("z", 100)) {
					t.Fatal("irrelevant knowledge entered selected facts")
				}
			}
			events, e := session.Read(sink.dir, "main")
			if e != nil {
				t.Fatal(e)
			}
			pairs, durableReceipts := 0, 0
			for _, event := range events {
				if event.Kind == "evidence" && strings.HasPrefix(event.Text, receipt) {
					durableReceipts++
				}
				if event.Kind == "continuation" {
					var snapshot session.Continuation
					if err := json.Unmarshal([]byte(event.Text), &snapshot); err != nil {
						t.Fatal(err)
					}
					if err := snapshot.Validate(); err != nil {
						t.Fatal(err)
					}
					if snapshot.Assignment != nil && snapshot.Coding == nil {
						t.Fatal("claim lost coding provenance")
					}
				}
				if event.Kind == "native_result" {
					pairs++
				}
			}
			if pairs != 3 {
				t.Fatalf("actual claim/Bash/check associations %d", pairs)
			}
			if durableReceipts != receiptCount {
				t.Fatalf("durable=%d snapshot=%d", durableReceipts, receiptCount)
			}
			if mode == "recordFailure" {
				return
			} // owner remains qualified; the outer run failed only its durable record.
			restored, _, e := session.Restore(events, 32768)
			if e != nil || restored.Assignment == nil || restored.Assignment.TaskID != task.ID || restored.Coding.Passed() {
				t.Fatalf("restored %+v %v", restored, e)
			}
			restoredReceipts := 0
			for i, entry := range restored.Entries {
				if strings.HasPrefix(entry.Message.Content, receipt) {
					restoredReceipts++
					if !restored.Protected(3)[i] {
						t.Fatal("receipt not protected on restore")
					}
				}
			}
			if restoredReceipts != 1 {
				t.Fatalf("restored receipts=%d", restoredReceipts)
			}
			summarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body struct {
					Messages []llm.Message
					Tools    []llm.ToolDefinition
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body.Tools) != 0 || len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Role != "user" {
					t.Error("unexpected summary roles/tools")
				} else {
					input := body.Messages[1].Content
					if strings.Count(input, receipt) != 1 || !strings.Contains(input, "kind=evidence role=user recorded observation reference-only=true") {
						t.Error("actual qualification not supplied once as factual reference")
					}
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"summary\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer summarySrv.Close()
			compactRunner := *r
			compactRunner.Model = llm.New(summarySrv.URL, "fixture").WithToolProtocol("structured")
			compactRunner.Compaction.Keep = 0
			compactInput := restored.Clone()
			compactInput.Add("reply", "assistant", "previous unprotected response", false)
			compacted, _, ran, err := compactRunner.Compact(context.Background(), compactInput)
			if err != nil || !ran {
				t.Fatalf("actual settlement compact: %t %v", ran, err)
			}
			compactReceiptCount := 0
			for _, entry := range compacted.Entries {
				if strings.HasPrefix(entry.Message.Content, receipt) {
					compactReceiptCount++
				}
			}
			if compactReceiptCount != 1 {
				t.Fatal("compaction did not retain exact owning receipt")
			}
			// Restored provenance is explanatory: fresh factory still cannot perform an effect.
			next, _, e := New(Deps{Cfg: cfg, Store: s, Session: sink, SessionID: "reopen", Server: srv.URL, Model: "fixture", Window: 32768, MaxSteps: 1, Continuation: restored}, "inspect")
			if e != nil {
				t.Fatal(e)
			}
			refused := codingFixture(t, "structured", []codingCall{{"bash", map[string]any{"cmd": "touch forbidden-reopen"}}})
			next.Model = llm.New(refused.URL, "fixture").WithRuntime("openai").WithToolProtocol("structured")
			_, _, _ = next.Run(context.Background(), "inspect")
			if _, e := os.Stat("forbidden-reopen"); !os.IsNotExist(e) {
				t.Fatal("restored provenance authorized a fresh effect")
			}
		})
	}
}

// Calibrate former ordinary coding controls with a real owning PAT and a model-emitted claim.
// The model/decision fixtures are doubles; claim, readiness, store, effects, check and review remain real.
type claimFixtureModel struct {
	agent.Model
	task     string
	pending  bool
	protocol string
}

func (m *claimFixtureModel) ToolProtocol() string { return m.protocol }
func (m *claimFixtureModel) Chat(ctx context.Context, messages []llm.Message) (string, string, llm.Usage, error) {
	if m.pending {
		m.pending = false
		return "```tool\ntool: task_start\ntask: " + m.task + "\n```", "fixture", llm.Usage{}, nil
	}
	return m.Model.Chat(ctx, messages)
}
func (m *claimFixtureModel) ChatTools(ctx context.Context, messages []llm.Message, defs []llm.ToolDefinition, delta func(string)) (llm.Reply, error) {
	if m.pending {
		m.pending = false
		if m.protocol == "text" {
			return llm.Reply{Message: llm.Message{Role: "assistant", Content: "```tool\ntool: task_start\ntask: " + m.task + "\n```"}}, nil
		}
		raw, _ := json.Marshal(map[string]string{"task": m.task})
		return llm.Reply{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "fixture-owning-claim", Name: "task_start", Arguments: string(raw)}}}}, nil
	}
	return m.Model.(agent.ToolModel).ChatTools(ctx, messages, defs, delta)
}
func assignedCodingNew(t *testing.T, d Deps, prompt string, command ...string) (*agent.Runner, []string, error) {
	t.Helper()
	bindDecisionFixture(t, &d.Cfg, nil)
	d.Cfg.Templates.Path = assignedTemplates()
	declared := d.Cfg.Coding.CheckCommand
	if len(command) > 0 {
		declared = command[0]
	}
	if declared == "" {
		declared = "true"
	}
	// This adapter tests coding/control receipts; cwd existence is a mechanical
	// acceptance predicate, not product correctness evidence.
	behavior := "test -d ."
	if len(command) > 1 {
		behavior = command[1]
	}
	task := assignedFixtureTask(t, d.Store, declared, behavior)
	scope, err := launch.SelectScope(d.Store, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Continuation.Workflow = scope
	d.Tools = "workflow"
	d.MaxSteps++ // one separately charged actual claim, retaining the original coding budget.
	ask := d.AskYesNo
	d.AskYesNo = func(ctx context.Context, prompt string) bool {
		_, raw, _ := strings.Cut(prompt, "Complete validated action (JSON):\n")
		var action struct{ Tool string }
		_ = json.Unmarshal([]byte(raw), &action)
		if action.Tool == "task_start" {
			return true
		}
		return ask != nil && ask(ctx, prompt)
	}
	r, notes, e := New(d, prompt)
	if e == nil {
		r.Model = &claimFixtureModel{Model: r.Model, task: task.ID, pending: true, protocol: d.Cfg.Model.ToolProtocol}
	}
	return r, notes, e
}

func TestAssignedPreclaimSemanticFactsAndSelectedKnowledge(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if e := os.WriteFile("source", []byte("observed α"), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := store.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	cfg.Model.Runtime = "openai"
	cfg.Model.ToolProtocol = "structured"
	var contexts []string
	bindDecisionFixture(t, &cfg, func(q decision.Request) int { contexts = append(contexts, q.Context); return 0 })
	srv := codingFixture(t, "structured", []codingCall{{"read", map[string]any{"path": "source"}}, {"graphql", map[string]any{"query": `mutation {createPlan(title:"actual unfinished",goal:"observed semantic goal"){id goal}}`}}, {"graphql", map[string]any{"query": `{__schema {types {name}}}`}}, {"graphql", map[string]any{"query": `mutation {putKnowledge(key:"working/fact",body:"supported α",filePath:"source",expectedPreviousId:""){id}}`}}})
	cfg.Model.Server = srv.URL
	r, _, e := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, "root")}, Cfg: cfg, Store: s, Server: srv.URL, Model: "fixture", Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "facts", Window: 32768, MaxSteps: 8}, "investigate")
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = r.Run(context.Background(), "investigate")
	if e != nil || r.Outcome.Status != outcome.Stopped {
		t.Fatalf("preclaim %+v %v", r.Outcome, e)
	}
	var semantic, selected, discovery, graphDigest bool
	for _, raw := range contexts {
		semantic = semantic || strings.Contains(raw, "observed semantic goal")
		selected = selected || strings.Contains(raw, "supported α")
		discovery = discovery || strings.Contains(raw, fmt.Sprintf("%x", sha256.Sum256([]byte("observed α"))))
		if strings.Contains(raw, "observed α") {
			t.Fatal("source body entered compact bookkeeping decision")
		}
		graphDigest = graphDigest || strings.Contains(raw, "operation_sha256")
		var input struct {
			Latest struct {
				Tool   string
				Args   map[string]any
				Result struct{ Text string }
			}
		}
		_ = json.Unmarshal([]byte(raw), &input)
		if input.Latest.Tool == "graphql" && (input.Latest.Args["query"] != nil || strings.Contains(input.Latest.Result.Text, "__schema") || strings.Contains(input.Latest.Result.Text, "__Directive")) {
			t.Fatal("raw schema/query dumped into decision context")
		}
	}
	if !semantic || !selected || !discovery || !graphDigest {
		t.Fatalf("facts semantic%t selected%t discovery%t digest%t", semantic, selected, discovery, graphDigest)
	}
}

func TestAssignedStaleOwnerRefusesBeforeGateAndCheckInvalidation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, e := store.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode = config.AuthAll
	cfg.Recall.TopN = 0
	cfg.Model.Runtime = "openai"
	cfg.Model.ToolProtocol = "structured"
	cfg.Coding.CheckCommand = "true"
	task := assignedFixtureTask(t, s, "true")
	bindDecisionFixture(t, &cfg, nil)
	approvals := 0
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		requests++
		var call codingCall
		if requests == 1 {
			call = codingCall{"task_start", map[string]any{"task": task.ID}}
		} else {
			a, _ := plan.TaskApproach(s, task.ID)
			p, _ := plan.ApproachPlan(s, a.ID)
			_ = p
			if _, e := plan.TransitionApproach(s, a.ID, plan.ApproachPaused); e != nil {
				t.Error(e)
			}
			call = codingCall{"bash", map[string]any{"cmd": "touch forbidden", "timeout": 2}}
		}
		raw, _ := json.Marshal(call.args)
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("stale", requests), "type": "function", "function": map[string]string{"name": call.name, "arguments": string(raw)}}}}, "finish_reason": "tool_calls"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	}))
	defer srv.Close()
	cfg.Model.Server = srv.URL
	scope, e := launch.SelectScope(s, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	r, _, e := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: scope}, Cfg: cfg, Store: s, Server: srv.URL, Model: "fixture", Window: 32768, MaxSteps: 2, SessionID: "stale", Session: &codingSink{dir: filepath.Join(dir, "sessions")}, AskYesNo: func(context.Context, string) bool { approvals++; return true }}, "actual stale ownership")
	if e != nil {
		t.Fatal(e)
	}
	_, _, _ = r.Run(context.Background(), "actual stale ownership")
	if approvals != 1 || r.Continuation.Coding.Revision != 0 {
		t.Fatalf("stale admission crossed gate/coding invalidation approvals%d coding%+v", approvals, r.Continuation.Coding)
	}
	if _, e := os.Stat("forbidden"); !os.IsNotExist(e) {
		t.Fatal("stale owner published effect")
	}
	if r.Outcome.Status == outcome.Completed {
		t.Fatal("stale owner qualified")
	}
}

func assignedTemplates() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "templates")
}

// Real discovery/tool/session continuation; only model and decision replies are doubles.
func TestAssignedUncertainGuidanceContinuesWithoutAsk(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pick       int
		confidence float64
		guidance   bool
	}{
		{"both failed", 3, .3, false}, {"alert failed", 4, .499, false}, {"coherent failed", 0, .3, false},
		{"both at floor", 3, .5, true}, {"alert above floor", 4, .6, true}, {"coherent confident", 0, .99, false}, {"high alert", 4, .9, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if err := os.WriteFile("source.go", []byte("package source\n// exact observed source α\n"), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := store.Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			cfg := config.Defaults()
			cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
			cfg.Auth.Mode, cfg.Recall.TopN = config.AuthYOLO, 0
			cfg.Templates.Path = assignedTemplates()
			bindDecisionFixture(t, &cfg, func(q decision.Request) int {
				var input struct{ NormalFinal bool }
				_ = json.Unmarshal([]byte(q.Context), &input)
				if input.NormalFinal {
					return 0
				}
				return tc.pick
			}, tc.confidence)
			var wires [][]llm.Message
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/chat/completions" {
					t.Error(request.URL.Path)
					http.NotFound(w, request)
					return
				}
				var body struct {
					Messages []llm.Message
					Stream   bool
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if !body.Stream {
					t.Error("actual tool request not streamed")
				}
				wires = append(wires, body.Messages)
				delta := map[string]any{"content": "discovery complete"}
				finish := "stop"
				calls := []codingCall{{"find_file", map[string]any{"pattern": "source.go"}}, {"read", map[string]any{"path": "source.go"}}}
				if n := len(wires) - 1; n < len(calls) {
					raw, _ := json.Marshal(calls[n].args)
					delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("observed", n), "type": "function", "function": map[string]string{"name": calls[n].name, "arguments": string(raw)}}}}
					finish = "tool_calls"
				}
				chunk, _ := json.Marshal(map[string]any{"model": "fixture", "choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 10}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer model.Close()
			cfg.Model.Server = model.URL
			sink := &codingSink{dir: filepath.Join(dir, "sessions")}
			r, _, err := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, "root")}, Cfg: cfg, Store: s, Session: sink, SessionID: "guidance", Server: model.URL, Model: "fixture", Window: 32768, MaxSteps: 3}, "investigate the actual source")
			if err != nil {
				t.Fatal(err)
			}
			asked := 0
			r.Ask = func(context.Context, string) bool { asked++; return false }
			_, _, err = r.Run(context.Background(), "investigate the actual source")
			if err != nil || asked != 0 || r.Outcome.Status != outcome.Stopped {
				t.Fatalf("actual outcome=%+v asks=%d error=%v", r.Outcome, asked, err)
			}
			for _, wire := range wires {
				bases, guides, intents := 0, 0, 0
				for _, message := range wire {
					if message.Role == "system" && strings.HasPrefix(message.Content, "You are a professional software engineer") {
						bases++
					}
					if message.Role == "system" && strings.Contains(message.Content, " Workspace: "+r.Workspace) {
						guides++
					}
					if message.Role == "user" && message.Content == "investigate the actual source" {
						intents++
					}
				}
				if bases != 1 || guides != 1 || intents != 1 {
					t.Fatalf("actual wire duplicate/missing base=%d guide=%d intent=%d", bases, guides, intents)
				}
			}
			events, err := session.Read(sink.dir, "guidance")
			if err != nil {
				t.Fatal(err)
			}
			results, logged := 0, false
			for _, event := range events {
				if event.Kind == "native_result" {
					results++
				}
				if event.Kind == "judgment" && strings.Contains(event.Text, "pat-product-enforcer") && strings.Contains(event.Text, "resolution=failed-confidence") {
					logged = true
				}
			}
			if len(wires) != 3 || results != 2 || logged != (tc.confidence < .5) || r.Outcome.Status != outcome.Stopped || r.Continuation.Coding.Passed() {
				t.Fatalf("requests=%d actualresults=%d durableguidance=%v outcome=%+v", len(wires), results, logged, r.Outcome)
			}
			observedSource := false
			for _, message := range wires[2] {
				observedSource = observedSource || message.Role == "tool" && message.ToolCallID == "observed1" && strings.Contains(message.Content, "// exact observed source α")
			}
			if !observedSource {
				t.Fatal("supervision lost the actual read result from the next model request")
			}
			guidance, preparation := false, false
			for _, message := range wires[1] {
				if message.Role == "user" && strings.Contains(message.Content, r.Prompts.Text("continuation.bookkeeping.request_prefix")) {
					guidance = true
					current := strings.HasPrefix(message.Content, r.Prompts.Text("continuation.preparation.current_frame"))
					preparation = preparation || current
					if tc.pick == 3 && current && (!strings.Contains(message.Content, strings.TrimSpace(r.Prompts.Text("continuation.preparation.next_action"))) || !strings.Contains(message.Content, "tool: plan")) {
						t.Fatalf("actual next wire lacks root preparation: %q", message.Content)
					}
					if tc.pick == 3 && current {
						for _, field := range []string{"title", "outcome", "rules", "articles", "proof"} {
							if !strings.Contains(message.Content, field) {
								t.Fatalf("next wire omitted required authored field %s: %q", field, message.Content)
							}
						}
					}
					if tc.pick == 4 && strings.Contains(message.Content, "alert and stop") {
						t.Fatal("advisory alert commands stop")
					}
				}
			}
			if guidance != tc.guidance {
				t.Fatalf("next actual wire guidance=%v want=%v", guidance, tc.guidance)
			}
			if tc.pick == 3 && tc.guidance && !preparation {
				t.Fatal("combined request lost current preparation advice")
			}
			if tc.pick == 4 && tc.guidance {
				found := false
				for _, message := range wires[1] {
					found = found || strings.Contains(message.Content, "request focused correction of conflicting PAT or product knowledge")
				}
				if !found {
					t.Fatal("actual next request lost successful repair direction")
				}
			}
		})
	}
}

// Successful reads can leave an actual bookkeeping request unresolved; loop input must describe progress, not deny success.
func TestAssignedSuccessfulReadStallTruthful(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("source", []byte("observed source α\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode, cfg.Recall.TopN = config.AuthYOLO, 0
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Agent.LoopRepeat, cfg.Agent.LoopStall = 0, 1
	var trip string
	bindDecisionFixture(t, &cfg, func(q decision.Request) int {
		if len(q.Options) == 3 && strings.Contains(q.Context, "cadence review") {
			trip = q.Context
			return 0
		}
		return 1 // concrete missing PAT; keep the actual request pending
	})
	model := codingFixture(t, "structured", []codingCall{{"read", map[string]any{"path": "source"}}, {"read", map[string]any{"path": "source"}}, {"read", map[string]any{"path": "source"}}})
	cfg.Model.Server = model.URL
	sink := &codingSink{dir: filepath.Join(dir, "sessions")}
	runner, _, err := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, "root")}, Cfg: cfg, Store: s, Server: model.URL, Model: "fixture", Session: sink, SessionID: "stall", Window: 32768, MaxSteps: 4}, "investigate")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runner.Run(context.Background(), "investigate")
	if err != nil || runner.Outcome.Status != outcome.Stopped {
		t.Fatalf("outcome=%+v error=%v", runner.Outcome, err)
	}
	events, err := session.Read(sink.dir, "stall")
	if err != nil {
		t.Fatal(err)
	}
	successful := 0
	for _, event := range events {
		if event.Kind == "native_result" {
			var entry session.Entry
			if err := json.Unmarshal([]byte(event.Text), &entry); err != nil {
				t.Fatal(err)
			}
			if entry.Kind == "output" && strings.Contains(entry.Message.Content, "observed source α") {
				successful++
			}
		}
	}
	if successful < 2 {
		t.Fatalf("successful real reads=%d", successful)
	}
	t.Logf("successful registered reads=%d actual loop input=%q", successful, trip)
	var evidence struct {
		Latest struct {
			Tool                                string
			Args                                map[string]any
			Failed, Blocked, Changed, Truncated bool
			OutputBytes                         int
			OutputSHA256, Error                 string
		}
		PendingRepair, ProgressBasis string
	}
	if json.Unmarshal([]byte(trip), &evidence) != nil || evidence.Latest.Tool != "read" || evidence.Latest.Args["path"] != "source" || evidence.Latest.OutputBytes == 0 || len(evidence.Latest.OutputSHA256) != 64 || evidence.Latest.Failed || evidence.Latest.Blocked || evidence.PendingRepair == "" || evidence.ProgressBasis == "" {
		t.Fatalf("missing actual loop observation: %q", trip)
	}
	if strings.Contains(trip, "no observed progress") || strings.Contains(trip, "no successful result") || !strings.Contains(trip, "cadence review") {
		t.Fatalf("misleading loop input after successful reads: %q", trip)
	}
}

// Actual current owning facts must distinguish an empty root from an explicitly selected node.
func TestAssignedPreparationAdvice(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	bindDecisionFixture(t, &cfg, nil)
	model := codingFixture(t, "structured", nil)
	cfg.Model.Server = model.URL
	makeRunner := func(scope *session.WorkflowScope) *agent.Runner {
		if scope == nil {
			scope = selectedFixtureScope(t, s, "root")
		}
		rr, _, e := New(Deps{Tools: "workflow", Cfg: cfg, Store: s, Server: model.URL, Model: "fixture", Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "advice", Window: 32768, MaxSteps: 4, Continuation: session.Continuation{Workflow: scope}}, "investigate")
		if e != nil {
			t.Fatal(e)
		}
		return rr
	}
	advice := func(rr *agent.Runner) string {
		facts, e := rr.Assigned.Facts(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		raw, e := json.Marshal(facts)
		if e != nil {
			t.Fatal(e)
		}
		var v struct{ Preparation string }
		if e = json.Unmarshal(raw, &v); e != nil {
			t.Fatal(e)
		}
		return v.Preparation
	}
	rr := makeRunner(nil)
	if _, available := rr.Registry.Lookup("prepare_task"); !available {
		t.Fatal("actual preparation owner missing")
	}
	if v := advice(rr); !strings.Contains(v, "tool: plan") || !strings.Contains(v, "articles (string, required)") {
		t.Fatalf("empty root must advise its current question: %q", v)
	}
	// The same actual owning facts retain the GraphQL document when the typed owner is unavailable.
	var fallback []string
	for _, name := range rr.Registry.Names() {
		if name != "prepare_task" {
			fallback = append(fallback, name)
		}
	}
	rr.Registry = rr.Registry.View(fallback)
	if v := advice(rr); !strings.Contains(v, "tool: plan") || !strings.Contains(v, "articles (string, required)") {
		t.Fatalf("current question changed with facade availability=%q", v)
	}
	// Execute the advised document through the actual registered GraphQL owner, replacing authored placeholders only.
	execute := func(rr *agent.Runner, document string) map[string]json.RawMessage {
		def, ok := rr.Registry.Lookup("graphql")
		if !ok {
			t.Fatal("missing graph owner")
		}
		payload, _ := json.Marshal(map[string]string{"query": document})
		args, why := tool.ValidateJSON(def, string(payload))
		if why != "" {
			t.Fatal(why)
		}
		if def.Bind != nil {
			var e error
			def, args, e = def.Bind(context.Background(), args)
			if e != nil {
				t.Fatal(e)
			}
		}
		res, e := def.Execute(context.Background(), args)
		if e != nil || res.Failed {
			t.Fatalf("advised document %q: %s %v", document, res.Text, e)
		}
		var env struct {
			Data   map[string]json.RawMessage
			Errors []string
		}
		if e = json.Unmarshal([]byte(res.Text), &env); e != nil || len(env.Errors) > 0 {
			t.Fatalf("advised result: %s %v", res.Text, e)
		}
		return env.Data
	}
	// The advanced GraphQL owner remains exercised explicitly; next-action advice
	// follows the current plan question instead of imposing this alternate route.
	first := rr.Prompts.Text("preparation.fresh_graphql")
	document := first[strings.Index(first, "mutation") : strings.LastIndex(first, "}")+1]
	for _, pair := range [][2]string{{"<author title>", "authored real title"}, {"<author goal>", "actual requested goal"}, {"<author strategy>", "inspected strategy"}, {"<author task>", "real task"}, {"<author requested work>", "actual requested work"}, {"<observed problem>", "observed source problem"}, {"<desired behavior>", "desired result"}, {"<implementation>", "inspected implementation"}, {"<observable desired behavior>", "command true exit0"}, {"<actual test scope>", "existing project tests"}, {"<reviewer instructions>", "independent actual check"}, {"<configured or inspected command>", "true"}, {"<inspected behavior assertion command>", "test -d ."}} {
		document = strings.ReplaceAll(document, pair[0], pair[1])
	}
	data := execute(rr, document)
	var created struct{ ID string }
	var prepared struct{ Plan struct{ ID string } }
	if err = json.Unmarshal(data["prepareTask"], &prepared); err == nil {
		created.ID = prepared.Plan.ID
	}
	if err != nil || created.ID == "" {
		t.Fatalf("creation data=%v %v", data, err)
	}
	plans, err := plan.ListPlans(s, "")
	if err != nil || len(plans) != 1 || plans[0].ID != created.ID {
		t.Fatalf("actual publication %v %v", plans, err)
	}

	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := plan.WithProject(context.Background(), root)
	p, e := plan.CreatePlanContext(ctx, s, "real plan", "real goal")
	if e != nil {
		t.Fatal(e)
	}
	if v := advice(rr); !strings.Contains(v, "tool: plan") || strings.Contains(v, p.ID) {
		t.Fatalf("unselected root invented selection: %q", v)
	}
	selected := makeRunner(&session.WorkflowScope{Store: s.Identity(), Kind: "plan", ID: p.ID})
	if v := advice(selected); !strings.Contains(v, "strategy (string, required)") || strings.Contains(v, "COMPLETE") || strings.Contains(v, "EXECUTE") {
		t.Fatalf("draft selected action=%q", v)
	}
	if _, e = plan.TransitionPlan(s, p.ID, plan.PlanActive); e != nil {
		t.Fatal(e)
	}
	if v := advice(selected); !strings.Contains(v, "strategy (string, required)") || strings.Contains(v, "transitionPlan") {
		t.Fatalf("stale plan action=%q", v)
	}
	data = execute(selected, fmt.Sprintf(`mutation { createApproach(planId:%q,title:"actual chosen strategy"){id state} }`, p.ID))
	var approach struct{ ID string }
	if e = json.Unmarshal(data["createApproach"], &approach); e != nil || approach.ID == "" {
		t.Fatalf("created approach %v %v", data, e)
	}
	approachRunner := makeRunner(&session.WorkflowScope{Store: s.Identity(), Kind: "approach", ID: approach.ID})
	document = fmt.Sprintf(`mutation { createTask(approachId:%q,title:"real task",prompt:"implement actual requested change"){id state} }`, approach.ID)
	data = execute(approachRunner, document)
	var task struct{ ID string }
	if e = json.Unmarshal(data["createTask"], &task); e != nil || task.ID == "" {
		t.Fatalf("created task %v %v", data, e)
	}
	taskRunner := makeRunner(&session.WorkflowScope{Store: s.Identity(), Kind: "task", ID: task.ID})
	document = fmt.Sprintf(`mutation { setTaskBlock(id:%q,current:"observed bug",target:"fixed behavior",sketch:"repair exact source",acceptance:"command true exit0",tests:"go tests",verify:"independent actual check and review",checkCommand:"true",acceptanceCommand:"test -d ."){id state} }`, task.ID)
	execute(taskRunner, document)
	actual, e := plan.GetTask(s, task.ID)
	if e != nil || actual.Current != "observed bug" || actual.CheckCommand != "true" {
		t.Fatalf("actual brief publication %+v %v", actual, e)
	}
	if _, e = plan.Ready(ctx, s, approach.ID, func(context.Context, *plan.Plan, []*plan.Task) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if v := advice(taskRunner); !strings.Contains(v, "tool: plan") || !strings.Contains(v, "NEXT") || strings.Contains(v, "task_start") {
		t.Fatalf("ready task question=%q", v)
	}
	if _, e = plan.TransitionApproach(s, approach.ID, plan.ApproachRunning); e != nil {
		t.Fatal(e)
	}
	if v := advice(taskRunner); strings.Contains(v, "task_start") {
		t.Fatalf("stale ready advice=%q", v)
	}

	if _, e = plan.CreatePlanContext(ctx, s, "second", "other real goal"); e != nil {
		t.Fatal(e)
	}
	if v := advice(rr); !strings.Contains(v, "tool: plan") || strings.Contains(v, p.ID) {
		t.Fatalf("ambiguous choice=%q", v)
	}
	selected.Continuation.Workflow.ID = "missing"
	if _, err := selected.Assigned.Facts(context.Background()); err == nil {
		t.Fatal("absent scope gained current-question authority")
	}
	// Stored closed-node browsing fixture only, not fabricated execution/qualification.
	if e = s.Write("MATCH (p:Plan {id: $id}) SET p.state = $state", map[string]any{"id": p.ID, "state": plan.PlanComplete}); e != nil {
		t.Fatal(e)
	}
	selected.Continuation.Workflow.ID = p.ID
	if v := advice(selected); !strings.Contains(v, "STATUS") || strings.Contains(v, "EXECUTE") {
		t.Fatalf("closed view advice=%q", v)
	}
	selected.Continuation.Workflow.Store = "foreign"
	if _, err := selected.Assigned.Facts(context.Background()); err == nil {
		t.Fatal("foreign view gained current-question authority")
	}

}

func TestAssignedLoopActualFailureEvidence(t *testing.T) {
	for _, kind := range []string{"missing-read", "bash-exit", "graphql-summary"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			s, e := store.Open(filepath.Join(dir, "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			cfg := config.Defaults()
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Recall.TopN = 0
			cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
			cfg.Agent.LoopRepeat, cfg.Agent.LoopStall = 0, 1
			var trip string
			bindDecisionFixture(t, &cfg, func(q decision.Request) int {
				if len(q.Options) == 3 && strings.Contains(q.Context, "cadence review") {
					trip = q.Context
				}
				if kind == "graphql-summary" && len(q.Options) == 5 {
					return 1
				}
				return 0
			})
			scopeRef := "root"
			calls := []codingCall{}
			for i := 0; i < 6; i++ {
				calls = append(calls, codingCall{"read", map[string]any{"path": "absent"}})
			}
			if kind == "bash-exit" {
				task := assignedFixtureTask(t, s, "true")
				scopeRef = task.ID
				calls = []codingCall{{"task_start", map[string]any{"task": task.ID}}}
				for i := 0; i < 6; i++ {
					calls = append(calls, codingCall{"bash", map[string]any{"cmd": "printf actual-output; exit 7", "timeout": 5}})
				}
			}
			if kind == "graphql-summary" {
				calls = nil
				for i := 0; i < 6; i++ {
					calls = append(calls, codingCall{"graphql", map[string]any{"query": `query { __type(name:"Mutation") { name } }`}})
				}
			}
			model := codingFixture(t, "structured", calls)
			cfg.Model.Server = model.URL
			rr, _, e := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, scopeRef)}, Cfg: cfg, Store: s, Server: model.URL, Model: "fixture", Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "loop-error", MaxSteps: 8, Window: 32768}, "investigate")
			if e != nil {
				t.Fatal(e)
			}
			_, _, e = rr.Run(context.Background(), "investigate")
			if e != nil || rr.Outcome.Status != outcome.Stopped || rr.Continuation.Coding.Passed() {
				t.Fatalf("outcome=%+v %v", rr.Outcome, e)
			}
			events, e := session.Read(filepath.Join(dir, "sessions"), "loop-error")
			if e != nil {
				t.Fatal(e)
			}
			results := 0
			for _, event := range events {
				if event.Kind == "native_result" {
					results++
				}
			}
			if results != len(calls) {
				t.Fatalf("advisory cadence prevented actual calls: results=%d want=%d outcome=%+v", results, len(calls), rr.Outcome)
			}
			var observed struct {
				Latest struct {
					Tool, Error  string
					Failed       bool
					ExitCode     *int
					OutputBytes  int
					OutputSHA256 string
					TextBasis    string
				}
			}
			if e = json.Unmarshal([]byte(trip), &observed); e != nil {
				t.Fatalf("loop evidence %q %v", trip, e)
			}
			if kind == "graphql-summary" {
				if observed.Latest.Tool != "graphql" || observed.Latest.TextBasis != "GraphQL observation summary; response body omitted" || observed.Latest.OutputBytes == 0 || len(observed.Latest.OutputSHA256) != 64 {
					t.Fatalf("mislabelled GraphQL provenance %+v", observed)
				}

				rawBody := ""
				for _, event := range events {
					if event.Kind == "native_result" {
						var entry session.Entry
						if e = json.Unmarshal([]byte(event.Text), &entry); e != nil {
							t.Fatal(e)
						}
						rawBody = entry.Message.Content
					}
				}
				if rawBody == "" || !strings.Contains(rawBody, "Mutation") || observed.Latest.OutputSHA256 == fmt.Sprintf("%x", sha256.Sum256([]byte(rawBody))) {
					t.Fatalf("summary hash confused with actual archived response: loop=%+v raw=%q", observed, rawBody)
				}
				return
			}
			if kind == "missing-read" {
				if observed.Latest.Tool != "read" || observed.Latest.Error == "" {
					t.Fatalf("missing actual error %+v", observed)
				}
			} else if observed.Latest.Tool != "bash" || !observed.Latest.Failed || observed.Latest.ExitCode == nil || *observed.Latest.ExitCode != 7 || observed.Latest.OutputBytes == 0 || len(observed.Latest.OutputSHA256) != 64 {
				t.Fatalf("missing settled Bash receipt %+v", observed)
			}
		})
	}
}

// Model and judge are doubles; public New, HTTP/native transport, claim, host
// check, reviewer, session receipts and Kuzu settlement are real. This tests
// delivered evidence, not whether a scripted PASS is semantically trustworthy.
func TestAssignedReviewReceivesOriginalBriefAndHostEvidence(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Auth.Mode, cfg.Recall.TopN = config.AuthYOLO, 0
	cfg.Templates.Path = assignedTemplates()
	cfg.Coding.CheckCommand = "true"
	task := assignedFixtureTask(t, s, cfg.Coding.CheckCommand)
	original := "Fix chained questions; preserve consecutive free text, cancellation and answer comments. Add a relevant regression."
	falseReport := "The implementation and new regression are complete."
	var review string
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []llm.Message `json:"messages"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		delta := map[string]any{"content": falseReport}
		reason := "stop"
		first := ""
		for _, message := range body.Messages {
			if message.Role == "user" && strings.HasPrefix(message.Content, "Review ") {
				first = message.Content
				break
			}
		}
		if strings.HasPrefix(first, "Review ") {
			if review == "" {
				review = first
			}
			if body.Messages[len(body.Messages)-1].Role == "user" {
				raw, _ := json.Marshal(map[string]any{"timeout": 2})
				delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "review-check", "type": "function", "function": map[string]any{"name": "check", "arguments": string(raw)}}}}
				reason = "tool_calls"
				calls = append(calls, "review-check")
			} else {
				delta["content"] = "PASS: scripted check-only claim"
			}
		} else if len(calls) == 0 {
			raw, _ := json.Marshal(map[string]any{"task": task.ID})
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "main-claim", "type": "function", "function": map[string]any{"name": "task_start", "arguments": string(raw)}}}}
			reason = "tool_calls"
			calls = append(calls, "main-claim")
		}
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	}))
	defer srv.Close()
	bindDecisionFixture(t, &cfg, nil)
	sink := &codingSink{dir: filepath.Join(dir, "sessions")}
	r, _, err := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, task.ID)}, Cfg: cfg, Store: s, Session: sink, SessionID: "review-handoff", Server: srv.URL, Model: "component", Window: 32768, MaxSteps: 3}, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.Run(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	// All requirements/facts precede prose, so fake worker claims are not the only input.
	prose := strings.Index(review, "worker report (unverified): "+falseReport)
	for _, want := range []string{
		"original execution requirements: " + original, "frozen task request: " + task.Prompt,
		"current: " + task.Current, "target: " + task.Target, "sketch: " + task.Sketch,
		"tests: " + task.Tests, "Verify instructions: " + task.Verify, "acceptance: " + task.Acceptance,
		"publications=0 tracked=0 net=0 partial=false", "coverage/requested-task correctness unverified",
	} {
		at := strings.Index(review, want)
		if at < 0 || prose < 0 || at >= prose {
			t.Errorf("review missing/precedes prose %q: %s", want, review)
		}
	}
	prefix := "current typed check facts: "
	start := strings.Index(review, prefix)
	if start < 0 {
		t.Fatalf("no check facts: %s", review)
	}
	raw := strings.SplitN(review[start+len(prefix):], "\n", 2)[0]
	var coding session.CodingState
	if err = json.Unmarshal([]byte(raw), &coding); err != nil || !coding.Passed() || coding.TaskID != task.ID || coding.Attempt != 1 || coding.Command != "true" {
		t.Fatalf("check facts are not exact owner facts: %+v %v", coding, err)
	}
	actual, err := plan.GetTask(s, task.ID)
	if err != nil || !actual.Qualified() || actual.Result != falseReport || len(calls) != 2 {
		t.Fatalf("existing check/PASS settlement changed: %+v calls=%v err=%v", actual, calls, err)
	}
	events, err := session.Read(sink.dir, "review-handoff")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == "native_reply" && strings.Contains(event.Text, falseReport) {
			return
		}
	}
	t.Fatal("original false worker response was not retained")
}
