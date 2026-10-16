package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

func TestHelpOpeningPreservesRequestedSchema(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "read", Desc: "Read a file", Params: []tool.Param{{Name: "path", Type: tool.TypeString, Required: true}}})
	d := helpDefinition(config.Defaults(), reg, nil)
	if !d.Opening {
		t.Fatal("help is not marked as an opening")
	}
	a, ignored := tool.NormalizeOpening(d, tool.Args{"topic": "schema.read", "query": "x"})
	if len(ignored) != 1 || ignored[0] != "query" {
		t.Fatal(ignored)
	}
	raw, _ := json.Marshal(a)
	a, gap := tool.ValidateJSON(d, string(raw))
	if gap != "" {
		t.Fatal(gap)
	}
	r, err := d.Execute(context.Background(), a)
	if err != nil || !strings.Contains(r.Text, "read") || !strings.Contains(r.Text, "path") || strings.Contains(r.Text, "# Harness operating guide") {
		t.Fatalf("requested schema replaced by default help: %s %v", r.Text, err)
	}
}
