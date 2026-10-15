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

	"gopkg.in/yaml.v3"
	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

// Existing run startup, real scoped GraphQL and durable Kuzu; no model is run.
func TestReferenceLibraryPublicStartupAndVersioning(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	s := openAssignedStore(t, dir)
	cfg := config.Defaults()
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Recall.TopN = 0
	cfg.Behavior.Path = ""
	build := func(cfg config.Config) *tool.Registry {
		t.Helper()
		r, _, err := New(Deps{Cfg: cfg, Store: s, Server: "http://unused", Model: "reference-control", SessionID: "reference-control", Window: 32768, In: strings.NewReader(""), Out: io.Discard}, "Inspect the project")
		if err != nil {
			t.Fatal(err)
		}
		if r.Memories != "" {
			t.Fatal("startup injected reference library")
		}
		return r.Registry
	}
	call := func(reg *tool.Registry, name string, supplied tool.Args) string {
		t.Helper()
		def, ok := reg.Lookup(name)
		if !ok {
			t.Fatal("missing public tool", name)
		}
		raw, _ := json.Marshal(supplied)
		args, repair := tool.ValidateJSON(def, string(raw))
		if repair != "" {
			t.Fatal(repair)
		}
		res, err := def.Execute(context.Background(), args)
		if err != nil || res.Failed {
			t.Fatal(res.Text, err)
		}
		return res.Text
	}
	read := func(reg *tool.Registry, selector string) (id, body, previous string) {
		t.Helper()
		text := call(reg, "graphql", tool.Args{"query": "query { knowledge(" + selector + ",limit:1) { id body previousId sessionRef } }"})
		var result struct {
			Data struct {
				Knowledge []struct{ ID, Body, PreviousID, SessionRef string }
			}
		}
		if err := json.Unmarshal([]byte(text), &result); err != nil || len(result.Data.Knowledge) != 1 {
			t.Fatalf("selected reference missing: %s %v", text, err)
		}
		e := result.Data.Knowledge[0]
		if e.SessionRef != "harness/reference" {
			t.Fatal("reference source not disclosed", e.SessionRef)
		}
		return e.ID, e.Body, e.PreviousID
	}
	tiny := cfg
	tiny.Tools.GraphQL.MaxRequestBytes = 64
	tiny.Tools.GraphQL.MaxRows = 1
	tiny.Tools.GraphQL.MaxResultBytes = 1200
	build(tiny)
	heads, err := memory.Knowledge(context.Background(), s, dir, "", "", 10, 0, false)
	if err != nil || len(heads) != 5 {
		t.Fatalf("small model budgets blocked host guide publication: %d %v", len(heads), err)
	}
	reg := build(cfg)
	for _, topic := range []string{"index", "pat", "workers", "scope", "tools"} {
		_, body, _ := read(reg, `key:"harness/reference/`+topic+`"`)
		helpTopic := "reference." + topic
		if topic == "index" {
			helpTopic = "reference"
		}
		text := call(reg, "help", tool.Args{"topic": helpTopic})
		if !strings.Contains(text, body) {
			t.Fatalf("help/graph canonical source differs at %s", topic)
		}
	}
	first, oldBody, _ := read(reg, `key:"harness/reference/pat"`)
	reg = build(cfg)
	unchanged, _, _ := read(reg, `key:"harness/reference/pat"`)
	if first != unchanged {
		t.Fatal("unchanged startup produced duplicate version")
	}
	var catalog struct {
		Texts map[string]string `yaml:"texts"`
	}
	if err := yaml.Unmarshal([]byte(prompts.Builtin().YAML()), &catalog); err != nil {
		t.Fatal(err)
	}
	catalog.Texts["help.reference.pat"] += "\nSelected canonical revision."
	raw, _ := yaml.Marshal(catalog)
	path := filepath.Join(dir, "selected-prompts.yaml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	pc, err := prompts.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.PromptCatalog = pc
	reg = build(cfg)
	changed, newBody, previous := read(reg, `key:"harness/reference/pat"`)
	if changed == first || previous != first || !strings.Contains(newBody, "Selected canonical revision.") {
		t.Fatal("selected YAML did not version owning head", changed, previous, newBody)
	}
	_, historical, _ := read(reg, `id:"`+first+`"`)
	if historical != oldBody {
		t.Fatal("old reference version was changed")
	}
	if plans, err := plan.ListPlans(s, ""); err != nil || len(plans) != 0 {
		t.Fatalf("reference startup authored PAT: %d %v", len(plans), err)
	}
}

// The existing host bindings author real PAT while schemas explain the current
// Q/A stage. No raw UUID choreography or worker/verification bypass is added.
func TestGuidedPlanningPublicQuestionSequence(t *testing.T) {
	f := newPlanningFixture(t)
	entry := f.definition()
	if len(entry.Params) != 0 || strings.TrimSpace(entry.Purpose) != "Call plan with {}; returns the first question." {
		t.Fatalf("entry competes with future questions: %s", entry.Schema())
	}
	_, example, found := strings.Cut(entry.CallTemplate(), "```json\n")
	example, _, _ = strings.Cut(example, "\n```")
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(example), &call); !found || err != nil || call.Name != "plan" || call.Arguments == nil || len(call.Arguments) != 0 {
		t.Fatalf("entry lacks its exact native empty call: %s error=%v", entry.CallTemplate(), err)
	}
	f.task()
	taskID := f.scope.ID
	f.call(tool.Args{"current": "observed source defect", "target": "requested behavior"})
	f.call(tool.Args{"acceptance": "observed distinguishing assertion", "tests": "unexecuted regression", "acceptance_command": "test 1 -eq 1"})
	for _, step := range []struct{ name, label, value string }{
		{"objective", "DRAFT SUBTASK", "repair responsible behavior"},
		{"execution", "DRAFT METHOD", "inspect and correct owning source"},
		{"validation", "DRAFT VALIDATION", "run distinguishing assertion"},
	} {
		def := f.definition()
		if len(def.Params) != 1 || def.Params[0].Name != step.name || !strings.Contains(def.Purpose, step.label) {
			t.Fatalf("current Q/A question mismatched: %s", def.Schema())
		}
		f.call(tool.Args{step.name: step.value})
	}
	if !strings.Contains(f.definition().Purpose, "DRAFT NEXT STEP") {
		t.Fatal("next-subtask question is unclear", f.definition().Schema())
	}
	f.call(tool.Args{"more": false})
	f.call(tool.Args{"verify": "independently inspect code and rerun proof"})
	actual, err := plan.GetTask(f.d.Store, taskID)
	if err != nil || actual.State != plan.TaskQueued || actual.Attempts != 0 || !strings.Contains(actual.Sketch, `"objective":"repair responsible behavior"`) || !strings.Contains(actual.Sketch, `"execution":"inspect and correct owning source"`) || !strings.Contains(actual.Sketch, `"validation":"run distinguishing assertion"`) {
		t.Fatal("questions did not author actual unexecuted task", actual, err)
	}
}

