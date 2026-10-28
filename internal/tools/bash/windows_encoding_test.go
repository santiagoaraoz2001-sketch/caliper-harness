package bash

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"specific-harness/internal/tool"
)

func TestWindowsUTF8ScriptAndUnicodePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows encoding fixture")
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell 7 is not installed; UTF-8 qualification unavailable")
	}
	dir := filepath.Join(t.TempDir(), "café 日本語")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "quoted space.ps1")
	want := "Text — café 日本語 🚀"
	// A detached PowerShell console defaults to OEM encoding; declare the
	// producer's UTF-8 output before testing the harness's unchanged byte capture.
	if err := os.WriteFile(path, []byte("[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)\n[Console]::WriteLine('"+want+"')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(s), "'", "'\\''") + "'" }
	command := quote(pwsh) + " -NoLogo -NoProfile -NonInteractive -File " + quote(path)
	r, err := NewAt(testCfg(), dir).Execute(t.Context(), tool.Args{"cmd": command})
	if err != nil || r.Failed || strings.TrimSpace(r.Text) != want {
		t.Fatalf("UTF-8/quoting changed: %+v %v", r, err)
	}
}
