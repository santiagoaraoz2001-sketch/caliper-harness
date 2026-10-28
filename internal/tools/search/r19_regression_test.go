package search

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func r19Write(t *testing.T, dir, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name = strings.ReplaceAll(name, ":", "–")
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestR19GlobalRequestedLimitAfterFiltering(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		r19Write(t, dir, name, "HIT\n")
	}
	r19Write(t, dir, "excluded.txt", "HIT\nHIT\n")
	res := grep(t, testCfg(), map[string]string{"pattern": "HIT", "path": dir, "glob": "*.go", "limit": "1"})
	if strings.Count(res.Text, "HIT") != 1 || !res.Truncated || !strings.Contains(res.Text, "[results truncated:") {
		t.Fatalf("global caller limit ignored: %+v", res)
	}
}
func TestR19ExplicitFileBasenameGlobAndLocation(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	p := r19Write(t, dir, "source: odd.go", "HIT\n")
	res := grep(t, testCfg(), map[string]string{"pattern": "HIT", "path": p, "glob": "*.go"})
	if !strings.Contains(grepLocators(t, res.Text), p+":1:HIT") || res.Truncated {
		t.Fatalf("explicit file location/glob lost: %+v", res)
	}
}
func TestR19ContextDoesNotConsumeMatchesOrDisappear(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	p := r19Write(t, dir, "source.go", "BEFORE\nHIT\nAFTER\n")
	cfg := testCfg()
	cfg.MaxMatches = 1
	for _, glob := range []string{"", "*.go"} {
		t.Run("glob="+glob, func(t *testing.T) {
			res := grep(t, cfg, map[string]string{"pattern": "HIT", "path": dir, "glob": glob, "context": "1"})
			for _, s := range []string{p + "-1-BEFORE", p + ":2:HIT", p + "-3-AFTER"} {
				if !strings.Contains(grepLocators(t, res.Text), s) {
					t.Errorf("context/match missing%q in%q", s, grepLocators(t, res.Text))
				}
			}
			if res.Truncated {
				t.Errorf("nonmatching context consumed match limit: %+v", res)
			}
		})
	}
}
func TestR19InvalidGlobNamesFilter(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	r19Write(t, dir, "source.go", "HIT\n")
	for _, name := range []string{"find", "grep"} {
		t.Run(name, func(t *testing.T) {
			def := NewFind(testCfg())
			slots := map[string]string{"pattern": "[", "path": dir}
			if name == "grep" {
				def = NewGrep(testCfg())
				slots = map[string]string{"pattern": "HIT", "glob": "[", "path": dir}
			}
			args, repair := tool.Validate(def, slots)
			if repair != "" {
				t.Fatal(repair)
			}
			_, err := def.Execute(context.Background(), args)
			if err == nil || !strings.Contains(err.Error(), "glob") || !strings.Contains(err.Error(), "[") {
				t.Fatalf("invalid filter presented as success: %v", err)
			}
		})
	}
}

