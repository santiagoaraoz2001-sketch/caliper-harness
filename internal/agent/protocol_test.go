package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

func TestProtocolMultipleCallsHaveNoPartialEffects(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	accepted := filepath.Join(dir, "accepted")
	block := func(cmd string) string { return "```tool\ntool: bash\ncmd: " + cmd + "\n```" }
	reg := tool.NewRegistry()
	reg.Register(bash.New(config.Defaults().Tools.Bash))
	model := &scriptModel{replies: []string{block("touch '"+first+"'") + "\n" + block("touch '"+second+"'"), block("printf accepted > '" + accepted + "'"), "done"}}
	r := &Runner{Model: model, Registry: reg, MaxSteps: 4}
	_, _, err := r.Run(context.Background(), "create only an accepted artifact after repairing an ambiguous reply")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{first, second} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("refused multiple-call reply created %s (err %v)", p, err)
		}
	}
	got, err := os.ReadFile(accepted)
	if err != nil || string(got) != "accepted" {
		t.Fatalf("corrected single call: got=%q err=%v", got, err)
	}
	if len(model.seen) < 2 || !strings.Contains(model.seen[1][len(model.seen[1])-1].Content, "one tool block") {
		t.Fatal("next request did not receive the single-call repair")
	}
}

func TestProtocolMalformedLiteralHasNoEffect(t *testing.T) {
	for _, continuation := range []string{"echo implicit continuation", "value = 2", "else:"} {
		t.Run(continuation, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "forbidden")
			reg := tool.NewRegistry()
			reg.Register(bash.New(config.Defaults().Tools.Bash))
			model := &scriptModel{replies: []string{"```tool\ntool: bash\ncmd: touch '" + marker + "'\n" + continuation + "\n```", "done"}}
			r := &Runner{Model: model, Registry: reg, MaxSteps: 3}
			_, _, err := r.Run(context.Background(), "refuse ambiguous multiline")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("malformed multiline executed (err %v)", err)
			}
			if !strings.Contains(model.seen[1][len(model.seen[1])-1].Content, "<<END") {
				t.Fatal("repair has no usable literal form")
			}
		})
	}
}

func TestProtocolLiteralBashBytes(t *testing.T) {
	expected := []byte("# YAML-like data\n\nroot:\n  value: a=b  \n\t# tab kept\n```tool\ntool: literal data\n```\nEND_LITERAL_other\ncrlf kept\r\n\n")
	file := filepath.Join(t.TempDir(), "bytes")
	command := fmt.Sprintf("cat > '%s' <<'DATA'\n%sDATA\ncat '%s'\n", file, expected, file)
	verifyLiteralCommand(t, command, expected)
	got, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(got, expected) {
		t.Fatalf("written bytes=%q want=%q err=%v", got, expected, err)
	}
}

func TestProtocolLiteralPythonBytes(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("byte probe requires installed python3")
	}
	expected := []byte("# comment\n\n  key: value  \n\ttab\n")
	command := "python3 - <<'PY'\n# preserve comment and blank line\n\nvalue = 2\nif value == 2:\n    data = b'# comment\\n\\n  key: value  \\n\\ttab\\n'\nelse:\n    data = b'wrong'\nimport sys\nsys.stdout.buffer.write(data)\nPY\n"
	verifyLiteralCommand(t, command, expected)
}

func verifyLiteralCommand(t *testing.T, command string, expected []byte) {
	t.Helper()
	call, found, repair := tool.Extract("```tool\ntool: bash\ncmd: <<END_LITERAL\n" + command + "END_LITERAL\n```")
	if !found || repair != "" || call.Slots["cmd"] != command {
		t.Fatalf("literal command changed: found=%v repair=%q got=%q want=%q", found, repair, call.Slots["cmd"], command)
	}
	def := bash.New(config.Defaults().Tools.Bash)
	args, repair := tool.Validate(def, call.Slots)
	if repair != "" {
		t.Fatal(repair)
	}
	res, err := def.Execute(context.Background(), args)
	if err != nil || res.Failed || res.Truncated || !bytes.Equal([]byte(res.Text), expected) {
		t.Fatalf("actual bash: result=%+v err=%v want=%q", res, err, expected)
	}
	t.Logf("command bytes=%d output bytes=%d exact=%v", len(command), len(res.Text), bytes.Equal([]byte(res.Text), expected))
	t.Logf("command=%q\noutput hex=%x\nexpected hex=%x", command, res.Text, expected)
}
