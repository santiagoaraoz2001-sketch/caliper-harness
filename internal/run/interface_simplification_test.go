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
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Only model responses are scripted. The public client, registry, auth, Kuzu,
// source publication, Bash and durable session receipts execute normally.
func interfaceFixture(t *testing.T, protocol string, calls []codingCall) *httptest.Server {
	t.Helper()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if request.URL.Path != "/chat/completions" || body["stream"] != true {
			t.Error("fixture did not receive the normal streamed client request")
		}
		delta, reason := map[string]any{"content": "component episode complete"}, "stop"
		if n < len(calls) {
			c := calls[n]
			if protocol == "structured" {
				raw, _ := json.Marshal(c.args)
				delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("call", n), "type": "function", "function": map[string]string{"name": c.name, "arguments": string(raw)}}}}
				reason = "tool_calls"
			} else {
				var text strings.Builder
				fmt.Fprintf(&text, "```tool\ntool: %s\n", c.name)
				keys := []string{}
				for key := range c.args {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					value := c.args[key]
					if value, ok := value.(string); ok && (strings.ContainsAny(value, "\r\n") || value != strings.TrimSpace(value)) {
						if !strings.HasSuffix(value, "\n") {
							t.Error("exact text literal fixture requires its source newline")
						}
						fmt.Fprintf(&text, "%s: <<ARG_END\n%sARG_END\n", key, value)
					} else {
						fmt.Fprintf(&text, "%s: %v\n", key, value)
					}
				}
				text.WriteString("```")
				delta["content"] = text.String()
			}
		}
		n++
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func interfaceConfig(t *testing.T, protocol string) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", protocol
	cfg.Auth.Mode, cfg.Recall.TopN, cfg.Coding.CheckCommand = config.AuthAll, 0, "test -f marker"
	cfg.Templates.Path = assignedTemplates()
	bindDecisionFixture(t, &cfg, nil)
	return cfg
}

func TestInterfaceNativeAndTextNormalizedEffects(t *testing.T) {
	for _, protocol := range []string{"structured", "text"} {
		t.Run(protocol, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			original, replacement := "package p\r\n// old  \r\n", "// new\t %20 α🙂  \r\n"
			if err := os.WriteFile("source file.go", []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			command := "printf '%s' '  α🙂  ' > marker\n"
			calls := []codingCall{
				{" FiNd> ", map[string]any{"pattern": "*.go"}},
				{" READ> ", map[string]any{"file_path": "source%20file.go", "offset": "1", "limit": "2"}},
				{" EDIT></edit> ", map[string]any{"file_path": "source%20file.go", "old": "// old  \r\n", "new": replacement, "expected_hash": completionHash(original)}},
				{" BASH> ", map[string]any{"command": command, "timeout": "2"}},
				{" CHECK> ", map[string]any{"command": "test -f marker", "timeout": "2"}},
			}
			srv := interfaceFixture(t, protocol, calls)
			cfg := interfaceConfig(t, protocol)
			s := openAssignedStore(t, dir)
			sink := &codingSink{dir: filepath.Join(dir, "sessions")}
			approvals := []string{}
			r, _, err := New(Deps{Cfg: cfg, Store: s, Server: srv.URL, Model: "component", MaxSteps: 8, Window: 32768, Session: sink, SessionID: "normalized", AskYesNo: func(_ context.Context, text string) bool { approvals = append(approvals, text); return true }}, "change source exactly and check the actual marker")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = r.Run(t.Context(), "change source exactly and check the actual marker"); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile("source file.go")
			if err != nil || string(got) != "package p\r\n"+replacement {
				t.Fatalf("source bytes changed: %q %v", got, err)
			}
			marker, err := os.ReadFile("marker")
			if err != nil || string(marker) != "  α🙂  " {
				t.Fatalf("actual command effect=%q %v", marker, err)
			}
			c := r.Continuation.Coding
			if r.Continuation.Assignment != nil || c == nil || c.Attempt != 0 || !c.Passed() || c.Receipt == nil || !c.Receipt.Direct || c.Receipt.Acceptance != nil {
				t.Fatalf("ordinary effects acquired Task authority: %+v", c)
			}
			shown := strings.Join(approvals, "\n")
			if !strings.Contains(shown, "run find_file?") || !strings.Contains(shown, "source file.go") || strings.Contains(shown, "source%20file.go") {
				t.Fatalf("approval did not use the canonical target: %s", shown)
			}
			events, err := session.Read(sink.dir, "normalized")
			if err != nil {
				t.Fatal(err)
			}
			resolutions, results := 0, 0
			for _, event := range events {
				if event.Kind == "call_resolution" {
					var saved struct {
						ToolCallID, OriginalName, Name string
						OriginalArguments              any
						Arguments                      map[string]any
					}
					if err := json.Unmarshal([]byte(event.Text), &saved); err != nil {
						t.Fatal(err)
					}
					canonical := []string{"find_file", "read", "edit", "bash", "check"}[resolutions]
					if saved.Name != canonical {
						t.Fatalf("wrong normalized operation: %+v", saved)
					}
					if protocol == "structured" {
						raw, _ := json.Marshal(calls[resolutions].args)
						if saved.ToolCallID != fmt.Sprint("call", resolutions) || saved.OriginalName != calls[resolutions].name || saved.OriginalArguments != string(raw) {
							t.Fatalf("raw/canonical identity was lost: %+v", saved)
						}
					}
					if canonical == "read" || canonical == "edit" {
						if saved.Arguments["path"] != "source file.go" {
							t.Fatalf("resolved path was not archived: %+v", saved)
						}
					}
					if canonical == "bash" && saved.Arguments["cmd"] != command {
						t.Fatalf("command bytes were rewritten: %+v", saved)
					}
					resolutions++
				}
				if event.Kind == "native_result" {
					var saved session.Entry
					if err := json.Unmarshal([]byte(event.Text), &saved); err != nil {
						t.Fatal(err)
					}
					if saved.Message.ToolCallID != fmt.Sprint("call", results) || saved.Message.Name != calls[results].name || saved.Kind != "output" {
						t.Fatalf("original correlation changed: %+v", saved)
					}
					results++
				}
			}
			if resolutions != len(calls) || protocol == "structured" && results != len(calls) {
				t.Fatalf("calls duplicated/lost: resolutions=%d results=%d", resolutions, results)
			}
		})
	}
}

