package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/launch"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Scripted HTTP/SSE only; actual factory/view/tool execution and durable sink.
func TestModelHelpNextWire(t *testing.T) {
	for _, protocol := range []string{"structured", "text"} {
		t.Run(protocol, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			s := openAssignedStore(t, dir)
			cfg := config.Defaults()
			cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", protocol
			cfg.Auth.Mode = config.AuthYOLO
			cfg.Recall.TopN = 0
			cfg.Behavior.Path = ""
			cfg.Templates.Path = assignedTemplates()
			bindDecisionFixture(t, &cfg, nil)
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				var body struct {
					Messages []struct{ Role, Content string }
					Tools    []struct{ Function struct{ Name string } }
				}
				if e := json.NewDecoder(q.Body).Decode(&body); e != nil {
					t.Error(e)
				}
				combined := ""
				for _, m := range body.Messages {
					combined += m.Content + "\n"
				}
				if requests == 0 && strings.Contains(combined, "# Harness operating guide") {
					t.Error("guide automatically injected")
				}
				if requests == 1 && (!strings.Contains(combined, "## help.read") || strings.Contains(combined, "## help.edit") || strings.Contains(combined, "# Harness operating guide")) {
					t.Error("focused material did not reach exact next request")
				}
				if requests == 2 && (!strings.Contains(combined, "# Harness operating guide") || !strings.Contains(combined, "## help.find_dir") || !strings.Contains(combined, "## help.edit") || strings.Contains(combined, "## help.find\n")) {
					t.Error("full material did not follow available view")
				}
				for _, def := range body.Tools {
					if def.Function.Name == "find" || def.Function.Name == "task_start" {
						t.Errorf("ordinary core advertised legacy/planned-only %s", def.Function.Name)
					}
				}
				name, args := "help", map[string]string{"topic": "read"}
				if requests == 1 {
					args = map[string]string{"topic": "guide"}
				}
				if requests == 2 {
					name = "read"
					args = map[string]string{"path": "rows.txt"}
				}
				raw, _ := json.Marshal(args)
				delta := map[string]any{}
				reason := "stop"
				if protocol == "structured" {
					delta["tool_calls"] = []any{map[string]any{"index": 0, "id": fmt.Sprintf("help-%d", requests), "type": "function", "function": map[string]string{"name": name, "arguments": string(raw)}}}
					reason = "tool_calls"
				} else {
					content := "```tool\ntool: " + name + "\n"
					for k, v := range args {
						content += k + ": " + v + "\n"
					}
					delta["content"] = content + "```"
				}
				requests++
				b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
			}))
			defer srv.Close()
			var rows strings.Builder
			for n := 1; n <= 2001; n++ {
				fmt.Fprintf(&rows, "ROW_%04d\n", n)
			}
			if e := os.WriteFile("rows.txt", []byte(rows.String()), 0600); e != nil {
				t.Fatal(e)
			}
			sink := &codingSink{dir: filepath.Join(dir, "sessions")}
			// Isolate help delivery from unknown-usage compaction pressure; dedicated context controls test that boundary.
			r, _, e := New(Deps{Cfg: cfg, Store: s, Server: srv.URL, Model: "model-help-fixture", Session: sink, SessionID: "help", MaxSteps: 3, Window: 65536, In: strings.NewReader(""), Out: io.Discard}, "Inspect the source and documentation")
			if e != nil {
				t.Fatal(e)
			}
			_, _, e = r.Run(context.Background(), "Inspect the source and documentation")
			if e != nil || requests != 3 {
				t.Fatalf("requests%d error%v", requests, e)
			}
			events, e := session.Read(sink.dir, "help")
			if e != nil {
				t.Fatal(e)
			}
			seen := false
			for _, ev := range events {
				text := ev.Text
				if ev.Kind == "native_result" {
					var entry session.Entry
					if err := json.Unmarshal([]byte(ev.Text), &entry); err != nil {
						t.Fatal(err)
					}
					text = entry.Message.Content
				}
				if (ev.Kind == "result" || ev.Kind == "native_result") && strings.Contains(text, "ROW_2000\n") {
					seen = true
					if strings.Contains(text, "ROW_2001\n") || !strings.Contains(text, "next_offset=2001") {
						t.Fatal("read default/cursor changed")
					}
				}
			}
			if !seen {
				t.Fatal("actual default first read not archived")
			}
		})
	}
}

