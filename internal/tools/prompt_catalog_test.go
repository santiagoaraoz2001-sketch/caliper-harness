package tools

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	api "specific-harness/internal/graph"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"specific-harness/internal/tools/fetch"
	"specific-harness/internal/tools/files"
	"specific-harness/internal/tools/graph"
	"specific-harness/internal/tools/judge"
	"specific-harness/internal/tools/recall"
	"specific-harness/internal/tools/search"
)

func catalogTools(t *testing.T, pc *prompts.Catalog) (*tool.Registry, config.Config) {
	t.Helper()
	cfg := config.Defaults()
	cfg.PromptCatalog = pc
	reg := tool.NewRegistry(pc)
	w := &files.Workspace{Path: ".", Caps: cfg.Tools.Bash, Prompts: pc}
	RegisterAll(reg, cfg, nil, io.Discard, nil)
	for _, d := range w.Definitions() {
		reg.Register(d)
	}
	reg.Register(judge.New(judge.Deps{}, pc))
	reg.Register(recall.New(recall.Deps{}, pc))
	g, err := graph.New(api.New(nil, "test", "sessions"), cfg.Tools.GraphQL, "/private/tmp", "test", "test-store", pc)
	if err != nil {
		t.Fatal(err)
	}
	reg.Register(g)
	for _, d := range graph.Preparation(g, nil, cfg.Tools.GraphQL, "go test ./...", pc) {
		reg.Register(d)
	}
	return reg, cfg
}

// The fixture was rendered by the pre-migration production constructors and
// actual help executor in a disposable copy, not reconstructed from new YAML.
// Approved question-led preparation prose was subsequently refreshed from these
// same owning renderers; untouched entries and all parity assertions remain.
func TestPromptCatalogDefaultToolAndHelpParity(t *testing.T) {
	bytes, err := os.ReadFile("testdata/prompt-defaults.json")
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{}
	if err := json.Unmarshal(bytes, &expected); err != nil {
		t.Fatal(err)
	}
	reg, _ := catalogTools(t, prompts.Builtin())
	actual := map[string]string{}
	for _, name := range reg.Names() {
		d, _ := reg.Lookup(name)
		actual[name+".schema"] = d.Schema()
	}
	h, _ := reg.Lookup("help")
	for _, topic := range append([]string{""}, reg.Names()...) {
		res, err := h.Execute(context.Background(), tool.Args{"topic": topic})
		if err != nil {
			t.Fatal(err)
		}
		actual["help."+topic] = res.Text
	}
	if len(actual) != len(expected) {
		t.Fatalf("rendered controls: got %d want %d", len(actual), len(expected))
	}
	for key, value := range expected {
		if actual[key] != value {
			t.Errorf("default rendering differs at %s (got %d bytes, want %d)", key, len(actual[key]), len(value))
		}
	}
}

func selectedCatalog(t *testing.T) *prompts.Catalog {
	t.Helper()
	doc := map[string]map[string]string{}
	if err := yaml.Unmarshal([]byte(prompts.Builtin().YAML()), &doc); err != nil {
		t.Fatal(err)
	}
	for key, value := range doc["texts"] {
		if strings.HasPrefix(key, "tools.") || strings.HasPrefix(key, "help.") || strings.HasPrefix(key, "discovery.") || strings.HasPrefix(key, "guidance.") {
			doc["texts"][key] = value + " [selected catalog]"
		}
	}
	bytes, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "prompts.yaml")
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	pc, err := prompts.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return pc
}

func TestPromptCatalogSelectedToolsHelpAndDiscovery(t *testing.T) {
	pc := selectedCatalog(t)
	reg, cfg := catalogTools(t, pc)
	for _, name := range reg.Names() {
		d, _ := reg.Lookup(name)
		if d.Prompts != pc || !strings.Contains(d.Desc, "[selected catalog]") || !strings.Contains(d.Purpose, "[selected catalog]") {
			t.Errorf("%s lost selected catalog", name)
		}
		for _, p := range d.Params {
			if p.Desc != "" && !strings.Contains(p.Desc, "[selected catalog]") {
				t.Errorf("%s.%s lost selected parameter prose", name, p.Name)
			}
		}
	}
	native, err := reg.NativeDefinitions()
	if err != nil || len(native) != len(reg.Names()) {
		t.Fatalf("selected native schemas: %d %v", len(native), err)
	}
	h, _ := reg.Lookup("help")
	for _, topic := range []string{"", "read", "help.edit", "graphql", "prepare_task"} {
		res, err := h.Execute(context.Background(), tool.Args{"topic": topic})
		if err != nil || !strings.Contains(res.Text, "[selected catalog]") {
			t.Errorf("selected help %q: %v", topic, err)
		}
	}
	// Real rg-backed zero-match routes must retain the same run snapshot, too.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("actual source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, d := range []tool.Definition{search.NewFindAt(cfg.Tools.Search, dir, pc), search.NewGrepAt(cfg.Tools.Search, dir, pc)} {
		args, repair := tool.ValidateJSON(d, `{"pattern":"no-such-source-text"}`)
		if repair != "" {
			t.Fatal(repair)
		}
		res, err := d.Execute(context.Background(), args)
		if err != nil || !res.NoMatches || !strings.Contains(res.Text, "[selected catalog]") {
			t.Errorf("selected %s feedback: %+v %v", d.Name, res, err)
		}
	}
}