func TestInterfaceDraftCodingWaitsForClaim(t *testing.T) {
	for _, kind := range []string{"root", "plan", "approach", "task"} {
		t.Run(kind, func(t *testing.T) {
			f := newPlanningFixture(t)
			f.task()
			t.Chdir(f.d.Workspace)
			taskID := f.scope.ID
			n, err := launchScopeForTest(f)
			if err != nil {
				t.Fatal(err)
			}
			scope := &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: kind}
			if kind == "task" {
				scope.ID = taskID
			} else if kind == "approach" {
				scope.ID = n[0]
			} else if kind == "plan" {
				scope.ID = n[1]
			}
			srv := interfaceFixture(t, "structured", []codingCall{{" BASH> ", map[string]any{"command": "printf project > marker\n"}}, {"check", map[string]any{}}})
			cfg := interfaceConfig(t, "structured")
			sink := &codingSink{dir: filepath.Join(f.d.Workspace, "sessions")}
			r, _, err := New(Deps{Cfg: cfg, Store: f.d.Store, Server: srv.URL, Model: "component", Window: 32768, MaxSteps: 3, Session: sink, SessionID: "draft", Continuation: session.Continuation{Workflow: scope}, AskYesNo: func(context.Context, string) bool { return true }}, "ordinary project coding while drafting")
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"read", "find_file", "plan", "graphql", "help"} {
				if _, ok := r.Registry.Lookup(name); !ok {
					t.Fatalf("draft hid core tool %s", name)
				}
			}
			r.Run(t.Context(), "ordinary project coding while drafting")
			marker, err := os.ReadFile("marker")
			if !os.IsNotExist(err) {
				t.Fatalf("draft coding unexpectedly wrote=%q %v", marker, err)
			}
			task, err := plan.GetTask(f.d.Store, taskID)
			if err != nil {
				t.Fatal(err)
			}
			c := r.Continuation.Coding
			if task.State != plan.TaskQueued || task.Attempts != 0 || task.Qualified() || r.Continuation.Assignment != nil || c != nil && c.Receipt != nil {
				t.Fatalf("draft project work became Task progress: task=%+v coding=%+v", task, c)
			}
		})
	}
}

func launchScopeForTest(f *planningFixture) ([2]string, error) {
	var ids [2]string
	parent, err := plan.TaskApproach(f.d.Store, f.scope.ID)
	if err != nil {
		return ids, err
	}
	ancestor, err := plan.ApproachPlan(f.d.Store, parent.ID)
	if err != nil {
		return ids, err
	}
	return [2]string{parent.ID, ancestor.ID}, nil
}