// Scripted HTTP only: actual run, current view, help receipts and next request.
// Reference bodies reach the model only after an explicit selected help call.
func TestReferenceSelectedNextWire(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s := openAssignedStore(t, dir)
	cfg := config.Defaults()
	cfg.Model.Runtime, cfg.Model.ToolProtocol = "openai", "structured"
	cfg.Auth.Mode = config.AuthYOLO
	cfg.Behavior.Path = ""
	bindDecisionFixture(t, &cfg, nil)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string }
			Tools    []struct{ Function struct{ Name string } }
		}
		if err := json.NewDecoder(q.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		combined := ""
		for _, m := range request.Messages {
			combined += m.Content + "\n"
		}
		for _, d := range request.Tools {
			if d.Function.Name == "launch" {
				t.Error("unavailable launcher was registered through documentation")
			}
		}
		if requests == 0 && strings.Contains(combined, "## Reference:") {
			t.Error("startup injected reference document bodies")
		}
		if requests == 1 && (!strings.Contains(combined, prompts.Builtin().Text("help.reference.workers")) || strings.Contains(combined, "## Reference: PAT lifecycle")) {
			t.Error("selected help did not deliver exactly its document")
		}
		if requests == 2 && (!strings.Contains(combined, "## help.launch") || !strings.Contains(combined, "unavailable in the current tool view")) {
			t.Error("unavailable capability documentation did not reach the next request")
		}
		topic := "reference.workers"
		if requests == 1 {
			topic = "launch"
		} else if requests == 2 {
			topic = "reference.scope"
		}
		args, _ := json.Marshal(map[string]string{"topic": topic})
		delta := map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("reference-%d", requests), "type": "function", "function": map[string]string{"name": "help", "arguments": string(args)}}}}
		requests++
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": "tool_calls"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	defer srv.Close()
	r, _, err := New(Deps{Cfg: cfg, Store: s, Server: srv.URL, Model: "reference-wire-control", SessionID: "reference-wire", Window: 65536, MaxSteps: 3, In: strings.NewReader(""), Out: io.Discard}, "Inspect selected operating references")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Run(context.Background(), "Inspect selected operating references"); err != nil || requests != 3 {
		t.Fatalf("actual selected reference requests=%d error=%v", requests, err)
	}
}
