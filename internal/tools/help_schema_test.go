package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"specific-harness/internal/config"
	api "specific-harness/internal/graph"
	"specific-harness/internal/graph/generated"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	graphTool "specific-harness/internal/tools/graph"
)

func nativeHelpTemplate(t *testing.T, text string) struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
} {
	t.Helper()
	_, tail, ok := strings.Cut(text, "```json\n")
	if !ok {
		t.Fatalf("missing callable template: %s", text)
	}
	block, _, _ := strings.Cut(tail, "\n```")
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(block), &call.Arguments); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "Call ") && strings.Contains(line, " with these argument fields directly") {
			call.Name = strings.Fields(line)[1]
			break
		}
	}
	if call.Name == "" || call.Arguments == nil {
		t.Fatal("missing tool name or flat arguments", text)
	}
	return call
}

func TestHelpSchemaIndexAndFocusedTemplate(t *testing.T) {
	reg, _ := catalogTools(t, prompts.Builtin())
	active := reg.View([]string{"help", "read", "graphql"})
	h, _ := active.Lookup("help")
	// A fresh help definition models the active request view, not the full registry.
	h = helpDefinition(config.Defaults(), reg, prompts.Builtin(), func() *tool.Registry { return active })
	index, err := h.Execute(context.Background(), tool.Args{"topic": "schema"})
	if err != nil || !strings.Contains(index.Text, "read") || !strings.Contains(index.Text, "graphql") || strings.Contains(index.Text, "\n- edit") || strings.Contains(index.Text, "## help.graphql") {
		t.Fatalf("current compact index: %s %v", index.Text, err)
	}
	compact, err := h.Execute(context.Background(), tool.Args{"topic": "schema.read"})
	if err != nil {
		t.Fatal(err)
	}
	call := nativeHelpTemplate(t, compact.Text)
	if call.Name != "read" || call.Arguments["path"] == nil || call.Arguments["input"] != nil || strings.Contains(compact.Text, "## help.read") {
		t.Fatalf("not an isolated flat tool template: %s", compact.Text)
	}
	if !strings.Contains(compact.Text, `"path": "<path>"`) || strings.Contains(compact.Text, `\u003c`) {
		t.Fatalf("model template has unreadable placeholders: %s", compact.Text)
	}
	full, err := h.Execute(context.Background(), tool.Args{"topic": "read"})
	if err != nil || len(compact.Text) >= len(full.Text) || !strings.Contains(full.Text, "## help.read") {
		t.Fatalf("compact schema and help conflated: %s %v", full.Text, err)
	}
	missing, err := h.Execute(context.Background(), tool.Args{"topic": "schema.edit"})
	if err != nil || !strings.Contains(missing.Text, "unavailable in the current tool view") || strings.Contains(missing.Text, "```json") {
		t.Fatalf("unavailable tool acquired a callable schema: %s %v", missing.Text, err)
	}
	_, err = h.Execute(context.Background(), tool.Args{"topic": "schema.reed"})
	if err == nil || !strings.Contains(err.Error(), "schema.read") {
		t.Fatalf("unknown tool recovery missed actual nearest route: %v", err)
	}
	overview, err := h.Execute(context.Background(), tool.Args{})
	if err != nil || !strings.Contains(overview.Text, "# Harness operating guide") || !strings.Contains(overview.Text, "schema.graphql") || strings.Contains(overview.Text, "## help.read") || strings.Contains(overview.Text, "## help.graphql") {
		t.Fatalf("default injected full tool guides: %s %v", overview.Text, err)
	}
}

func TestBashHelpMatchesBoundWorkspaceAndArguments(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "workspace marker.txt"), []byte("captured workspace"), 0600); err != nil {
		t.Fatal(err)
	}
	pc, cfg := prompts.Builtin(), config.Defaults()
	d := bash.NewAt(cfg.Tools.Bash, root, pc)
	for _, text := range []string{d.Purpose, pc.Text("help.bash")} {
		for _, want := range []string{"cmd", "timeout", "workspace", "cwd"} {
			if !strings.Contains(text, want) {
				t.Errorf("Bash reference omits %s: %s", want, text)
			}
		}
	}
	if len(d.Params) != 2 || d.Params[0].Name != "cmd" || d.Params[1].Name != "timeout" {
		t.Fatalf("Bash argument contract changed: %+v", d.Params)
	}
	if _, repair := tool.ValidateJSON(d, `{"cmd":"pwd","cwd":"/"}`); repair == "" {
		t.Fatal("reference claims cwd is unsupported but actual validator accepted it")
	}
	args, repair := tool.ValidateJSON(d, `{"cmd":"cat 'workspace marker.txt'"}`)
	if repair != "" {
		t.Fatal(repair)
	}
	res, err := d.Execute(context.Background(), args)
	if err != nil || res.Failed || res.Text != "captured workspace" {
		t.Fatalf("relative path did not resolve in the advertised workspace: %+v %v", res, err)
	}
}