func TestInterfaceNormalizedArrayPreflight(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s := openAssignedStore(t, dir)
	cfg := interfaceConfig(t, "structured")
	approvals := 0
	srv := codingFixture(t, "structured", []codingCall{{" BASH> ", map[string]any{"command": "touch first"}}, {"BASH", map[string]any{"cmd": "touch second", "command": "touch conflicting"}}}, 2)
	r, _, err := New(Deps{Cfg: cfg, Store: s, Server: srv.URL, Model: "component", MaxSteps: 2, Window: 32768, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "array", AskYesNo: func(context.Context, string) bool { approvals++; return true }}, "refuse conflicting array before effects")
	if err != nil {
		t.Fatal(err)
	}
	r.Run(t.Context(), "refuse conflicting array before effects")
	for _, marker := range []string{"first", "second", "conflicting"} {
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("whole-array preflight published %s", marker)
		}
	}
	if approvals != 0 {
		t.Fatalf("refused array crossed approval: %d", approvals)
	}
	results := 0
	for _, entry := range r.Continuation.Entries {
		if entry.Message.Role == "tool" {
			if entry.Kind != "failure" || !strings.Contains(strings.ToLower(entry.Message.Content), "conflict") || entry.Message.ToolCallID != fmt.Sprint("call", results) {
				t.Fatalf("refusal correlation lost: %+v", entry)
			}
			results++
		}
	}
	if results != 2 {
		t.Fatalf("missing refused-array receipts: %d", results)
	}
}

func TestInterfacePathFallbackKeepsLiteralAndScope(t *testing.T) {
	for _, mode := range []string{"decoded", "literal", "double decode", "outside", "URI", "final symlink"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			s := openAssignedStore(t, dir)
			input, target, original, want := "target%20file", "target file", "original", "replacement %20 α🙂\r\n"
			if err := os.WriteFile(target, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			permitted := mode == "decoded" || mode == "literal"
			switch mode {
			case "literal":
				target = input
				if err := os.WriteFile(target, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
			case "double decode":
				input = "target%2520file"
			case "outside":
				target = filepath.Join(t.TempDir(), "outside file")
				if err := os.WriteFile(target, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				input = strings.ReplaceAll(target, " ", "%20")
			case "URI":
				input = "file://" + filepath.Join(dir, input)
			case "final symlink":
				target = filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(target, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, "link file"); err != nil {
					if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
						t.Skipf("native file symlink privilege unavailable: %v", err)
					}
					t.Fatal(err)
				}
				input = "link%20file"
			}
			cfg := interfaceConfig(t, "structured")
			approvals := []string{}
			srv := interfaceFixture(t, "structured", []codingCall{{" WRITE> ", map[string]any{"file_path": input, "content": want, "expected_hash": completionHash(original)}}})
			r, _, err := New(Deps{Cfg: cfg, Store: s, Server: srv.URL, Model: "component", Window: 32768, MaxSteps: 1, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "path", AskYesNo: func(_ context.Context, text string) bool { approvals = append(approvals, text); return true }}, "change only the unambiguous permitted path")
			if err != nil {
				t.Fatal(err)
			}
			r.Run(t.Context(), "change only the unambiguous permitted path")
			got, err := os.ReadFile(target)
			expected := original
			if permitted {
				expected = want
			}
			if err != nil || string(got) != expected {
				t.Fatalf("path %s source=%q want=%q err=%v", mode, got, expected, err)
			}
			if mode == "literal" {
				untouched, _ := os.ReadFile("target file")
				if string(untouched) != original {
					t.Fatal("literal existing path lost priority")
				}
			}
			if mode == "outside" && len(approvals) != 0 {
				t.Fatal("decoded outside mutation reached approval")
			}
			if mode == "decoded" && !strings.Contains(strings.Join(approvals, "\n"), "target file") {
				t.Fatal("approval missed resolved actual path")
			}
		})
	}
}

