package tools

import (
	"context"
	"io"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/files"
	"specific-harness/internal/tools/recall"
	"strings"
	"testing"
)

func TestSimplificationCompactActiveHelp(t *testing.T) {
	cfg := config.Defaults()
	cfg.Tools.Bash.MaxBytes = 512
	reg := tool.NewRegistry()
	w := &files.Workspace{Path: t.TempDir(), Caps: cfg.Tools.Bash}
	RegisterAt(reg, cfg, nil, io.Discard, nil, w)
	res, e := helpCall(t, reg, "")
	if e != nil || len(res.Text) > cfg.Tools.Bash.MaxBytes {
		t.Fatalf("compact index %+v %v", res, e)
	}
	for _, name := range reg.Names() {
		if !strings.Contains(res.Text, name) {
			t.Fatalf("index omitted %s", name)
		}
	}
}
func TestSimplificationRecallSchemaKinds(t *testing.T) {
	d := recall.New(recall.Deps{})
	for _, value := range []string{"event", "decision", "note", ""} {
		if _, q := tool.ValidateJSON(d, `{"query":"x","kind":"`+value+`"}`); q != "" {
			t.Fatal(q)
		}
	}
	for _, p := range d.Params {
		if p.Name == "kind" && len(p.Enum) != 4 {
			t.Fatal("kind schema does not advertise accepted values")
		}
	}
	if _, q := tool.ValidateJSON(d, `{"query":"x","kind":"bogus"}`); q == "" {
		t.Fatal("invalid kind passed schema")
	}
	_, _ = d.Execute(context.Background(), tool.Args{"query": "x"})
}
