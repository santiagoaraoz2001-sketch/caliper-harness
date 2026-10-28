package bash

import (
	"os"
	"path/filepath"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestScriptTransportPreservesHeredocBytesAndNestedRegex(t *testing.T) {
	dir := t.TempDir()
	source := "const regex = /\\s+/g;\nconst nested = `const r = /\\\\s+/g`;\nconst text = 'café 日本語 🚀';\n"
	source += "if ('gross   pay'.replace(regex, ' ') !== 'gross pay') process.exit(7);\n"
	source += "if (new Function(nested + \";return 'gross   pay'.replace(r, ' ')\")() !== 'gross pay') process.exit(8);\n"
	script := "cat > exact.mjs <<'EOF'\n" + source + "EOF\nprintf '%s' 'stdout intact'\n"
	result, err := NewAt(testCfg(), dir).Execute(t.Context(), tool.Args{"cmd": script})
	if err != nil || result.Failed || !strings.Contains(result.Text, "stdout intact") {
		t.Fatalf("%+v %v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "exact.mjs"))
	if err != nil || string(got) != source {
		t.Fatalf("bytes changed: %q want %q (%v)", got, source, err)
	}
	// Exercise the generated nested program when Node is present, not merely exit zero.
	node := "node exact.mjs"
	result, err = NewAt(testCfg(), dir).Execute(t.Context(), tool.Args{"cmd": node})
	if err != nil || result.Failed {
		t.Fatalf("nested JS semantics: %+v %v", result, err)
	}
}