func TestInterfaceDraftAdmissionPreservesSelectedRevisions(t *testing.T) {
	for _, mode := range []string{"selected revision", "ancestor revision", "sibling revision", "plan with sibling revision", "revision during approval"} {
		t.Run(mode, func(t *testing.T) {
			f := newPlanningFixture(t)
			f.task()
			t.Chdir(f.d.Workspace)
			ids, err := launchScopeForTest(f)
			if err != nil {
				t.Fatal(err)
			}
			scope, targetKind, targetID := f.scope, "task", f.scope.ID
			if mode == "ancestor revision" {
				targetKind, targetID = "approach", ids[0]
			}
			if strings.Contains(mode, "sibling") {
				sibling, err := plan.CreateTaskContext(plan.WithProject(t.Context(), f.d.Workspace), f.d.Store, ids[0], "unrelated sibling", "authored sibling")
				if err != nil {
					t.Fatal(err)
				}
				targetID = sibling.ID
				if mode == "plan with sibling revision" {
					scope = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "plan", ID: ids[1]}
				}
			}
			requestRevision := func() {
				ctx := plan.WithProject(t.Context(), f.d.Workspace)
				snapshot, _, err := plan.ContractSnapshot(ctx, f.d.Store, targetKind, targetID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = plan.RequestRevision(ctx, f.d.Store, targetKind, targetID, snapshot, "user", "fixture", "explicit authored revision"); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "revision during approval" {
				requestRevision()
			}
			cfg := interfaceConfig(t, "structured")
			approvals := 0
			srv := interfaceFixture(t, "structured", []codingCall{{" BASH> ", map[string]any{"command": "printf ordinary > marker\n"}}})
			r, _, err := New(Deps{Cfg: cfg, Store: f.d.Store, Server: srv.URL, Model: "component", Window: 32768, MaxSteps: 1, Session: &codingSink{dir: filepath.Join(f.d.Workspace, "sessions")}, SessionID: "scope", Continuation: session.Continuation{Workflow: scope}, AskYesNo: func(context.Context, string) bool {
				approvals++
				if mode == "revision during approval" {
					requestRevision()
				}
				return true
			}}, "ordinary project coding must honor selected scope")
			if err != nil {
				t.Fatal(err)
			}
			r.Run(t.Context(), "ordinary project coding must honor selected scope")
			marker, readErr := os.ReadFile("marker")
			if !os.IsNotExist(readErr) {
				t.Fatalf("selected/sibling scope admission mode=%s marker=%q err=%v", mode, marker, readErr)
			}
			if approvals != 0 {
				t.Fatal("selected revision reached approval")
			}
			actual, err := plan.GetTask(f.d.Store, f.scope.ID)
			if err != nil || actual.Attempts != 0 || actual.State != plan.TaskQueued || r.Continuation.Assignment != nil {
				t.Fatalf("pre-claim project work changed Task authority: %+v %v", actual, err)
			}
		})
	}
}

func TestInterfaceStoppedTaskCannotBorrowDraftCoding(t *testing.T) {
	for _, mode := range []string{"stopped task", "paused ancestor"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			s := openAssignedStore(t, dir)
			task := assignedFixtureTask(t, s, "true")
			ancestor, err := plan.TaskApproach(s, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := plan.ReadinessSnapshot(s, ancestor.ID)
			if err != nil {
				t.Fatal(err)
			}
			root, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			ctx := plan.WithProject(t.Context(), root)
			if _, err = plan.ClaimTaskContext(ctx, s, task.ID, snapshot, "true"); err != nil {
				t.Fatal(err)
			}
			if _, err = plan.TransitionTaskContext(ctx, s, task.ID, plan.TaskStopped); err != nil {
				t.Fatal(err)
			}
			wantState, wantAttempt := plan.TaskStopped, 1
			if mode == "paused ancestor" {
				if _, err = plan.TransitionApproach(s, ancestor.ID, plan.ApproachPaused); err != nil {
					t.Fatal(err)
				}
				task, err = plan.CreateTaskContext(ctx, s, ancestor.ID, "queued draft", "authored draft")
				if err != nil {
					t.Fatal(err)
				}
				wantState, wantAttempt = plan.TaskQueued, 0
			}
			cfg := interfaceConfig(t, "structured")
			approvals := 0
			srv := interfaceFixture(t, "structured", []codingCall{{" BASH> ", map[string]any{"command": "touch marker"}}})
			r, _, err := New(Deps{Cfg: cfg, Store: s, Server: srv.URL, Model: "component", Window: 32768, MaxSteps: 1, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "stopped", Continuation: session.Continuation{Workflow: selectedFixtureScope(t, s, task.ID)}, AskYesNo: func(context.Context, string) bool { approvals++; return true }}, "preserve stopped work")
			if err != nil {
				t.Fatal(err)
			}
			r.Run(t.Context(), "preserve stopped work")
			actual, err := plan.GetTask(s, task.ID)
			if err != nil || actual.State != wantState || actual.Attempts != wantAttempt || approvals != 0 || r.Continuation.Assignment != nil {
				t.Fatalf("stopped work regained authority: %+v approvals=%d err=%v", actual, approvals, err)
			}
			if _, err := os.Stat("marker"); !os.IsNotExist(err) {
				t.Fatal("stopped scope published an effect")
			}
		})
	}
}

