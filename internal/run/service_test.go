package run

import (
	"io"
	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"specific-harness/internal/tools/files"
	"strings"
	"testing"
)

func TestServiceOwnerChangesOnlyOwnedBashAndChildContract(t *testing.T) {
	cfg := config.Defaults()
	cfg.PromptCatalog = prompts.Builtin()
	owner := &bash.ServiceOwner{}
	defer owner.Close()
	w := &files.Workspace{Path: t.TempDir(), Caps: cfg.Tools.Bash}
	for _, owned := range []bool{false, true} {
		var services *bash.ServiceOwner
		if owned {
			services = owner
		}
		d := launchDeps(cfg, nil, "http://unused", "unused", 0, 32768, strings.NewReader(""), io.Discard, w, "", services)
		reg := d.NewRegistry()
		def, _ := reg.Lookup("bash")
		hasService := false
		for _, p := range def.Params {
			hasService = hasService || p.Name == "service"
		}
		if hasService != owned {
			t.Fatalf("owned=%t: service exposure=%t", owned, hasService)
		}
		if owned {
			res, err := def.Execute(t.Context(), tool.Args{"service": "status"})
			if err != nil || !strings.Contains(res.Text, "No running owned") {
				t.Fatalf("%s %v", res.Text, err)
			}
		}
		if _, ok := reg.Lookup("read"); !ok {
			t.Fatal("unrelated tool lost")
		}
	}
}