func TestHelpGraphQLIndexShowsOperationAndRequiredArguments(t *testing.T) {
	reg, cfg := catalogTools(t, prompts.Builtin())
	h := helpDefinition(cfg, reg, prompts.Builtin())
	res, err := h.Execute(context.Background(), tool.Args{"topic": "schema.graphql"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range api.ModelFields() {
		t.Run(name, func(t *testing.T) {
			root, field := api.ModelOperation(name)
			var line string
			for _, candidate := range strings.Split(res.Text, "\n") {
				if strings.HasPrefix(candidate, "- "+name+" — ") {
					line = candidate
					break
				}
			}
			prefix := "- " + name + " — " + strings.ToLower(root.Name) + " " + name
			if !strings.HasPrefix(line, prefix) {
				t.Fatalf("index does not distinguish the operation: %q, want prefix %q", line, prefix)
			}
			signature, _, _ := strings.Cut(strings.TrimPrefix(line, prefix), " — ")
			for _, arg := range field.Arguments {
				want := arg.Type.NonNull && arg.DefaultValue == nil
				got := strings.Contains(signature, arg.Name+": "+arg.Type.String())
				if got != want {
					t.Errorf("argument %s present=%t required=%t in %q", arg.Name, got, want, signature)
				}
			}
		})
	}
	for _, want := range []string{
		"- noticed — mutation noticed(approachId: ID!",
		"- notices — query notices(approachId: ID!)",
	} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("observation write/read distinction missing: %s", want)
		}
	}
}

func TestHelpGraphQLTemplatesMatchExecutableSDL(t *testing.T) {
	reg, cfg := catalogTools(t, prompts.Builtin())
	h := helpDefinition(cfg, reg, prompts.Builtin())
	schema := generated.NewExecutableSchema(generated.Config{}).Schema()
	for _, root := range []*ast.Definition{schema.Query, schema.Mutation} {
		for _, f := range root.Fields {
			switch f.Name {
			case "sessions", "session", "addMemory", "addSessionEvent":
				continue // These host operations have no model-call template.
			}
			res, err := h.Execute(context.Background(), tool.Args{"topic": "schema.graphql." + f.Name})
			if err != nil {
				t.Fatal(err)
			}
			call := nativeHelpTemplate(t, res.Text)
			if _, errs := gqlparser.LoadQuery(schema, call.Arguments["query"].(string)); len(errs) != 0 {
				t.Fatalf("template %s disagrees with executable SDL: %v", f.Name, errs)
			}
		}
	}
}

func TestHelpSchemaReflectsCurrentDefinition(t *testing.T) {
	reg, cfg := catalogTools(t, prompts.Builtin())
	active := tool.NewRegistry()
	h := helpDefinition(cfg, reg, prompts.Builtin(), func() *tool.Registry { return active })
	for _, current := range []string{"first observed objective", "next observed proof"} {
		active = tool.NewRegistry()
		active.Register(tool.Definition{Name: "plan", Desc: "answer the current question", Params: []tool.Param{{Name: "answer", Type: tool.TypeString, Required: true, Desc: current}}})
		active.Register(tool.Definition{Name: "check", Params: []tool.Param{{Name: "cmd", Type: tool.TypeString, Default: "npm test"}, {Name: "acceptance_cmd", Type: tool.TypeString, Default: "node --test behavior.test.js"}}})
		plan, err := h.Execute(context.Background(), tool.Args{"topic": "schema.plan"})
		if err != nil || !strings.Contains(plan.Text, current) || nativeHelpTemplate(t, plan.Text).Arguments["answer"] == nil {
			t.Fatalf("stale guided question: %s %v", plan.Text, err)
		}
		check, err := h.Execute(context.Background(), tool.Args{"topic": "schema.check"})
		if err != nil || !strings.Contains(check.Text, "npm test") || !strings.Contains(check.Text, "node --test behavior.test.js") || len(nativeHelpTemplate(t, check.Text).Arguments) != 0 {
			t.Fatalf("lost assigned defaults/minimal check invocation: %s %v", check.Text, err)
		}
	}
}