func TestR19ExactFitExcludedAndConfiguredCeiling(t *testing.T) {
	requireRG(t)
	for _, tc := range []struct {
		name, limit   string
		allowed, want int
		truncated     bool
	}{{"exact-filtered", "1", 1, 1, false}, {"request-overflow", "1", 3, 1, true}, {"ceiling", "2", 3, 2, true}, {"default", "", 3, 2, true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := testCfg()
			cfg.MaxMatches = 2
			r19Write(t, dir, "selected.go", strings.Repeat("HIT\n", tc.allowed))
			r19Write(t, dir, "excluded.txt", strings.Repeat("HIT\n", 8))
			res := grep(t, cfg, map[string]string{"pattern": "HIT", "path": dir, "glob": "*.go", "limit": tc.limit})
			if strings.Count(res.Text, "HIT") != tc.want || res.Truncated != tc.truncated {
				t.Fatalf("caller/config/excluded bound %+v", res)
			}
			if tc.truncated && !strings.Contains(res.Text, "within "+map[int]string{1: "1", 2: "2"}[tc.want]+" matches") {
				t.Fatalf("marker hides effective request ceiling %+v", res)
			}
		})
	}
}
func TestR19RelativeGlobContextGroupsCaseAndLiteral(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "source dir"), 0700); err != nil {
		t.Fatal(err)
	}
	p := r19Write(t, filepath.Join(dir, "source dir"), "a: b.go", "BEFORE\nHiT|x\nAFTER\nGAP\nGAP\nBEFORE_2\nHiT|x\nAFTER_2\n")
	res := grep(t, testCfg(), map[string]string{"pattern": "hit|x", "literal": "true", "ignore_case": "true", "path": dir, "glob": "source dir/*.go", "context": "1", "limit": "2"})
	for _, text := range []string{p + "-1-BEFORE", p + ":2:HiT|x", p + "-3-AFTER", "\n--\n", p + "-6-BEFORE_2", p + ":7:HiT|x", p + "-8-AFTER_2"} {
		if !strings.Contains(grepLocators(t, res.Text), text) {
			t.Errorf("path/context/group lost %q: %s", text, grepLocators(t, res.Text))
		}
	}
	if res.Truncated {
		t.Fatal("exact matching-line limit inflated by context")
	}
}
func TestR19NonUTF8FilenameAndDecoderRefusal(t *testing.T) {
	requireRG(t)
	t.Run("native-filename", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows converts filename strings to UTF-16; raw non-UTF8 filenames cannot be represented. Decoder controls still run.")
		}
		dir := t.TempDir()
		name := "bad" + string([]byte{255}) + ".go"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("HIT\n"), 0600); illegalFilename(err) {
			t.Skip("filesystem refuses non-UTF8 filenames: native rg filename-byte route unexercised")
		} else if err != nil {
			t.Fatal(err)
		}
		for _, def := range []tool.Definition{NewGrep(testCfg()), NewFind(testCfg())} {
			slots := map[string]string{"pattern": "HIT", "path": dir}
			if def.Name == "find" {
				slots["pattern"] = "*.go"
			}
			args, repair := tool.Validate(def, slots)
			if repair != "" {
				t.Fatal(repair)
			}
			_, err := def.Execute(context.Background(), args)
			if err == nil || !strings.Contains(err.Error(), "non-UTF8") || !strings.Contains(err.Error(), "base64") {
				t.Fatalf("invalid filename silently changed/dropped by %s: %v", def.Name, err)
			}
		}
	})
	// Explicit decoder controls are component evidence, not an admitted real filename.
	encoded := base64.StdEncoding.EncodeToString([]byte("valid.go"))
	if got, err := (rgText{Bytes: encoded}).value(); err != nil || got != "valid.go" {
		t.Fatalf("valid base64 failed %q %v", got, err)
	}
	if _, err := (rgText{Bytes: "!!!"}).value(); err == nil {
		t.Fatal("malformed base64 accepted")
	}
	encoded = base64.StdEncoding.EncodeToString([]byte{255})
	if _, err := (rgText{Bytes: encoded}).value(); err == nil || !strings.Contains(err.Error(), "non-UTF8") {
		t.Fatalf("invalid decoded bytes silently changed %v", err)
	}
}

func TestR19ExistingByteLineCapsAndRGDiagnostic(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	r19Write(t, dir, "source.go", "HIT "+strings.Repeat("long", 80)+"\n")
	cfg := testCfg()
	cfg.MaxLineLength = 120
	res := grep(t, cfg, map[string]string{"pattern": "HIT", "path": dir})
	if !res.Truncated || !strings.Contains(res.Text, "[line cut]") {
		t.Fatalf("line cap lost %+v", res)
	}
	cfg = testCfg()
	cfg.MaxBytes = 80
	args, repair := tool.Validate(NewGrep(cfg), map[string]string{"pattern": "HIT", "path": dir})
	if repair != "" {
		t.Fatal(repair)
	}
	if _, err := NewGrep(cfg).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "max_bytes") {
		t.Fatalf("unrecoverable small-cap page must refuse: %v", err)
	}
	cfg.MaxBytes = 512
	cfg.MaxLineLength = 120
	res = grep(t, cfg, map[string]string{"pattern": "HIT", "path": dir})
	if !res.Truncated || len(res.Text) > cfg.MaxBytes || !strings.Contains(res.Text, "read file/line") {
		t.Fatalf("fitting byte/line cap lost: %+v", res)
	}
	args, repair = tool.Validate(NewGrep(testCfg()), map[string]string{"pattern": "[", "path": dir})
	if repair != "" {
		t.Fatal(repair)
	}
	_, err := NewGrep(testCfg()).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "search failed") || !strings.Contains(err.Error(), "regex parse error") {
		t.Fatalf("stderr diagnostic lost %v", err)
	}
}

