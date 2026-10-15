package tool_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// Exact failed reply shapes retained from the actual wave2 run6 session.
func TestLiteralRepairActualQuotedAndCollidingReplies(t *testing.T) {
	for _, tc := range []struct {
		name, header, ending string
		words                []string
	}{
		{"quoted tool marker", "<<'EOF'", "EOF", []string{"quoted", "unquoted", "shell"}},
		{"shell terminator closed tool value", "<<END", "END", []string{"cmd", "END", "outside", "If", "TOOL_END"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "```tool\ntool: bash\ncmd: " + tc.header + "\ncat <<'END' > fenced_payload.md\n```\nR40_FENCE_BODY\n```\nEND\nsha256sum fenced_payload.md | awk '{print $1}'\n" + tc.ending + "\n```"
			call, found, repair := tool.Extract(raw)
			if !found || call.Name != "" || call.Slots != nil || repair == "" {
				t.Fatalf("failed reply became executable: call=%+v found=%v repair=%q", call, found, repair)
			}
			for _, word := range tc.words {
				if !strings.Contains(repair, word) {
					t.Errorf("repair does not explain %q: %q", word, repair)
				}
			}
			if len(repair) > 600 || strings.Contains(repair, "sha256sum") || strings.Contains(repair, "R40_FENCE_BODY") {
				t.Error("repair echoes raw command/body rather than the boundary")
			}
			t.Logf("actual refused reply=%q; repair=%q", raw, repair)
		})
	}
}

func TestLiteralRepairDoesNotInventCollisionOrDumpLabels(t *testing.T) {
	call, found, repair := tool.Extract("```tool\ntool: bash\ncmd: true\nunrelated raw line\n```")
	if !found || call.Name != "" || repair == "" || strings.Contains(repair, "literal") || strings.Contains(repair, "TOOL_END") {
		t.Fatalf("unrelated bad line diagnosed as literal collision: %q", repair)
	}
	label := strings.Repeat("A", 10000)
	key := strings.Repeat("k", 10000)
	_, _, repair = tool.Extract("```tool\ntool: bash\n" + key + ": <<" + label + "\nraw\n" + label + "\nunrelated outside line\n```")
	if len(repair) > 600 || strings.Contains(repair, label) || strings.Contains(repair, key) || !strings.Contains(repair, "If") {
		t.Fatalf("boundary diagnostic unbounded or unconditional: length=%d", len(repair))
	}
	for _, header := range []string{"<<\"EOF\"", "<<'EOF", "<<'" + label + "'"} {
		c, found, q := tool.Extract("```tool\ntool: bash\ncmd: " + header + "\nraw\nEOF\n```")
		if !found || c.Name != "" || q == "" || len(q) > 600 || !strings.Contains(q, "unquoted") {
			t.Fatalf("quoted marker accepted or unhelpful: call=%+v found=%v length=%d", c, found, len(q))
		}
	}
}

func TestLiteralRepairAcceptedBytesAndSafety(t *testing.T) {
	command := "# preserved comment\r\nprintf 'value: x=y  \\n'\r\n"
	raw := "```tool\r\n# cmd: <<IGNORED\r\ntool: bash\r\ncmd: <<TOOL_END\r\n" + command + "TOOL_END\r\ntimeout: 10\r\n```"
	call, found, repair := tool.Extract(raw)
	if !found || repair != "" || call.Name != "bash" || call.Slots["cmd"] != command || call.Slots["timeout"] != "10" {
		t.Fatalf("valid scalar after literal or raw CRLF changed: call=%+v repair=%q", call, repair)
	}
	c, found, repair := tool.Extract(raw + "\n```tool\ntool: bash\ncmd: true\n```")
	if !found || c.Name != "" || c.Slots != nil || repair == "" {
		t.Fatal("multiple calls not refused before effects")
	}
}

func TestLiteralRepairDistinctMarkerBashCounterpart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "fenced_payload.md")
	expected := "```\nR40_FENCE_BODY\n```\n"
	command := "cat <<'END' > '" + file + "'\n" + expected + "END\ncat '" + file + "'\n"
	call, found, repair := tool.Extract("```tool\ntool: bash\ncmd: <<TOOL_END\n" + command + "TOOL_END\n```")
	if !found || repair != "" || call.Slots["cmd"] != command {
		t.Fatalf("distinct-marker counterpart refused/rewritten: call=%+v repair=%q", call, repair)
	}
	reg := tool.NewRegistry()
	reg.Register(bash.New(config.Defaults().Tools.Bash))
	def, ok := reg.Lookup(call.Name)
	if !ok {
		t.Fatal("actual Bash not registered")
	}
	args, repair := tool.Validate(def, call.Slots)
	if repair != "" {
		t.Fatal(repair)
	}
	res, err := def.Execute(context.Background(), args)
	bytes, readErr := os.ReadFile(file)
	if err != nil || readErr != nil || res.Failed || res.Truncated || res.Text != expected || string(bytes) != expected {
		t.Fatalf("real Bash counterpart result=%+v err=%v file=%q readErr=%v", res, err, bytes, readErr)
	}
	t.Logf("actual cmd=%q; artifact/output=%q", command, bytes)
}