func TestPromptCatalogSelectedGuidanceKeepsFactualRefusals(t *testing.T) {
	pc := selectedCatalog(t)
	cfg := config.Defaults()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte("one\ntwo\none\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := &files.Workspace{Path: dir, Caps: cfg.Tools.Bash, Prompts: pc}
	ds := w.Definitions()
	read, err := ds[0].Execute(context.Background(), tool.Args{"path": "sample.go", "offset": int64(1), "limit": int64(1)})
	if err != nil {
		t.Fatal(err)
	}
	_, tail, _ := strings.Cut(read.Text, "sha256=")
	hash := strings.Fields(tail)[0]
	for _, c := range []struct {
		name, prefix string
		def          tool.Definition
		args         tool.Args
	}{
		{"existing", "sample.go exists;", ds[2], tool.Args{"path": "sample.go", "content": "new"}},
		{"hash", "sample.go:", ds[1], tool.Args{"path": "sample.go", "expected_hash": "wrong", "old": "two", "new": "new"}},
		{"routes", "edit mixes exact-span and line-range routes;", ds[1], tool.Args{"path": "sample.go", "expected_hash": hash, "old": "two", "start_line": int64(2), "end_line": int64(2), "new": "new"}},
		{"range", "lines 2-2 not returned by current read;", ds[1], tool.Args{"path": "sample.go", "expected_hash": hash, "start_line": int64(2), "end_line": int64(2), "new": "new"}},
		{"missing-old", "old matched 0 times;", ds[1], tool.Args{"path": "sample.go", "expected_hash": hash, "old": "absent", "new": "new"}},
		{"ambiguous-old", "old matched 2 times;", ds[1], tool.Args{"path": "sample.go", "expected_hash": hash, "old": "one", "new": "new"}},
		{"empty-old", "old is empty;", ds[1], tool.Args{"path": "sample.go", "expected_hash": hash, "new": "new"}},
		{"missing-end", "line-range edit needs both integer start_line and end_line;", ds[1], tool.Args{"path": "sample.go", "expected_hash": hash, "start_line": int64(1), "new": "new"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := c.def.Execute(context.Background(), c.args)
			if err == nil || res.Changed || !strings.HasPrefix(err.Error(), c.prefix) || !strings.Contains(err.Error(), "[selected catalog]") {
				t.Fatalf("factual refusal/snapshot lost: %+v %v", res, err)
			}
			if c.name == "range" && !strings.Contains(err.Error(), `read path="sample.go" offset=2 limit=1; page through end_line=2`) {
				t.Fatal("actual range recommendation lost")
			}
		})
	}
	unchanged, _ := os.ReadFile(filepath.Join(dir, "sample.go"))
	if string(unchanged) != "one\ntwo\none\n" {
		t.Fatal("refused edits changed bytes")
	}
	b := bash.New(cfg.Tools.Bash, pc)
	if _, err := b.Execute(context.Background(), tool.Args{"cmd": "true", "timeout": int64(cfg.Tools.Bash.MaxTimeout + 1)}); err == nil || !strings.Contains(err.Error(), "[selected catalog]") {
		t.Fatalf("timeout guidance: %v", err)
	}
	limited := cfg.Tools.Bash
	limited.MaxBytes = 3
	b = bash.New(limited, pc)
	res, err := b.Execute(context.Background(), tool.Args{"cmd": "printf 0123456789"})
	if err != nil || !res.Truncated || !strings.Contains(res.Text, "[selected catalog]") {
		t.Fatalf("truncation guidance: %+v %v", res, err)
	}
	f := fetch.New(cfg.Tools.Fetch, pc)
	if _, err := f.Execute(context.Background(), tool.Args{"url": "bad-url"}); err == nil || !strings.Contains(err.Error(), "[selected catalog]") {
		t.Fatalf("fetch guidance: %v", err)
	}
	for _, capture := range []bool{false, true} {
		limits := cfg.Tools.Search
		if capture {
			limits.MaxCaptureBytes = 1
		} else {
			limits.MaxBytes = 20
		}
		d := search.NewGrepAt(limits, dir, pc)
		a, repair := tool.ValidateJSON(d, `{"pattern":"one"}`)
		if repair != "" {
			t.Fatal(repair)
		}
		if _, err := d.Execute(context.Background(), a); err == nil || !strings.Contains(err.Error(), "[selected catalog]") {
			t.Fatalf("search guidance capture=%t: %v", capture, err)
		}
	}
	g, err := graph.New(api.New(nil, "test", "sessions"), cfg.Tools.GraphQL, "/private/tmp", "test", "store", pc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Execute(context.Background(), tool.Args{"query": strings.Repeat("x", cfg.Tools.GraphQL.MaxRequestBytes+1)}); err == nil || !strings.Contains(err.Error(), "[selected catalog]") {
		t.Fatalf("Graph request guidance: %v", err)
	}
	prepare := graph.Preparation(g, nil, cfg.Tools.GraphQL, "", pc)[0]
	if _, _, err := prepare.Bind(context.Background(), tool.Args{"acceptance_command": "true"}); err == nil || !strings.Contains(err.Error(), "[selected catalog]") {
		t.Fatalf("Graph configured-command guidance: %v", err)
	}
	j := judge.New(judge.Deps{Judge: func(context.Context, decision.Request) (decision.Verdict, error) {
		t.Fatal("invalid options reached decision transport")
		return decision.Verdict{}, nil
	}}, pc)
	if _, err := j.Execute(context.Background(), tool.Args{"options": "one"}); err == nil || !strings.Contains(err.Error(), "[selected catalog]") {
		t.Fatalf("judge guidance: %v", err)
	}
}
