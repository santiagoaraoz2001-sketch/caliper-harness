package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/files"
)

func helpCall(t *testing.T, reg *tool.Registry, topic string) (tool.Result, error) {
	t.Helper()
	d, ok := reg.Lookup("help")
	if !ok {
		t.Fatal("help not registered")
	}
	a, repair := tool.ValidateJSON(d, fmt.Sprintf(`{"topic":%q}`, topic))
	if repair != "" {
		t.Fatal(repair)
	}
	return d.Execute(context.Background(), a)
}

func TestHelpCurrentViewAndCaps(t *testing.T) {
	cfg := config.Defaults()
	t.Chdir(t.TempDir())
	w, e := files.Capture(cfg.Tools.Bash)
	if e != nil {
		t.Fatal(e)
	}
	reg := tool.NewRegistry()
	var active *tool.Registry
	RegisterAt(reg, cfg, strings.NewReader(""), io.Discard, nil, w, func() *tool.Registry { return active })
	active = reg.View([]string{"help", "read", "find_file", "find_dir", "ask"})
	focused, e := helpCall(t, active, "help.read")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(focused.Text, "## help.read") || !strings.Contains(focused.Text, "[default: 2000]") || strings.Contains(focused.Text, "## help.edit") || strings.Contains(focused.Text, "# Harness operating guide") {
		t.Fatalf("bad focused help: %s", focused.Text)
	}
	if res, err := helpCall(t, active, "edit"); err != nil || !strings.Contains(res.Text, "unavailable in the current tool view") || strings.Contains(res.Text, "Current schema:") {
		t.Fatalf("unavailable edit documentation/schema: %s %v", res.Text, err)
	}
	if _, e = helpCall(t, active, "absent"); e == nil {
		t.Fatal("unknown topic admitted")
	}
	full, e := helpCall(t, active, "guide")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(full.Text, "# Harness operating guide") || !strings.Contains(full.Text, "## help.find_dir") || strings.Contains(full.Text, "## help.edit") || strings.Contains(full.Text, "## help.find\n") {
		t.Fatal("full help ignored active view")
	}
	// Replacement registry models a claim's re-registered check, not a stale captured map.
	replacement := tool.NewRegistry()
	for _, n := range reg.Names() {
		d, _ := reg.Lookup(n)
		replacement.Register(d)
	}
	replacement.Register(tool.Definition{Name: "check", Params: []tool.Param{{Name: "cmd", Type: tool.TypeString, Default: "actual assigned check"}}})
	active = replacement.View([]string{"help", "edit", "check"})
	check, e := helpCall(t, active, "check")
	if e != nil || !strings.Contains(check.Text, "[default: actual assigned check]") {
		t.Fatalf("stale help: %s %v", check.Text, e)
	}
	if _, e = helpCall(t, active, "edit"); e != nil {
		t.Fatal(e)
	}
	cfg.Tools.Bash.MaxBytes = 100
	small := tool.NewRegistry()
	RegisterAt(small, cfg, nil, io.Discard, nil, w)
	if res, e := helpCall(t, small, "read"); e == nil || res.Text != "" || !strings.Contains(e.Error(), "focused topic") {
		t.Fatalf("silent cap: %+v %v", res, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d, _ := active.Lookup("help")
	if _, e = d.Execute(ctx, tool.Args{}); e != context.Canceled {
		t.Fatalf("cancel: %v", e)
	}
}

func TestDefaultFirstReadAndBash2000(t *testing.T) {
	cfg := config.Defaults()
	dir := t.TempDir()
	t.Chdir(dir)
	var body strings.Builder
	for i := 1; i <= 2001; i++ {
		fmt.Fprintf(&body, "ROW_%04d\n", i)
	}
	if e := os.WriteFile(filepath.Join(dir, "rows.txt"), []byte(body.String()), 0600); e != nil {
		t.Fatal(e)
	}
	w, e := files.Capture(cfg.Tools.Bash)
	if e != nil {
		t.Fatal(e)
	}
	reg := tool.NewRegistry()
	RegisterAt(reg, cfg, nil, io.Discard, nil, w)
	d, _ := reg.Lookup("read")
	a, repair := tool.ValidateJSON(d, `{"path":"rows.txt"}`)
	if repair != "" {
		t.Fatal(repair)
	}
	res, e := d.Execute(context.Background(), a)
	if e != nil || !strings.Contains(res.Text, "ROW_2000\n") || strings.Contains(res.Text, "ROW_2001\n") || !strings.Contains(res.Text, "next_offset=2001") || !res.Truncated {
		t.Fatalf("first default read: %v %s", e, res.Text)
	}
	d, _ = reg.Lookup("bash")
	a, repair = tool.ValidateJSON(d, `{"cmd":"cat rows.txt"}`)
	if repair != "" {
		t.Fatal(repair)
	}
	res, e = d.Execute(context.Background(), a)
	if e != nil || !strings.Contains(res.Text, "ROW_2000") || strings.Contains(res.Text, "ROW_2001") || !res.Truncated {
		t.Fatalf("default Bash: %v", e)
	}
	cfg.Tools.Bash.MaxLines = 500
	reg = tool.NewRegistry()
	RegisterAt(reg, cfg, nil, io.Discard, nil, w)
	d, _ = reg.Lookup("bash")
	a, _ = tool.ValidateJSON(d, `{"cmd":"cat rows.txt"}`)
	res, e = d.Execute(context.Background(), a)
	if e != nil || !strings.Contains(res.Text, "ROW_0500") || strings.Contains(res.Text, "ROW_0501") || !res.Truncated {
		t.Fatal("explicit line cap changed")
	}
}
