package process

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPythonAliasIdentificationDoesNotClaimOrdinaryInstalls(t *testing.T) {
	for _, p := range []string{`C:\Users\A\AppData\Local\Microsoft\WindowsApps\python3.exe`, `C:/Users/A/Microsoft/WindowsApps/PYTHON.EXE`} {
		if !WindowsPythonAlias(p) {
			t.Fatal(p)
		}
	}
	for _, p := range []string{`C:\Python\python.exe`, `C:/WindowsApps/notpython.exe`, `C:/apps/node.exe`} {
		if WindowsPythonAlias(p) {
			t.Fatal(p)
		}
	}
}

func TestPythonAliasStubIsNotInstalledRuntime(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows alias executable")
	}
	dir := filepath.Join(t.TempDir(), "WindowsApps")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "python3.exe")
	if err := os.WriteFile(path, nil, 0700); err != nil {
		t.Fatal(err)
	}
	if resolved, err := Executable(path); err == nil || resolved != "" || !strings.Contains(err.Error(), "not a verified interpreter") {
		t.Fatalf("stub declared installed: %q %v", resolved, err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := Executable(exe); err != nil || resolved != exe {
		t.Fatalf("ordinary executable changed: %q %v", resolved, err)
	}
}

func TestVersionProbeOutputIsBoundedAndDrained(t *testing.T) {
	w := &versionOutput{}
	data := []byte(strings.Repeat("x", 1024))
	for range 10 {
		if n, err := w.Write(data); n != len(data) || err != nil {
			t.Fatal(n, err)
		}
	}
	if len(w.text) != 128 {
		t.Fatal(len(w.text))
	}
}