func TestInterfacePlanEntryNameDecorations(t *testing.T) {
	for _, name := range []string{"plan", " PLAN ", "plan>", "plan></plan>", "plan>\n</plan>"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			s := openAssignedStore(t, dir)
			cfg := interfaceConfig(t, "structured")
			srv := interfaceFixture(t, "structured", []codingCall{{name, map[string]any{}}})
			r, _, err := New(Deps{Cfg: cfg, Store: s, Server: srv.URL, Model: "component", MaxSteps: 1, Window: 32768, Session: &codingSink{dir: filepath.Join(dir, "sessions")}, SessionID: "entry", AskYesNo: func(context.Context, string) bool { return true }}, "enter the current question")
			if err != nil {
				t.Fatal(err)
			}
			r.Run(t.Context(), "enter the current question")
			if scope := r.Continuation.Workflow; scope == nil || scope.Kind != "root" || scope.ID != "" {
				t.Fatalf("unambiguous empty entry did not settle: %+v", scope)
			}
			if r.Continuation.Assignment != nil {
				t.Fatal("empty plan entry invented a claim")
			}
		})
	}
}

func TestInterfaceConditionalTemplatesBindLegalCalls(t *testing.T) {
	for _, phase := range []string{"another", "revision"} {
		t.Run(phase, func(t *testing.T) {
			f := newPlanningFixture(t)
			f.task()
			taskID := f.scope.ID
			if phase == "another" {
				if _, err := plan.SetTaskBlock(f.d.Store, taskID, "current", "target", "steps", "behavior", "tests", "review", "true", "test -d ."); err != nil {
					t.Fatal(err)
				}
				ids, err := launchScopeForTest(f)
				if err != nil {
					t.Fatal(err)
				}
				f.scope = &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "approach", ID: ids[0]}
			} else {
				ctx := plan.WithProject(t.Context(), f.d.Workspace)
				snapshot, _, err := plan.ContractSnapshot(ctx, f.d.Store, "task", taskID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = plan.RequestRevision(ctx, f.d.Store, "task", taskID, snapshot, "user", "fixture", "revise the title"); err != nil {
					t.Fatal(err)
				}
			}
			d := f.definition()
			schema := d.Schema()
			_, block, ok := strings.Cut(schema, "```json\n")
			if !ok {
				t.Fatalf("schema has no current call example: %s", schema)
			}
			block, _, _ = strings.Cut(block, "\n```")
			var example map[string]any
			if err := json.Unmarshal([]byte(block), &example); err != nil || example == nil {
				t.Fatalf("bad current template: %s %v", block, err)
			}
			for key, value := range example {
				if value, ok := value.(string); ok && strings.HasPrefix(value, "<") {
					example[key] = "concrete " + key + " for component evidence"
				}
			}
			raw, _ := json.Marshal(example)
			args, repair := tool.ValidateJSON(d, string(raw))
			if repair != "" {
				t.Fatal(repair)
			}
			bound, expanded, err := d.Bind(plan.WithProject(t.Context(), f.d.Workspace), args)
			if err != nil {
				t.Fatalf("current example cannot bind: %v", err)
			}
			if _, err = bound.Execute(plan.WithProject(t.Context(), f.d.Workspace), expanded); err != nil {
				t.Fatalf("current example does not reach the actual owner: %v", err)
			}
			if phase == "another" {
				tasks, err := plan.ApproachTasks(f.d.Store, f.scope.ID)
				if err != nil || len(tasks) != 2 || example["invoke"] != "TASK" {
					t.Fatalf("TASK example omitted authored task: %d %v", len(tasks), err)
				}
			} else {
				actual, err := plan.GetTask(f.d.Store, taskID)
				if err != nil || actual.Title != example["value"] || actual.Attempts != 0 {
					t.Fatalf("EDIT example omitted legal field/value: %+v %v", actual, err)
				}
			}
		})
	}
}
