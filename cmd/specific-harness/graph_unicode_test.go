package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIGraphEscapedUnicode(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	call := func(query string) ([]byte, int) {
		t.Helper()
		argv := []string{"graph", "--config", config, "--db", filepath.Join(dir, "db"), "--sessions-dir", filepath.Join(dir, "sessions"), "--query", query}
		cmd := exec.Command(os.Args[0], "-test.run=^TestCLIProcess$")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HARNESS_CLI_PROCESS=1", "HARNESS_CLI_ARGV="+strings.Join(argv, "\x1f"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		t.Logf("actual main/graph argv=%q exit=%d stderr=%q stdout=%s", argv, code, stderr.String(), stdout.Bytes())
		return stdout.Bytes(), code
	}
	mixed := "R23_UNICODE café Ελληνικά 東京 👩‍💻  \n\tR27\t\"quoted\"\\slash\r\n\ntrail  "
	quoted, _ := json.Marshal(mixed)
	escaped := strings.ReplaceAll(strings.ReplaceAll(string(quoted), "👩‍💻", `\ud83d\udc69\u200d\ud83d\udcbb`), "é", `\u00e9`)
	cases := []struct{ name, value, want string }{
		{"escaped_pairs", escaped, mixed},
		{"literal_utf8", string(quoted), mixed},
		{"literal_backslashes", `"literal \\ud83d\\udc69"`, `literal \ud83d\udc69`},
		{"block_string", `"""literal \ud83d\udc69"""`, `literal \ud83d\udc69`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, code := call(`mutation{createPlan(title:` + c.value + `,goal:"fixture"){id title}}`)
			var out struct{ CreatePlan struct{ ID, Title string } }
			if err := json.Unmarshal(raw, &out); code != 0 || err != nil || out.CreatePlan.ID == "" || out.CreatePlan.Title != c.want {
				t.Errorf("actual --query readback: exit%d title=%q want=%q (%v)", code, out.CreatePlan.Title, c.want, err)
			}
			// A second actual CLI process reopens the persisted owning Kuzu store.
			id, _ := json.Marshal(out.CreatePlan.ID)
			raw, code = call(`{plan(id:` + string(id) + `){title}}`)
			var got struct{ Plan struct{ Title string } }
			if err := json.Unmarshal(raw, &got); code != 0 || err != nil || got.Plan.Title != c.want {
				t.Errorf("CLI reopen changed bytes: exit%d title=%q want=%q (%v)", code, got.Plan.Title, c.want, err)
			}
		})
	}
	before, code := call(`{plans{title}}`)
	if code != 0 {
		t.Fatal("cannot record pre-error state")
	}
	for _, bad := range []string{`"\u12G4"`, `"\u123"`, `"unclosed`} {
		t.Run("malformed_"+bad, func(t *testing.T) {
			_, code := call(`mutation{before:createPlan(title:"must not exist",goal:"g"){id} bad:createPlan(title:` + bad + `,goal:"g"){id}}`)
			if code == 0 {
				t.Fatal("CLI malformed inline value accepted")
			}
			after, code := call(`{plans{title}}`)
			if code != 0 || !bytes.Equal(before, after) {
				t.Fatalf("parse failure mutated public state: exit%d before=%s after=%s", code, before, after)
			}
		})
	}
}