func TestHelpFullLegacyAndApproval(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s := openAssignedStore(t, dir)
	os.WriteFile("owned.txt", []byte("needle\n"), 0600)
	cfg := config.Defaults()
	cfg.Recall.TopN = 0
	cfg.Behavior.Path = ""
	cfg.Auth.Mode = config.AuthAll
	bindDecisionFixture(t, &cfg, nil)
	approvals := 0
	makeRun := func(view string) *tool.Registry {
		r, _, e := New(Deps{Tools: view, Cfg: cfg, Store: s, Server: "http://unused", Model: "fixture", MaxSteps: 1, Window: 32768, In: strings.NewReader(""), Out: io.Discard, AskYesNo: func(context.Context, string) bool { approvals++; return true }}, "inspect")
		if e != nil {
			t.Fatal(e)
		}
		def, ok := r.Registry.Lookup("help")
		if !ok {
			t.Fatal("missing help")
		}
		a, repair := tool.ValidateJSON(def, `{"topic":"read"}`)
		if repair != "" {
			t.Fatal(repair)
		}
		if _, e = r.Exec(context.Background(), def, a); e != nil {
			t.Fatal(e)
		}
		return r.Registry
	}
	standard := makeRun("")
	if _, ok := standard.Lookup("find"); ok {
		t.Fatal("legacy alias advertised in standard view")
	}
	full := makeRun("full")
	d, ok := full.ResolveName("find")
	if !ok || d.Name != "find_file" {
		t.Fatal("explicit full lost canonical compatibility finder")
	}
	for _, name := range full.Names() {
		if name == "find" {
			t.Fatal("explicit full advertises redundant finder schema")
		}
	}
	a, repair := tool.ValidateJSON(d, `{"pattern":"owned.txt"}`)
	if repair != "" {
		t.Fatal(repair)
	}
	res, e := d.Execute(context.Background(), a)
	if e != nil || !strings.Contains(res.Text, "owned.txt") {
		t.Fatalf("legacy finder %+v %v", res, e)
	}
	d, _ = full.Lookup("help")
	a, _ = tool.ValidateJSON(d, `{"topic":"guide"}`)
	res, e = d.Execute(context.Background(), a)
	if e != nil || !strings.Contains(res.Text, "## help.find_file\n") || !strings.Contains(res.Text, "tool: workflow_focus") || !strings.Contains(res.Text, "tool: task_start") {
		t.Fatalf("incomplete full guide %v", e)
	}
	if approvals != 2 {
		t.Fatalf("authorize-all help bypass: %d", approvals)
	}
	deps, e := LaunchDeps(cfg, s, "http://unused", "fixture", 1, 32768, strings.NewReader(""), io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	child := deps.NewRegistry()
	if _, ok = child.Lookup("find"); ok {
		t.Fatal("standard child duplicates legacy finder")
	}
	for _, name := range []string{"find_file", "find_dir", "help", "fetch"} {
		if _, ok = child.Lookup(name); !ok {
			t.Fatalf("child lost %s", name)
		}
	}
}

// Actual reviewer registration, Bash checks and owning qualification; model is scripted HTTP.
func TestChildHelpSeesLateAssignedCheck(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s := openAssignedStore(t, dir)
	cfg := config.Defaults()
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	cfg.Behavior.Path = ""
	cfg.Templates.Path = assignedTemplates()
	// This is test instrumentation, not a project file that the reviewer may edit.
	checkMarker := filepath.Join(t.TempDir(), "checks")
	command := fmt.Sprintf(`test "$(cat artifact)" = actual; printf checked >> %q`, checkMarker)
	task := assignedFixtureTask(t, s, command, `test "$(cat artifact)" = actual`)
	bindDecisionFixture(t, &cfg, nil)
	worker, reviewer := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		var body struct {
			Messages []struct{ Role, Content string }
			Tools    []struct{ Function struct{ Name string } }
		}
		if e := json.NewDecoder(q.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		review := false
		combined := ""
		for _, m := range body.Messages {
			review = review || strings.HasPrefix(m.Content, "Review ")
			combined += m.Content + "\n"
		}
		delta := map[string]any{"content": "Done"}
		reason := "stop"
		call := func(name string, args string) {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("child-%t-%d", review, worker+reviewer), "type": "function", "function": map[string]string{"name": name, "arguments": args}}}}
			reason = "tool_calls"
		}
		if review {
			hasCheck := false
			for _, d := range body.Tools {
				hasCheck = hasCheck || d.Function.Name == "check"
			}
			if !hasCheck {
				t.Error("late reviewer check hidden")
			}
			if reviewer == 0 {
				call("help", `{"topic":"check"}`)
			} else if reviewer == 1 {
				if !strings.Contains(combined, "## help.check") || !strings.Contains(combined, "[default: "+command+"]") {
					t.Error("child help captured stale registry/check")
				}
				call("check", `{}`)
			} else {
				delta["content"] = "PASS: current declared check passed"
			}
			reviewer++
		} else {
			if worker == 0 {
				call("bash", `{"cmd":"printf actual > artifact"}`)
			}
			worker++
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	defer srv.Close()
	deps, e := LaunchDeps(cfg, s, srv.URL, "child-help-fixture", 4, 32768, strings.NewReader(""), io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	approach, e := plan.TaskApproach(s, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	result, e := launch.LaunchChecked(context.Background(), deps, approach.ID, "")
	if e != nil || len(result) != 1 || !result[0].Qualified() {
		t.Fatalf("actual child settlement %+v %v", result, e)
	}
	actual, e := plan.GetTask(s, task.ID)
	if e != nil || !actual.Qualified() || actual.Attempts != 1 {
		t.Fatalf("owning qualification %+v %v", actual, e)
	}
	checked, e := os.ReadFile(checkMarker)
	if e != nil || string(checked) != "checkedchecked" || worker != 2 || reviewer != 3 {
		t.Fatalf("actual check/review worker%d reviewer%d checks%q error%v", worker, reviewer, checked, e)
	}
}
