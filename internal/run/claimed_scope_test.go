package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/launch"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

// Scripted native model/decision only: owning store, claims, files and Bash are real.
func TestClaimedTaskScopeNativeCoding(t *testing.T) {
	for _, variant := range []struct {
		name                                     string
		ordinary, frozen, preparation, postclaim bool
	}{{name: "postclaim", ordinary: true, postclaim: true}, {name: "postclaim-uncertain", ordinary: true, postclaim: true}, {name: "focused"}, {name: "browse-other"}, {name: "ordinary", ordinary: true}, {name: "frozen", frozen: true}, {name: "preparation", ordinary: true, preparation: true}} {
		t.Run(variant.name, func(t *testing.T) {
			frozen := variant.frozen
			dir := t.TempDir()
			t.Chdir(dir)
			s := openAssignedStore(t, dir)
			cfg := config.Defaults()
			cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Recall.TopN = 0
			cfg.Templates.Path = assignedTemplates()
			// Keep test instrumentation outside the project inventory being reviewed.
			checkMarker := filepath.Join(t.TempDir(), "check-marker")
			cfg.Coding.CheckCommand = fmt.Sprintf(`test "$(cat facts.txt)" = observed; printf check >> %q`, checkMarker)
			confidence := 0.99

			bindDecisionFixture(t, &cfg, func(q decision.Request) int {
				var f struct {
					NormalFinal       bool
					Checkpoint        agent.ProgressCheckpoint
					Facts             map[string]json.RawMessage
					ProvisionalAnswer string
				}
				_ = json.Unmarshal([]byte(q.Context), &f)
				if variant.postclaim && f.Checkpoint.Phase == "EXECUTE" && !f.NormalFinal {
					if f.Facts["Preparation"] != nil || f.Facts["Checkpoint"] != nil {
						t.Error("claimed facts duplicate the authoritative checkpoint")
					}
					return 1
				}
				if variant.preparation && f.NormalFinal && strings.Contains(q.Context, "UNSUPPORTED_PREPARATION_SUCCESS") {
					return 1
				}
				return 0
			}, confidence)
			task := assignedFixtureTask(t, s, cfg.Coding.CheckCommand, `test "$(cat facts.txt)" = observed`)
			other := assignedFixtureTask(t, s, cfg.Coding.CheckCommand, `test "$(cat facts.txt)" = observed`)
			scope, e := launch.SelectScope(s, task.ID)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile("facts.txt", []byte("observed\n"), 0600); e != nil {
				t.Fatal(e)
			}
			calls := []codingCall{{"task_start", map[string]any{"task": task.ID}}, {"workflow_focus", map[string]any{"ref": task.ID}}, {"read", map[string]any{"path": "facts.txt"}}, {"bash", map[string]any{"cmd": "printf scoped > bash-marker"}}, {"check", map[string]any{"timeout": 2}}}
			if variant.name == "browse-other" {
				calls[1].args = map[string]any{"ref": other.ID}
			}
			if variant.ordinary {
				calls = append(calls[:1], calls[2:]...)
			}
			if frozen {
				calls = []codingCall{calls[0], calls[2]}
			}
			requests, preparationStage := 0, 0
			restoring := false
			var running *agent.Runner
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/chat/completions" {
					t.Errorf("unexpected route %s", req.URL.Path)
				}
				var body struct {
					Tools    []struct{ Function struct{ Name string } }
					Messages []llm.Message
					Stream   bool
				}
				if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
					t.Error(e)
				}
				if !body.Stream {
					t.Error("native request did not stream")
				}
				// Compaction uses the same model endpoint, with a distinct document
				// request. It must not consume a scripted coding step or be tested
				// as a role/tool advertisement. Match the actual catalog template.
				compactTemplate, err := running.Prompts.Render("templates.compaction", map[string]any{"Words": "COMPACTION_WORD_TARGET", "Range": "COMPACTION_RANGE_BOUNDARY"})
				if err != nil {
					t.Error(err)
					return
				}
				compactPrefix, _, _ := strings.Cut(compactTemplate, "COMPACTION_RANGE_BOUNDARY")
				if len(body.Messages) > 0 && compactPrefix != "" && strings.HasPrefix(body.Messages[0].Content, compactPrefix) {
					if len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Role != "user" || len(body.Tools) != 0 {
						t.Fatal("compaction lost its two-role, no-tools request contract")
					}
					currentRequest := "observe and check"
					if restoring {
						currentRequest = "read provenance"
					}
					if !strings.Contains(body.Messages[1].Content, "reference-only=") || !strings.Contains(body.Messages[1].Content, currentRequest) {
						t.Errorf("compaction source lost provenance or current request %q (restoring=%t)", currentRequest, restoring)
					}
					t.Log("answered factual compaction document request separately from coding role")
					b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Historical preparation and tool exchanges occurred; retain the original requirements, user note, current request and recorded owner receipts."}, "finish_reason": "stop"}}})
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
					return
				}
				if variant.preparation && preparationStage < 2 {
					delta := map[string]any{"content": "UNSUPPORTED_PREPARATION_SUCCESS", "reasoning": "invented preparation diagnosis"}
					finish := "stop"
					if preparationStage == 0 {
						delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "prep-read", "type": "function", "function": map[string]string{"name": "read", "arguments": `{"path":"facts.txt"}`}}}}
						finish = "tool_calls"
					}
					preparationStage++
					b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
					return
				}
				names := map[string]bool{}
				for _, def := range body.Tools {
					names[def.Function.Name] = true
				}
				review := false
				for _, m := range body.Messages {
					review = review || strings.HasPrefix(m.Content, "Review ")
				}
				if !variant.ordinary && !review && requests == 0 {
					for _, name := range []string{"read", "find_file", "find_dir", "help", "grep", "ask"} {
						if !names[name] {
							t.Errorf("unclaimed selected task lacks discovery %s", name)
						}
					}
					for _, name := range []string{"bash", "check", "edit", "write"} {
						if names[name] {
							t.Errorf("unclaimed selected task exposes effect %s", name)
						}
					}
				}
				settled := false
				if !review && running != nil {
					current, e := plan.GetTask(s, task.ID)
					if e != nil {
						t.Error(e)
					} else {
						settled = current.Qualified()
					}
				}
				if !review {
					base, behavior, intent, current := "", 0, 0, 0
					originalIntent, currentRequest := "observe and check", "observe and check"
					if variant.preparation {
						originalIntent = "previous user intent"
					}
					if restoring {
						currentRequest = "read provenance"
					}
					for _, m := range body.Messages {
						if m.Role == "system" && strings.HasPrefix(m.Content, "You are a professional software engineer") {
							base = m.Content
						}
						if m.Role == "system" && m.Content == "CLAIMED_ROLE_BEHAVIOR_κ" {
							behavior++
						}
						if m.Role == "user" && m.Content == originalIntent {
							intent++
						}
						if m.Role == "user" && m.Content == currentRequest {
							current++
						}
					}
					guides := 0
					for _, m := range body.Messages {
						if m.Role == "system" {
							guides += strings.Count(m.Content, "Implement the requested behavior")
						}
					}
					wantGuides := 0
					if !restoring && !frozen && requests > 0 && !settled {
						wantGuides = 1
					}
					if guides != wantGuides || !strings.Contains(base, "Workspace: "+running.Workspace) {
						t.Errorf("actual claim phase guides=%d want%d or base workspace missing", guides, wantGuides)
					}
					if wantGuides == 1 && !strings.Contains(base, "Bound task check: "+running.CheckCommand) {
						t.Error("claimed task check missing or mislabeled")
					}
					if behavior != 1 || intent != 1 || current != 1 {
						t.Errorf("prefix ownership behavior=%d original-intent=%d current-request=%d", behavior, intent, current)
					}
					if !restoring && !frozen && requests > 0 && !settled {
						if !strings.Contains(base, task.ID) || !strings.Contains(base, "attempt1") || !strings.Contains(base, "Keep this claimed brief") || !strings.Contains(base, "independent review") {
							t.Errorf("missing live direct-task guidance: %s", base)
						}
						if strings.Contains(base, "Invoke advertised launch") || strings.Contains(base, "PLANNING ORCHESTRATOR:") || strings.Contains(base, "Only launch or explicit workflow_resume") {
							t.Errorf("claimed wire retained preparation/delegation: %s", base)
						}
						if !variant.ordinary && !strings.Contains(base, "Selected workflow: task") {
							t.Error("selected identity header lost")
						}
					} else if settled && !restoring {
						if !strings.Contains(base, "Settled task "+task.ID) {
							t.Error("actual settled identity lost")
						}
					} else if !strings.Contains(base, "PLANNING ORCHESTRATOR:") {
						t.Error("unclaimed preparation guidance lost")
					}
				}
				if !review {
					claimed := !restoring && !frozen && requests > 0 && !settled
					for _, name := range []string{"prepare_task", "ready_task", "task_start"} {
						if names[name] == claimed {
							t.Errorf("claim phase=%t preparation schema %s present=%t", claimed, name, names[name])
						}
					}
					if variant.postclaim && claimed && requests == 1 {
						calls, receipts := 0, 0
						// A successful claim already carries the execution receipt; it
						// remains correlated when current host context follows it.
						for _, message := range body.Messages {
							for _, call := range message.ToolCalls {
								if message.Role == "assistant" && call.ID == "call-0-0" && call.Name == "task_start" {
									calls++
								}
							}
							if message.Role == "tool" && message.ToolCallID == "call-0-0" && strings.Contains(message.Content, "claimed task "+task.ID) {
								receipts++
							}
							if strings.Contains(message.Content, "Harness bookkeeping request:") || strings.Contains(message.Content, "grants no coding/check/review authority") {
								t.Errorf("successful claim added redundant advice: %s", message.Content)
							}
						}
						if calls != 1 || receipts != 1 {
							t.Errorf("successful claim lost exact correlation: calls=%d receipts=%d", calls, receipts)
						}
					}
					if !names["graphql"] || !names["help"] {
						t.Error("phase projection lost bookkeeping/help")
					}
					if requests == 1 && !restoring && !frozen {
						def, ok := running.Registry.Lookup("help")
						if !ok {
							t.Error("current help definition missing")
							return
						}
						doc, err := def.Execute(req.Context(), map[string]any{"topic": "guide"})
						if err != nil {
							t.Error(err)
						}
						for _, name := range []string{"prepare_task", "ready_task", "task_start"} {
							if strings.Contains(doc.Text, "## help."+name+"\n") {
								t.Errorf("claimed help retained unavailable section %s", name)
							}
						}
						if !strings.Contains(doc.Text, "## help.edit\n") || !strings.Contains(doc.Text, "## help.check\n") {
							t.Error("claimed help lost coding/check sections")
						}
					}
				}
				if !restoring && !frozen && !review && requests > 0 && !settled {
					for _, name := range agent.ToolNames("coding") {
						if !names[name] {
							t.Errorf("claimed next wire lacks %s: %+v", name, names)
						}
					}
				}

				if variant.name == "browse-other" && requests > 1 && !review && !restoring && !settled {
					if running.Continuation.Workflow.ID != task.ID || running.Continuation.Assignment.TaskID != task.ID {
						t.Error("browsing replaced active task authority")
					}
					found := false
					for _, m := range body.Messages {
						found = found || m.Role == "tool" && strings.Contains(m.Content, "not a task handoff") && strings.Contains(m.Content, other.ID) && strings.Contains(m.Content, task.ID)
					}
					if !found {
						t.Error("missing truthful browsing/claim receipt")
					}
				}
				if !review && settled {
					for _, name := range []string{"bash", "check", "edit", "write"} {
						if names[name] {
							t.Errorf("settled phase retained coding %s", name)
						}
					}
				}
				if variant.preparation && requests == 1 && !restoring && !review {
					old, note, source, claim, brief, proposal, reasoning := false, false, false, false, false, false, false
					for _, m := range body.Messages {
						proposal = proposal || strings.Contains(m.Content, "UNSUPPORTED_PREPARATION_SUCCESS")
						reasoning = reasoning || strings.Contains(m.Reasoning, "invented preparation diagnosis")
						old = old || m.Content == "previous-run standalone reply"
						note = note || m.Content == "keep exact user note κ"
						source = source || m.ToolCallID == "prep-read" && strings.Contains(m.Content, "observed")
						claim = claim || m.ToolCallID == "call-0-0" && strings.Contains(m.Content, task.ID)
						if m.ToolCallID == "call-0-0" && strings.Contains(m.Content, "Frozen authored requirements") {
							t.Error("claim receipt duplicated the system brief")
						}
						if m.Role == "system" {
							brief = brief || strings.Contains(m.Content, "Behavioral check: "+task.AcceptanceCommand) && strings.Contains(m.Content, "Project check: "+task.CheckCommand) && strings.Contains(m.Content, task.Current) && strings.Contains(m.Content, task.Prompt)
						}
					}
					if !old || !note || !source || !claim || !brief || !proposal || !reasoning {
						t.Errorf("claim projection lost prior=%t note=%t source=%t claim=%t brief=%t proposal=%t reasoning=%t", old, note, source, claim, brief, proposal, reasoning)
					}
				}
				if !frozen && requests == 1 {
					a := running.Continuation.Assignment
					for _, scope := range []*session.WorkflowScope{{Store: a.Store, Kind: "task", ID: other.ID}, {Store: a.Store, Kind: "plan", ID: a.PlanID}, {Store: a.Store, Kind: "approach", ID: a.ApproachID}} {
						view, _, err := running.WorkflowView(scope)
						if err != nil {
							t.Error(err)
							continue
						}
						for _, name := range []string{"read", "find_file", "find_dir", "help", "grep", "ask"} {
							if _, ok := view.Lookup(name); !ok {
								t.Errorf("other scope %+v lacks discovery %s", scope, name)
							}
						}
						for _, name := range []string{"bash", "edit", "write"} {
							if _, ok := view.Lookup(name); ok {
								t.Errorf("other scope %+v gained %s", scope, name)
							}
						}
					}
					if _, _, err := running.WorkflowView(&session.WorkflowScope{Store: "foreign", Kind: "task", ID: a.TaskID}); err == nil {
						t.Error("foreign store accepted current task ID")
					}
				}
				n := requests
				requests++
				delta := map[string]any{"content": "PASS complete"}
				finish := "stop"
				group := []any{}
				add := func(c codingCall, index int) {
					raw, _ := json.Marshal(c.args)
					group = append(group, map[string]any{"index": index, "id": fmt.Sprintf("call-%d-%d", n, index), "type": "function", "function": map[string]string{"name": c.name, "arguments": string(raw)}})
				}
				if !restoring && !review && n < len(calls) {
					add(calls[n], 0)
				}
				if frozen && n == 0 {
					add(codingCall{"bash", map[string]any{"cmd": "printf scoped > bash-marker"}}, 1) // A real claim must settle before a coding array uses its authority.
				}
				if !restoring && review && n == len(calls)+1 {
					add(codingCall{"check", map[string]any{"timeout": 2}}, 0)
				}
				if len(group) > 0 {
					delta = map[string]any{"tool_calls": group}
					finish = "tool_calls"
				}
				b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
			}))
			defer srv.Close()
			cfg.Model.Server = srv.URL
			sink := &codingSink{dir: filepath.Join(dir, "sessions")}
			tools, continuation := "workflow", session.Continuation{Workflow: scope}
			if variant.ordinary {
				tools, continuation = "workflow", session.Continuation{Workflow: scope}
			}
			r, _, e := New(Deps{Tools: tools, Cfg: cfg, Store: s, Server: srv.URL, Model: "fixture", Window: 32768, MaxSteps: 8, Session: sink, SessionID: "claimed", Continuation: continuation}, "observe and check")
			if e != nil {
				t.Fatal(e)
			}
			r.Behavior = "CLAIMED_ROLE_BEHAVIOR_κ"
			if variant.preparation {
				r.MaxSteps += 2
				r.Continuation.Version = 1
				r.Continuation.Add("intent", "user", "previous user intent", false)
				r.Continuation.Add("reply", "assistant", "previous-run standalone reply", false)
				r.Continuation.Add("note", "user", "keep exact user note κ", false)
			}
			if frozen {
				r.MaxSteps = 1 // Isolate the invalid first array; do not script a later repair.
			}
			running = r
			if variant.name == "postclaim-uncertain" {
				judge := r.Judge
				r.Judge = func(ctx context.Context, q decision.Request) (decision.Verdict, error) {
					v, err := judge(ctx, q)
					if v.Option == "request executor PAT update" {
						v.Confidence = 0.6
					}
					return v, err
				}
			}
			_, _, runErr := r.Run(context.Background(), "observe and check")
			if variant.name == "browse-other" {
				untouched, err := plan.GetTask(s, other.ID)
				if err != nil || untouched.Attempts != 0 || untouched.State != plan.TaskQueued {
					t.Fatalf("browsing claimed another task: %+v %v", untouched, err)
				}
			}
			actual, e := plan.GetTask(s, task.ID)
			if e != nil {
				t.Fatal(e)
			}
			if frozen {
				if runErr != nil || requests != 1 || r.Outcome.Status != outcome.Stopped {
					t.Fatalf("invalid frozen array transport/settlement: requests=%d outcome=%+v err=%v", requests, r.Outcome, runErr)
				}
				if actual.Attempts != 0 {
					t.Fatalf("initial invalid array claimed task: %+v", actual)
				}
				for _, path := range []string{checkMarker, "bash-marker"} {
					if _, e = os.Stat(path); !os.IsNotExist(e) {
						t.Fatalf("invalid initial array published %s", path)
					}
				}
				return
			}
			// This script validates the claimed Task but never answers the parent
			// handoff questions. A qualified Task cannot complete its whole Plan.
			parent, parentErr := plan.TaskApproach(s, actual.ID)
			if parentErr != nil {
				t.Fatal(parentErr)
			}
			owner, ownerErr := plan.ApproachPlan(s, parent.ID)
			if ownerErr != nil {
				t.Fatal(ownerErr)
			}
			if runErr != nil || r.Outcome.Status != outcome.Stopped || !actual.Qualified() || parent.State != plan.ApproachRunning || owner.State != plan.PlanActive {
				t.Fatalf("task=%s qualified=%t approach=%s plan=%s outcome=%+v err=%v", actual.ID, actual.Qualified(), parent.State, owner.State, r.Outcome, runErr)
			}
			marker, _ := os.ReadFile("bash-marker")
			checked, _ := os.ReadFile(checkMarker)
			if string(marker) != "scoped" || string(checked) != "checkcheck" {
				t.Fatalf("real effects bash=%q check=%q", marker, checked)
			}
			events, e := session.Read(sink.dir, "claimed")
			if e != nil {
				t.Fatal(e)
			}
			readSeen := false
			for _, ev := range events {
				if ev.Kind == "native_result" && strings.Contains(ev.Text, "observed") {
					readSeen = true
				}
			}
			if !readSeen || r.Continuation.Coding.Receipt == nil || r.Continuation.Coding.Receipt.ExitCode == nil || *r.Continuation.Coding.Receipt.ExitCode != 0 {
				t.Fatal("missing actual read/current exit0 receipt")
			}
			if variant.preparation {
				archived := false
				for _, ev := range events {
					archived = archived || ev.Kind == "native_reply" && strings.Contains(ev.Text, "UNSUPPORTED_PREPARATION_SUCCESS")
				}
				if !archived {
					t.Fatal("original preparation reply lost from raw native archive")
				}
			}
			restored, _, e := session.Restore(events, 32768)
			if e != nil {
				t.Fatal(e)
			}
			if variant.preparation {
				proposal, reasoning := false, false
				for _, entry := range restored.Entries {
					proposal = proposal || strings.Contains(entry.Message.Content, "UNSUPPORTED_PREPARATION_SUCCESS")
					reasoning = reasoning || strings.Contains(entry.Message.Reasoning, "invented preparation diagnosis")
				}
				if !proposal || !reasoning {
					t.Fatal("durable restore lost retained preparation narrative/reasoning")
				}
			}
			reopened, _, e := New(Deps{Tools: tools, Cfg: cfg, Store: s, Server: srv.URL, Model: "fixture", Window: 32768, MaxSteps: 1, Session: sink, SessionID: "reopened", Continuation: restored}, "read provenance")
			if e != nil {
				t.Fatal(e)
			}
			for _, name := range []string{"read", "find_file", "find_dir", "help", "grep", "ask"} {
				if _, ok := reopened.Registry.Lookup(name); !ok {
					t.Fatalf("restored planning scope lacks discovery %s", name)
				}
			}
			for _, name := range []string{"bash", "check", "edit", "write"} {
				if _, ok := reopened.Registry.Lookup(name); ok && !variant.ordinary {
					t.Fatalf("restored provenance granted %s", name)
				}
			}
			if reopened.ClaimedTaskScope(scope) {
				t.Fatal("restored provenance renewed claim authority")
			}
			restoring = true
			reopened.Behavior = r.Behavior
			_, _, _ = reopened.Run(context.Background(), "read provenance")
		})
	}
}

func openAssignedStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, e := store.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s
}

// Scripted HTTP model/decision; the public builder, owning claim and native
// projection are real. The next response is held, not a coding-success claim.
func TestClaimSchemaPreservesMeasuredDensity(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s := openAssignedStore(t, dir)
	cfg := config.Defaults()
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Auth.Mode, cfg.Recall.TopN = config.AuthYOLO, 0
	cfg.Templates.Path = assignedTemplates()
	cfg.Coding.CheckCommand = "printf checked > check-marker"
	bindDecisionFixture(t, &cfg, nil)
	task := assignedFixtureTask(t, s, cfg.Coding.CheckCommand, `test "$(cat facts.txt)" = observed`)
	var thinking strings.Builder
	for i := 0; i < 700; i++ {
		fmt.Fprintf(&thinking, "Reference %04d: inspect the current question boundaries before changing source.\n", i)
	}
	type wire struct {
		Messages []llm.Message
		Tools    []llm.ToolDefinition
	}
	type observation struct {
		body wire
		c    session.Continuation
	}
	first := make(chan wire, 1)
	next := make(chan observation, 1)
	release := make(chan struct{})
	var running *agent.Runner
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/chat/completions" {
			t.Errorf("unexpected model route %s", req.URL.Path)
			http.NotFound(w, req)
			return
		}
		var body wire
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if len(body.Messages) == 2 && strings.Contains(body.Messages[0].Content, "Summarize") {
			t.Error("protected claim group must not become compaction source")
			return
		}
		requests++
		if requests == 1 {
			first <- body
			args, _ := json.Marshal(map[string]string{"task": task.ID})
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": thinking.String(), "tool_calls": []any{map[string]any{"index": 0, "id": "density-claim", "type": "function", "function": map[string]string{"name": "task_start", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}, "usage": map[string]int{"prompt_tokens": 1000, "completion_tokens": 2000}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			return
		}
		next <- observation{body, running.Continuation.Clone()}
		<-release
	}))
	defer srv.Close()
	cfg.Model.Server = srv.URL
	sink := &codingSink{dir: filepath.Join(dir, "sessions")}
	r, _, err := New(Deps{Tools: "workflow", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, task.ID)}, Cfg: cfg, Store: s, Server: srv.URL, Model: "fixture", Window: 40960, MaxSteps: 2, Session: sink, SessionID: "density"}, "observe and check")
	if err != nil {
		t.Fatal(err)
	}
	r.Behavior = "DENSITY_BEHAVIOR_κ"
	running = r
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer close(release)
	done := make(chan error, 1)
	go func() { _, _, err := r.Run(ctx, "observe and check"); done <- err }()
	initial := <-first
	var held observation
	select {
	case held = <-next:
	case err := <-done:
		t.Fatalf("claimed schema transition refused next native request: outcome=%+v err=%v", r.Outcome, err)
	}
	initialSchemas, _ := json.Marshal(initial.Tools)
	requestBytes := len(initialSchemas)
	for _, m := range initial.Messages {
		requestBytes += m.Size()
	}
	heldSchemas, _ := json.Marshal(held.body.Tools)
	if held.c.Measured || held.c.BasisTokens != 1000 || held.c.BasisChars != requestBytes || held.c.SchemaBytes != len(heldSchemas) || string(heldSchemas) == string(initialSchemas) {
		t.Errorf("changed schema lost calibrated request or invented measurement: %+v, original bytes=%d schema=%d", held.c.Snapshot(), requestBytes, len(initialSchemas))
	}
	names := map[string]bool{}
	for _, def := range held.body.Tools {
		names[def.Function.Name] = true
	}
	for _, name := range []string{"bash", "edit", "write", "check"} {
		if !names[name] {
			t.Errorf("actual claimed projection lacks %s", name)
		}
	}
	base, behavior, intent, claim, receipt := 0, 0, 0, 0, 0
	for _, m := range held.body.Messages {
		if m.Role == "system" && strings.HasPrefix(m.Content, "You are a professional software engineer") {
			base++
			if !strings.Contains(m.Content, "Bound task check: "+cfg.Coding.CheckCommand) {
				t.Error("bound configured check missing")
			}
		}
		if m.Role == "system" && m.Content == r.Behavior {
			behavior++
		}
		if m.Role == "user" && m.Content == "observe and check" {
			intent++
		}
		if len(m.ToolCalls) == 1 && m.ToolCalls[0].ID == "density-claim" && m.ToolCalls[0].Name == "task_start" && m.ReasoningContent == thinking.String() {
			claim++
		}
		if m.Role == "tool" && m.ToolCallID == "density-claim" && strings.Contains(m.Content, "claimed task "+task.ID) {
			receipt++
		}
	}
	if base != 1 || behavior != 1 || intent != 1 || claim != 1 || receipt != 1 || held.c.Assignment == nil || held.c.Assignment.TaskID != task.ID {
		t.Errorf("held native request lost owned inputs: base%d behavior%d intent%d call%d receipt%d assignment%+v", base, behavior, intent, claim, receipt, held.c.Assignment)
	}
	t.Logf("actual schema bytes %d→%d; measured basis1000/%d retained; next estimate%d (unmeasured)", len(initialSchemas), held.c.SchemaBytes, requestBytes, held.c.Estimate())
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("held request cancellation=%v", err)
	}
	events, err := session.Read(sink.dir, "density")
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := session.Restore(events, 40960, r.ModelKey)
	if err != nil {
		t.Fatal(err)
	}
	changed, _, err := New(Deps{Cfg: cfg, Store: s, Server: srv.URL, Model: "different-fixture", Window: 40960, MaxSteps: 1, Session: sink, SessionID: "changed-model", Continuation: restored}, "new model")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = changed.Run(context.Background(), "new model")
	if err != nil || changed.Continuation.BasisTokens != 0 || changed.Continuation.BasisChars != 0 || changed.Continuation.Measured || changed.ModelKey == r.ModelKey || !strings.Contains(changed.Outcome.Reason, "content estimate") {
		t.Fatalf("changed model reused stale density: outcome%+v error%v basis%d/%d", changed.Outcome, err, changed.Continuation.BasisTokens, changed.Continuation.BasisChars)
	}
	if _, err := os.Stat("check-marker"); !os.IsNotExist(err) {
		t.Fatal("held/canceled fixture executed a check")
	}
}