func TestHelpGraphQLSchemaTemplatesAndRecovery(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	root := t.TempDir()
	d, err := graphTool.New(api.New(s, "test", t.TempDir()), cfg.Tools.GraphQL, root, "help-template", s.Identity())
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	reg.Register(d)
	reg.Register(helpDefinition(cfg, reg, prompts.Builtin()))
	index, err := helpCall(t, reg, "schema.graphql")
	if err != nil || !strings.Contains(index.Text, "appendKnowledge") || strings.Contains(index.Text, "putKnowledge") || strings.Contains(index.Text, "addMemory") || strings.Contains(index.Text, "addSessionEvent") {
		t.Fatalf("model SDL index: %s %v", index.Text, err)
	}
	compact, err := helpCall(t, reg, "schema.graphql.appendKnowledge")
	if err != nil || !strings.Contains(compact.Text, "expectedPreviousId: ID!") || strings.Contains(compact.Text, "input:") || strings.Contains(compact.Text, "prepareTask") {
		t.Fatalf("selected flat owning signature: %s %v", compact.Text, err)
	}
	call := nativeHelpTemplate(t, compact.Text)
	if call.Name != "graphql" || len(call.Arguments) != 1 {
		t.Fatalf("unexpected callable envelope: %+v", call)
	}
	query := call.Arguments["query"].(string)
	query = strings.NewReplacer("<key>", "product/decisions", "<body>", "20x20 dark", "<expectedPreviousId>", "").Replace(query)
	call.Arguments["query"] = query
	raw, _ := json.Marshal(call.Arguments)
	args, repair := tool.ValidateJSON(d, string(raw))
	if repair != "" {
		t.Fatal(repair)
	}
	result, err := d.Execute(context.Background(), args)
	if err != nil || result.Failed || !strings.Contains(result.Text, `"field":"appendKnowledge"`) || !strings.Contains(result.Text, `"status":"applied"`) {
		t.Fatalf("generated template does not execute owning append: %s %v", result.Text, err)
	}
	_, err = d.Execute(context.Background(), tool.Args{"query": `mutation { appendKnowledge(input:{key:"wrong",body:"no"}) {id} }`})
	if err == nil || !strings.Contains(err.Error(), "schema.graphql.appendKnowledge") {
		t.Fatalf("actual malformed GraphQL lacks selected recovery: %v", err)
	}
	_, err = helpCall(t, reg, "schema.graphql.appendKnowlege")
	if err == nil || !strings.Contains(err.Error(), "schema.graphql.appendKnowledge") {
		t.Fatalf("unknown owning operation lacks nearest actual reference: %v", err)
	}
	alias, err := helpCall(t, reg, "schema.graphql.putKnowledge")
	if err != nil || !strings.Contains(alias.Text, "putKnowledge") {
		t.Fatalf("legacy operation compatibility help missing: %s %v", alias.Text, err)
	}
	h := helpDefinition(cfg, reg, prompts.Builtin(), func() *tool.Registry { return reg.View([]string{"help"}) })
	missing, err := h.Execute(context.Background(), tool.Args{"topic": "schema.graphql.appendKnowledge"})
	if err != nil || !strings.Contains(missing.Text, "unavailable in the current tool view") || strings.Contains(missing.Text, "```json") {
		t.Fatalf("unavailable graph gained executable template: %s %v", missing.Text, err)
	}
}

func TestEvidenceHelpReturnsRecordsAndReviewerRecipe(t *testing.T) {
	reg, cfg := catalogTools(t, prompts.Builtin())
	h := helpDefinition(cfg, reg, prompts.Builtin())
	for _, name := range []string{"taskEvidence", "sessionEvidence"} {
		res, err := h.Execute(context.Background(), tool.Args{"topic": "schema.graphql." + name})
		if err != nil {
			t.Fatal(err)
		}
		query := nativeHelpTemplate(t, res.Text).Arguments["query"].(string)
		for _, field := range []string{"groups", "records", "nextOffset", "kind"} {
			if !strings.Contains(query, field) {
				t.Errorf("%s template lacks useful %s: %s", name, field, query)
			}
		}
		if name == "taskEvidence" {
			for _, cue := range []string{`phase:"reviewer"`, "record", "kind", "offset:0"} {
				if !strings.Contains(res.Text, cue) {
					t.Errorf("missing %s in %s", cue, res.Text)
				}
			}
		}
	}
}

func TestEvidenceHelpExecutesReviewerRead(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.CreatePlanContext(plan.WithProject(t.Context(), root), s, "plan", "goal")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "approach")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "task", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	records, _ := json.Marshal([]any{map[string]any{"Kind": "review", "Attempt": 1, "Verification": "unverified", "Output": strings.Repeat("large recorded output", 20000), "ReviewerEvents": []session.Event{{Time: time.Now().UTC(), Kind: "role_violation", Text: "reviewer shell modified project contents"}}}})
	if err := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempt_log=$log,t.attempts=1`, map[string]any{"id": task.ID, "log": string(records)}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	d, err := graphTool.New(api.New(s, "test", t.TempDir()), cfg.Tools.GraphQL, root, "evidence-help", s.Identity())
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	reg.Register(d)
	h := helpDefinition(cfg, reg, prompts.Builtin())
	res, err := h.Execute(t.Context(), tool.Args{"topic": "schema.graphql.taskEvidence"})
	if err != nil {
		t.Fatal(err)
	}
	generated := nativeHelpTemplate(t, res.Text).Arguments["query"].(string)
	_, recipe, _ := strings.Cut(res.Text, "```graphql\n")
	recipe, _, _ = strings.Cut(recipe, "```")
	for index, query := range []string{strings.ReplaceAll(generated, "<taskId>", task.ID), strings.ReplaceAll(recipe, "<actual task ID>", task.ID)} {
		want := `"kind":"review"`
		if index == 1 {
			want = "reviewer shell modified project contents"
		}
		result, err := d.Execute(t.Context(), tool.Args{"query": query})
		if err != nil || result.Failed || !strings.Contains(result.Text, want) {
			t.Fatalf("help failed to retrieve real reviewer failure: %s %v", result.Text, err)
		}
	}
}
