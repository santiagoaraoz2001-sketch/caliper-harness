package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

type codingSink struct {
	dir      string
	fail     bool
	failOnce bool
	before   func(string)
}

func (s *codingSink) Append(_ context.Context, id, kind, text string) error {
	if kind == "coding" {
		var selected session.CodingState
		_ = json.Unmarshal([]byte(text), &selected)
		if selected.Receipt == nil {
			return session.Append(s.dir, id, session.Event{Kind: kind, Text: text})
		}
		if s.before != nil {
			s.before(text)
		}
		if s.fail || s.failOnce {
			s.failOnce = false
			return fmt.Errorf("owned coding record failure")
		}
	}
	return session.Append(s.dir, id, session.Event{Kind: kind, Text: text})
}

type codingCall struct {
	name string
	args map[string]any
}

func codingFixture(t *testing.T, protocol string, calls []codingCall, firstBatch ...int) *httptest.Server {
	t.Helper()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("wrong fixture origin/path %s", r.URL.Path)
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] != true {
			t.Error("tool request not streamed")
		}
		delta := map[string]any{"content": "PASS complete"}
		reason := "stop"
		if n < len(calls) {
			c := calls[n]
			if protocol == "text" {
				text := "```tool\ntool: " + c.name + "\n"
				for k, v := range c.args {
					if str, ok := v.(string); ok && str != strings.TrimSpace(str) {
						text += fmt.Sprintf("%s: <<ARG_END\n%s\nARG_END\n", k, str)
					} else {
						text += fmt.Sprintf("%s: %v\n", k, v)
					}
				}
				delta["content"] = text + "```"
			} else {
				args, _ := json.Marshal(c.args)
				delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint("call", n), "type": "function", "function": map[string]string{"name": c.name, "arguments": string(args)}}}}
				reason = "tool_calls"
			}
		}
		if protocol == "structured" && n == 0 && len(firstBatch) > 0 {
			group := []any{}
			for i := 0; i < firstBatch[0]; i++ {
				raw, _ := json.Marshal(calls[i].args)
				group = append(group, map[string]any{"index": i, "id": fmt.Sprint("call", i), "type": "function", "function": map[string]string{"name": calls[i].name, "arguments": string(raw)}})
			}
			delta = map[string]any{"tool_calls": group}
			n += firstBatch[0] - 1
		}
		if protocol == "text" {
			reason = "stop"
		}
		n++
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	}))
	t.Cleanup(srv.Close)
	return srv
}
func TestConfiguredCheckAuthAndDurableReceipt(t *testing.T) {
	for _, protocol := range []string{"structured", "text", "proposed"} {
		t.Run(protocol, func(t *testing.T) {
			proposed := protocol == "proposed"
			if proposed {
				protocol = "structured"
			}
			dir := t.TempDir()
			t.Chdir(dir)
			cfg := config.Defaults()
			effective := "printf checked > marker"
			cfg.Coding.CheckCommand = effective
			callArgs := map[string]any{"timeout": 2}
			if proposed {
				cfg.Coding.CheckCommand = ""
				callArgs["cmd"] = effective
			}
			cfg.Recall.TopN = 0
			cfg.Model.ToolProtocol = protocol
			srv := codingFixture(t, protocol, []codingCall{{"check", callArgs}})
			cfg.Model.Server = srv.URL
			sink := &codingSink{dir: filepath.Join(dir, "sessions")}
			recorded := 0
			sink.before = func(text string) {
				var c session.CodingState
				if e := json.Unmarshal([]byte(text), &c); e != nil {
					t.Fatal(e)
				}
				if recorded == 0 {
					if c.Command != effective || c.Receipt.Status != "unexecuted" || c.Receipt.Acceptance == nil || c.Receipt.Acceptance.Status != "unexecuted" || c.Receipt.EpisodeStarted == "" {
						t.Fatalf("selection %+v", c)
					}
					if _, e := os.Stat("marker"); !os.IsNotExist(e) {
						t.Fatal("effect before durable selection")
					}
				}
				recorded++
			}
			approved := false
			owned, storeErr := store.Open(filepath.Join(t.TempDir(), "db"))
			if storeErr != nil {
				t.Fatal(storeErr)
			}
			defer owned.Close()
			r, _, e := assignedCodingNew(t, Deps{Store: owned, Cfg: cfg, Server: cfg.Model.Server, Model: "fixture", MaxSteps: 3, Window: 32768, Session: sink, SessionID: "coding", AskYesNo: func(_ context.Context, p string) bool {
				_, body, complete := strings.Cut(p, "Complete validated action (JSON):\n")
				var action struct {
					Tool string         `json:"tool"`
					Args map[string]any `json:"args"`
				}
				dec := json.NewDecoder(strings.NewReader(body))
				dec.UseNumber()
				approved = complete && dec.Decode(&action) == nil && action.Tool == "check" && action.Args["cmd"] == effective && action.Args["timeout"] == json.Number("2")
				return approved
			}}, "owned check", effective)
			if e != nil {
				t.Fatal(e)
			}
			_, _, e = r.Run(context.Background(), "owned check")
			if e != nil || r.Outcome.Status != outcome.Completed || !r.Continuation.Coding.Passed() || !approved || recorded < 2 {
				t.Fatalf("settlement %+v %+v approved%t records%d error%v", r.Outcome, r.Continuation.Coding, approved, recorded, e)
			}
			b, _ := os.ReadFile("marker")
			if string(b) != "checked" {
				t.Fatalf("actual check %q", b)
			}
			events, e := session.Read(sink.dir, "coding")
			if e != nil {
				t.Fatal(e)
			}
			restored, _, e := session.Restore(events, 32768)
			if e != nil || restored.Coding.Passed() {
				t.Fatalf("restore qualified old pass %+v %v", restored.Coding, e)
			}
		})
	}
}
func TestCodingChecksRefuseAndInvalidateTruthfully(t *testing.T) {
	for _, scenario := range []string{"exit9", "later bash", "later launch", "later workflow_resume", "record failure", "empty configured", "empty configured text", "case configured", "case configured text", "space configured", "space configured text", "wrong configured", "hidden workflow", "failed workflow", "record once then pass"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			cfg := config.Defaults()
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Recall.TopN = 0
			cfg.Coding.CheckCommand = "printf checked > marker"
			calls := []codingCall{{"check", map[string]any{}}}
			want := outcome.Stopped
			view := ""
			switch scenario {
			case "record once then pass":
				calls = append([]codingCall{{"write", map[string]any{"path": "artifact", "content": "actual"}}}, calls...)
				want = outcome.Failed
			case "exit9":
				cfg.Coding.CheckCommand = "exit 9"
				want = outcome.Failed
			case "later bash":
				calls = append(calls, codingCall{"bash", map[string]any{"cmd": "printf changed > changed-source"}})
			case "later launch", "later workflow_resume":
				calls = append(calls, codingCall{strings.TrimPrefix(scenario, "later "), map[string]any{"approach": "owned"}})
				view = "full"
			case "record failure":
				want = outcome.Failed
			case "empty configured", "empty configured text":
				calls[0].args = map[string]any{"cmd": ""}
			case "case configured", "case configured text":
				cfg.Coding.CheckCommand = "true"
				calls[0].args = map[string]any{"cmd": "TRUE"}
			case "space configured", "space configured text":
				cfg.Coding.CheckCommand = "true"
				calls[0].args = map[string]any{"cmd": " true "}
			case "wrong configured":
				calls[0].args = map[string]any{"cmd": "touch forbidden"}
			case "hidden workflow":
				calls = []codingCall{{"launch", map[string]any{}}}
			case "failed workflow":
				calls = append(calls, codingCall{"launch", map[string]any{}})
				want = outcome.Failed
				view = "full"
			}
			protocol := "structured"
			authCalls := 0
			if strings.Contains(scenario, "configured") {
				cfg.Auth.Mode = config.AuthAll
			}
			if strings.HasSuffix(scenario, "configured text") {
				protocol = "text"
			}
			cfg.Model.ToolProtocol = protocol
			srv := codingFixture(t, protocol, calls)
			cfg.Model.Server = srv.URL
			sink := &codingSink{dir: filepath.Join(dir, "sessions"), fail: scenario == "record failure", failOnce: scenario == "record once then pass"}
			owned, storeErr := store.Open(filepath.Join(t.TempDir(), "db"))
			if storeErr != nil {
				t.Fatal(storeErr)
			}
			defer owned.Close()
			if scenario == "later launch" || scenario == "later workflow_resume" || scenario == "failed workflow" {
				p, err := plan.CreatePlan(owned, "owned parent control", "preserve current check truth")
				if err != nil {
					t.Fatal(err)
				}
				a, err := plan.CreateApproach(owned, p.ID, "actual owning reference")
				if err != nil {
					t.Fatal(err)
				}
				// Draft is deliberate: the real failed-workflow route must refuse readiness.
				calls[len(calls)-1].args["approach"] = a.ID
			}
			deps := Deps{Store: owned, Cfg: cfg, AskYesNo: func(context.Context, string) bool { authCalls++; return true }, Tools: view, Server: cfg.Model.Server, Model: "fixture", MaxSteps: 4, Window: 32768, Session: sink, SessionID: "coding"}
			var r *agent.Runner
			var e error
			if view == "full" {
				// An explicit full-tool parent owns child dispatch; a selected claimed task
				// intentionally cannot acquire another approach's launch schema.
				bindDecisionFixture(t, &deps.Cfg, nil)
				r, _, e = New(deps, "owned control")
			} else {
				r, _, e = assignedCodingNew(t, deps, "owned control")
			}
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "later launch" || scenario == "later workflow_resume" {
				// Controlled owning-report double isolates the parent wrapper; the child effect is real registered Bash.
				name := strings.TrimPrefix(scenario, "later ")
				d, _ := r.Registry.Lookup(name)
				bash, _ := r.Registry.Lookup("bash")
				d.Execute = func(ctx context.Context, a tool.Args) (tool.Result, error) {
					res, err := agent.Direct(ctx, bash, tool.Args{"cmd": "printf child > child-marker", "timeout": int64(2)})
					res.Workflow = &outcome.Report{Ref: a["approach"].(string), Qualified: err == nil && !res.Failed, Outcome: outcome.Outcome{Status: outcome.Completed}}
					return res, err
				}
				reg := tool.NewRegistry()
				for _, key := range r.Registry.Names() {
					item, _ := r.Registry.Lookup(key)
					if key == name {
						item = d
					}
					reg.Register(item)
				}
				// Claim refreshes the actual view; install the same controlled report
				// in that refreshed view, retaining its current check/authority wiring.
				originalView := r.WorkflowView
				r.WorkflowView = func(scope *session.WorkflowScope) (*tool.Registry, string, error) {
					view, header, e := originalView(scope)
					if e != nil {
						return view, header, e
					}
					replacement := tool.NewRegistry()
					for _, key := range view.Names() {
						item, _ := view.Lookup(key)
						if key == name {
							item.Execute = d.Execute
						}
						replacement.Register(item)
					}
					return replacement, header, nil
				}
				r.Registry = reg
			}
			_, _, e = r.Run(context.Background(), "owned control")
			hostExpected := scenario == "later bash" || scenario == "later launch" || scenario == "later workflow_resume" || scenario == "hidden workflow" || strings.Contains(scenario, "configured")
			if hostExpected {
				want = outcome.Completed
			}
			if scenario == "later launch" || scenario == "later workflow_resume" {
				b, err := os.ReadFile("child-marker")
				t.Logf("actual child Bash bytes=%q error=%v parent current=%v", b, err, r.Continuation.Coding.Passed())
			}
			if r.Outcome.Status != want || (r.Outcome.ExitCode() == 0) != (want == outcome.Completed) {
				t.Fatalf("status %+v want%v err%v", r.Outcome, want, e)
			}
			if strings.Contains(scenario, "configured") && authCalls != 1 {
				t.Fatal("expected only the separately announced exact configured host check approval")
			}
			if hostExpected {
				events, err := session.Read(sink.dir, "coding")
				if err != nil {
					t.Fatal(err)
				}
				host := 0
				for _, event := range events {
					if event.Kind == "host_check" {
						host++
					}
				}
				if host != 1 || !r.Continuation.Coding.Passed() || r.Continuation.Coding.Receipt.Command != cfg.Coding.CheckCommand {
					t.Fatalf("D18 fresh actual configured host receipt missing: host=%d %+v", host, r.Continuation.Coding)
				}
			}
			if scenario == "record failure" {
				if _, e := os.Stat("marker"); !os.IsNotExist(e) {
					t.Fatal("refused effect")
				}
			}
			if scenario == "later launch" || scenario == "later workflow_resume" {
				b, err := os.ReadFile("child-marker")
				if err != nil || string(b) != "child" || !r.Continuation.Coding.Passed() {
					t.Fatalf("parent fresh host receipt after actual child effect %q %v %+v", b, err, r.Continuation.Coding)
				}
			}
			if scenario == "record once then pass" {
				b, _ := os.ReadFile("artifact")
				if string(b) != "actual" || r.Continuation.Coding.Passed() {

					t.Fatalf("actual preserved mutation/latched recording failure prevents later check %q %+v", b, r.Continuation.Coding)
				}
			}
			if scenario == "failed workflow" && !strings.Contains(r.Outcome.Reason, "workflow") {
				t.Fatalf("check masked workflow failure %+v", r.Outcome)
			}
		})
	}
}