func TestR19FindPathReusedUneditedAsGrepGlob(t *testing.T) {
	requireRG(t)
	dir := t.TempDir()
	nested := filepath.Join(dir, "internal", "tools", "fetch")
	if e := os.MkdirAll(nested, 0700); e != nil {
		t.Fatal(e)
	}
	r19Write(t, nested, "fetch.go", "before\nHTML text unescape\nafter\n")
	t.Chdir(dir)
	cwd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	reg := tool.NewRegistry()
	reg.Register(NewFindAt(testCfg(), cwd))
	reg.Register(NewGrepAt(testCfg(), cwd))
	invoke := func(name string, slots map[string]string) tool.Result {
		t.Helper()
		d, ok := reg.Lookup(name)
		if !ok {
			t.Fatal(name)
		}
		a, q := tool.Validate(d, slots)
		if q != "" {
			t.Fatal(q)
		}
		res, e := d.Execute(context.Background(), a)
		if e != nil {
			t.Fatal(e)
		}
		return res
	}
	found := invoke("find", map[string]string{"pattern": "**/*.go"})
	if found.Text != "internal/tools/fetch/fetch.go" || found.Truncated {
		t.Fatalf("find did not return the native relative path: %+v", found)
	}
	result := invoke("grep", map[string]string{"pattern": "html|text|unescape", "glob": found.Text})
	t.Logf("find path reused verbatim=%q grep=%q", found.Text, grepLocators(t, result.Text))
	if !strings.Contains(grepLocators(t, result.Text), found.Text+":2:HTML text unescape") || result.Truncated {
		t.Fatalf("find path cannot be reused unedited as a glob: %+v", result)
	}
	// A leading ./ remains root-relative, rather than becoming a basename wildcard.
	r19Write(t, dir, "fetch.go", "ROOT text\n")
	rootOnly := invoke("find", map[string]string{"pattern": "./fetch.go"})
	if rootOnly.Text != "./fetch.go" {
		t.Fatalf("root-relative find broadened: %+v", rootOnly)
	}
	rootResult := invoke("grep", map[string]string{"pattern": "text", "glob": rootOnly.Text})
	if rootResult.Truncated || strings.Contains(grepLocators(t, rootResult.Text), "internal/") || !strings.Contains(grepLocators(t, rootResult.Text), "./fetch.go:1:ROOT text") {
		t.Fatalf("root-relative grep broadened: %+v", rootResult)
	}
	// Narrowing to the exact returned file must not erase its glob identity.
	for _, file := range []struct{ path, line, canonical string }{
		{found.Text, ":2:HTML text unescape", "internal/tools/fetch/fetch.go"},
		{rootOnly.Text, ":1:ROOT text", "./fetch.go"},
	} {
		t.Run(file.path, func(t *testing.T) {
			result := invoke("grep", map[string]string{"pattern": "text", "path": file.path, "glob": file.path})
			if grepLocators(t, result.Text) != file.canonical+file.line || result.Truncated || result.Failed {
				t.Fatalf("exact returned file/glob lost its match: %+v", result)
			}
		})
	}
}

