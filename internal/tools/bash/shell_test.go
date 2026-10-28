package bash

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

func TestLiteralShellAndPipelineFailure(t *testing.T) {
	d := NewAt(config.Defaults().Tools.Bash, t.TempDir())
	r, err := d.Execute(context.Background(), tool.Args{"cmd": "value='literal $() `data`'; printf '%s\\n' \"$value\"; false | cat", "timeout": int64(10)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Text, "literal $() `data`") || !r.Failed || r.ExitCode == nil || *r.ExitCode == 0 {
		t.Fatalf("literal command/pipeline status lost: %+v", r)
	}
}

func TestNativeWindowsNodeAndQuotedWorkspace(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows runtime identity")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is not installed")
	}
	d := NewAt(config.Defaults().Tools.Bash, t.TempDir())
	r, err := d.Execute(t.Context(), tool.Args{"cmd": `node -e 'if(process.platform!=="win32")process.exit(7); console.log("NATIVE_WINDOWS_OK")'`, "timeout": int64(10)})
	if err != nil || r.Failed || !strings.Contains(r.Text, "NATIVE_WINDOWS_OK") {
		t.Fatalf("native identity: %+v %v", r, err)
	}
}

func TestNestedUnbornGitWorkspaceStatus(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "unborn root")
	workspace := filepath.Join(parent, "project child")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", parent).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	for _, path := range []string{filepath.Join(parent, "unrelated.txt"), filepath.Join(workspace, "inside.txt")} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	d := NewAt(config.Defaults().Tools.Bash, workspace)
	if !strings.Contains(d.Schema(), "porcelain paths against git rev-parse --show-toplevel") {
		t.Fatal("callable Bash guidance omits repository-relative path ownership")
	}
	status, err := d.Execute(t.Context(), tool.Args{"cmd": "git status --porcelain=v1 --untracked-files=all -- ."})
	if err != nil || status.Failed || !strings.Contains(status.Text, "project child/inside.txt") || strings.Contains(status.Text, "unrelated.txt") {
		t.Fatalf("nested scoped status: %+v %v", status, err)
	}
	top, err := d.Execute(t.Context(), tool.Args{"cmd": "git rev-parse --show-toplevel"})
	if err != nil || top.Failed || !strings.HasSuffix(strings.TrimSpace(top.Text), "/unborn root") {
		t.Fatalf("actual ancestor root: %+v %v", top, err)
	}
	head, err := d.Execute(t.Context(), tool.Args{"cmd": "git rev-parse --verify HEAD"})
	if err != nil || !head.Failed || head.ExitCode == nil || *head.ExitCode == 0 {
		t.Fatalf("unborn baseline misreported: %+v %v", head, err)
	}
}