func TestR19ScopedFindPathReusedAsExactGlob(t *testing.T) {
	requireRG(t)
	dir := fixture(t)
	t.Chdir(dir)
	cwd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	spacedRoot := "space: root"
	if runtime.GOOS == "windows" {
		spacedRoot = "space; root"
	}
	for _, name := range []string{"internal/tui", spacedRoot} {
		if e := os.MkdirAll(filepath.Join(cwd, name, "nested"), 0700); e != nil {
			t.Fatal(e)
		}
		r19Write(t, filepath.Join(cwd, name), "settings.go", "before\nsyncDerived\nafter\nsyncDerived\n")
		r19Write(t, filepath.Join(cwd, name, "nested"), "fetch.go", "NESTED syncDerived\n")
		r19Write(t, filepath.Join(cwd, name), "fetch.go", "ROOT syncDerived\n")
	}
	r19Write(t, cwd, "fetch.go", "WORKSPACE syncDerived\n")
	reg := tool.NewRegistry()
	reg.Register(NewFindAt(testCfg(), cwd))
	reg.Register(NewGrepAt(testCfg(), cwd))
	t.Chdir(t.TempDir()) // Bound workspace, not the process cwd, owns path identity.
	call := func(name string, slots map[string]any) (tool.Result, error) {
		t.Helper()
		d, ok := reg.Lookup(name)
		if !ok {
			t.Fatal(name)
		}
		raw, e := json.Marshal(slots)
		if e != nil {
			t.Fatal(e)
		}
		a, q := tool.ValidateJSON(d, string(raw))
		if q != "" {
			t.Fatal(q)
		}
		return d.Execute(context.Background(), a)
	}
	for _, root := range []string{"internal/tui", filepath.Join(cwd, "internal/tui"), spacedRoot, filepath.Join(cwd, spacedRoot)} {
		t.Run(root, func(t *testing.T) {
			f, e := call("find", map[string]any{"pattern": "settings.go", "path": root})
			if e != nil || f.Truncated || f.Text != filepath.ToSlash(strings.TrimPrefix(filepath.Join(root, "settings.go"), cwd+string(filepath.Separator))) {
				t.Fatal(f, e)
			}
			g, e := call("grep", map[string]any{"pattern": "syncDerived", "path": root, "glob": f.Text, "literal": true, "limit": 1, "context": 1})
			if e != nil || !g.Truncated || !strings.Contains(grepLocators(t, g.Text), f.Text+":2:syncDerived") || !strings.Contains(grepLocators(t, g.Text), "after") || strings.Contains(grepLocators(t, g.Text), ":4:syncDerived") {
				t.Fatal("scoped exact path/cap/context", g, e)
			}
			explicit, e := call("grep", map[string]any{"pattern": "syncDerived", "path": f.Text, "glob": f.Text})
			if e != nil || explicit.Truncated || !strings.Contains(grepLocators(t, explicit.Text), f.Text+":4:syncDerived") {
				t.Fatal(explicit, e)
			}
			actual := f.Text
			if !filepath.IsAbs(actual) {
				actual = filepath.Join(cwd, actual)
			}
			mixed, e := call("grep", map[string]any{"pattern": "syncDerived", "path": actual, "glob": f.Text, "literal": true})
			if e != nil || mixed.Truncated || !strings.Contains(grepLocators(t, mixed.Text), f.Text+":4:syncDerived") {
				t.Fatal("raw Find glob with absolute file path", mixed, e)
			}
			relRoot, e := filepath.Rel(cwd, filepath.Dir(actual))
			if e != nil {
				t.Fatal(e)
			}
			for _, query := range []struct{ path, emitted string }{
				{relRoot, relRoot + "/settings.go"},
				{filepath.Dir(actual), relRoot + "/settings.go"},
				{relRoot + "/settings.go", relRoot + "/settings.go"},
				{actual, relRoot + "/settings.go"},
				{".", relRoot + "/settings.go"},
			} {
				t.Run(query.path, func(t *testing.T) {
					g, e := call("grep", map[string]any{"pattern": "syncDerived", "path": query.path, "glob": f.Text, "literal": true, "context": 1, "limit": 1})
					if e != nil || !g.Truncated || !strings.Contains(grepLocators(t, g.Text), filepath.ToSlash(query.emitted)+":2:syncDerived") || !strings.Contains(grepLocators(t, g.Text), "after") || strings.Contains(grepLocators(t, g.Text), ":4:syncDerived") {
						t.Fatal("raw Find glob cross matrix", g, e)
					}
				})
			}
			relative, e := call("grep", map[string]any{"pattern": "syncDerived", "path": root, "glob": "nested/*.go"})
			if e != nil || !strings.Contains(grepLocators(t, relative.Text), "nested/fetch.go:1:NESTED") || strings.Contains(grepLocators(t, relative.Text), "ROOT") {
				t.Fatal("directory-relative glob changed", relative, e)
			}
			anchored, e := call("grep", map[string]any{"pattern": "syncDerived", "path": root, "glob": "./fetch.go"})
			if e != nil || !strings.Contains(grepLocators(t, anchored.Text), "fetch.go:1:ROOT") || strings.Contains(grepLocators(t, anchored.Text), "NESTED") {
				t.Fatal("anchored pattern broadened", anchored, e)
			}
		})
	}
	rootOnly, e := call("grep", map[string]any{"pattern": "syncDerived", "glob": "./fetch.go"})
	if e != nil || !strings.Contains(grepLocators(t, rootOnly.Text), "./fetch.go:1:WORKSPACE") || strings.Contains(grepLocators(t, rootOnly.Text), "internal/") {
		t.Fatal(rootOnly, e)
	}
	ignored, e := call("grep", map[string]any{"pattern": "NEEDLE", "glob": "./ignored.txt"})
	if e != nil || strings.Contains(ignored.Text, "NEEDLE") {
		t.Fatal("exact shortcut bypassed ignore", ignored, e)
	}
	for _, args := range []map[string]any{{"pattern": "syncDerived", "glob": "["}, {"pattern": "syncDerived", "path": "missing-dir", "glob": "missing-dir/a.go"}} {
		if _, e := call("grep", args); e == nil {
			t.Fatal("invalid query became success", args)
		}
	}
}
