package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var testPlatform = runtime.GOOS + "-" + runtime.GOARCH

func archiveBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for n, v := range entries {
		if e := tw.WriteHeader(&tar.Header{Name: n, Mode: 0755, Size: int64(len(v)), Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		tw.Write([]byte(v))
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}
func fixture(t *testing.T) []byte {
	files := map[string]string{"Caliper/caliper-harness": "binary", "Caliper/caliper.exe": "binary", "Caliper/prompts.yaml": "instructions", "Caliper/runtime/manifest.json": fmt.Sprintf(`{"format":1,"platform":%q}`, testPlatform)}
	for _, name := range []string{"lib/libkuzu.dylib", "lib/libkuzu.so", "kuzu_shared.dll", "ensure-windows-runtime.ps1", "bin/rg.exe", "runtime/decision/python.exe", "bin/rg", "runtime/decision/bin/python3", "runtime/worker/bin/python3", "runtime/decision/requirements.lock"} {
		files["Caliper/"+name] = "component"
	}
	files["Caliper/ensure-windows-runtime.ps1"] = "exit 0\n"
	var inventory strings.Builder
	for n, v := range files {
		fmt.Fprintf(&inventory, "%x  ./%s\n", sha256.Sum256([]byte(v)), strings.TrimPrefix(n, "Caliper/"))
	}
	files["Caliper/MANIFEST.sha256"] = inventory.String()
	return archiveBytes(t, files)
}
func server(t *testing.T, data []byte, badChecksum bool) (Manager, Release) {
	t.Helper()
	var base string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive":
			w.Write(data)
		case "/sum":
			if badChecksum {
				fmt.Fprintf(w, "%064d", 0)
			} else {
				fmt.Fprintf(w, "%x  archive.tar.gz", sha256.Sum256(data))
			}
		case "/latest":
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.1.3", "assets": []map[string]string{{"name": "caliper-0.1.3-" + testPlatform + ".tar.gz", "browser_download_url": base + "/archive"}, {"name": "caliper-0.1.3-" + testPlatform + ".tar.gz.sha256", "browser_download_url": base + "/sum"}}})
		}
	}))
	base = s.URL
	t.Cleanup(s.Close)
	return Manager{Home: t.TempDir(), Platform: testPlatform, Client: s.Client(), API: base + "/latest"}, Release{Version: "0.1.3", ArchiveURL: base + "/archive", ChecksumURL: base + "/sum"}
}
func TestStageNextLaunchPreservesOldAndUserData(t *testing.T) {
	m, r := server(t, fixture(t), false)
	os.MkdirAll(filepath.Join(m.Home, "versions", "0.1.2"), 0700)
	old := filepath.Join(m.Home, "versions", "0.1.2", "work")
	os.WriteFile(old, []byte("prior version"), 0600)
	user := filepath.Join(m.Home, "settings.yaml")
	os.WriteFile(user, []byte("custom"), 0600)
	found, e := m.Check(context.Background(), "0.1.2")
	if e != nil || found.Version != r.Version {
		t.Fatalf("check %+v %v", found, e)
	}
	selected, e := m.Prepare(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if s, _ := m.Selected(); s != nil {
		t.Fatal("prepare activated before consent/config preservation")
	}
	if _, e = m.Activate(selected); e != nil {
		t.Fatal(e)
	}
	s, e := m.Selected()
	if e != nil || s.Version != "0.1.3" {
		t.Fatalf("selected %+v %v", s, e)
	}
	for p, want := range map[string]string{old: "prior version", user: "custom"} {
		got, e := os.ReadFile(p)
		if e != nil || string(got) != want {
			t.Fatalf("prior data changed %s", p)
		}
	}
	// Retry after successful extraction or a pointer-write failure reuses only
	// the verified immutable folder instead of overwriting it.
	if _, e = m.Stage(context.Background(), r); e != nil {
		t.Fatal(e)
	}
}
func TestChecksumFailurePreservesSelection(t *testing.T) {
	m, r := server(t, fixture(t), true)
	prior := []byte("previous-selection")
	os.WriteFile(filepath.Join(m.Home, "current.json"), prior, 0600)
	if _, e := m.Stage(context.Background(), r); e == nil || !strings.Contains(e.Error(), "checksum") {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(filepath.Join(m.Home, "current.json"))
	if !bytes.Equal(got, prior) {
		t.Fatal("changed selection on failure")
	}
}
func TestArchiveTraversalRefused(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "Caliper/../../escape", `Caliper\..\escape`, "C:/escape"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if e := extract(bytes.NewReader(archiveBytes(t, map[string]string{name: "bad"})), root); e == nil {
				t.Fatal("accepted escaping archive")
			}
		})
	}
}
func TestArchiveSymlinkEscapeBeforeMkdir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("native symlink creation unavailable: %v", err)
	}
	e := extract(bytes.NewReader(archiveBytes(t, map[string]string{"link/created/file": "bad"})), root)
	if e == nil {
		t.Fatal("accepted parent link")
	}
	if _, e := os.Stat(filepath.Join(outside, "created")); !os.IsNotExist(e) {
		t.Fatal("created directory outside staging")
	}
}
func TestIncompleteOrCorruptInstallNotSelected(t *testing.T) {
	for _, files := range []map[string]string{{"Caliper/prompts.yaml": "only"}, {"Caliper/caliper-harness": "binary", "Caliper/caliper.exe": "binary", "Caliper/prompts.yaml": "instructions", "Caliper/runtime/manifest.json": fmt.Sprintf(`{"format":1,"platform":%q}`, testPlatform), "Caliper/MANIFEST.sha256": strings.Repeat("0", 64) + "  ./prompts.yaml\n"}} {
		m, r := server(t, archiveBytes(t, files), false)
		if _, e := m.Stage(context.Background(), r); e == nil {
			t.Fatal("accepted incomplete/corrupt")
		}
		if _, e := os.Stat(filepath.Join(m.Home, "current.json")); !os.IsNotExist(e) {
			t.Fatal("activated bad release")
		}
	}
}
func TestVersionAndMissingPlatform(t *testing.T) {
	if !Newer("0.1.12", "0.1.9") || Newer("0.1.2", "0.1.2") || Newer("garbage", "0.1.2") {
		t.Fatal("version comparison")
	}
	m, _ := server(t, fixture(t), false)
	m.Platform = "unsupported-platform"
	if _, e := m.Check(context.Background(), "0.1.2"); e == nil {
		t.Fatal("missing native bundle accepted")
	}
}
func TestSelectionCannotEscapeAndEnvUsesBase(t *testing.T) {
	m := Manager{Home: t.TempDir(), Platform: "linux-arm64"}
	b, _ := json.Marshal(Selection{Version: "../../escape", Platform: m.Platform, Root: "/tmp/escape"})
	os.WriteFile(filepath.Join(m.Home, "current.json"), b, 0600)
	if _, e := m.Selected(); e == nil {
		t.Fatal("invalid selection")
	}
	s := Selection{Root: "/app", Platform: "linux-arm64"}
	env := strings.Join(s.Env([]string{"PATH=/original", "LD_LIBRARY_PATH=/original/lib"}), "\n")
	if !strings.Contains(env, "PATH="+filepath.Join("/app", "bin")+string(os.PathListSeparator)+"/original") || !strings.Contains(env, "LD_LIBRARY_PATH="+filepath.Join("/app", "lib")+string(os.PathListSeparator)+"/original/lib") {
		t.Fatal(env)
	}
}

func TestInstalledCommandsMustRemainExecutable(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Windows does not use executable mode bits")
	}
	m, r := server(t, fixture(t), false)
	selected, e := m.Stage(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	commands := []string{"caliper-harness", "bin/rg", "runtime/decision/bin/python3"}
	if runtime.GOOS == "darwin" {
		commands = append(commands, "runtime/worker/bin/python3")
	}
	for _, name := range commands {
		file := filepath.Join(selected.Root, name)
		if e = os.Chmod(file, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = m.Selected(); e == nil {
			t.Fatalf("accepted nonexecutable %s", name)
		}
		if e = os.Chmod(file, 0700); e != nil {
			t.Fatal(e)
		}
	}
	env := Selection{Root: "/app", Platform: "linux-arm64"}.Env([]string{"PATH=/bin"})
	for _, value := range env {
		if value == "LD_LIBRARY_PATH=/app/lib:" {
			t.Fatal("empty library path searches working directory")
		}
	}
}

func TestPreparedReleaseReuseDoesNotDownload(t *testing.T) {
	m, r := server(t, fixture(t), false)
	if _, e := m.Prepare(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	r.ArchiveURL = "http://invalid.invalid/must-not-download"
	r.ChecksumURL = r.ArchiveURL
	if _, e := m.Prepare(context.Background(), r); e != nil {
		t.Fatalf("existing verified version should need no download: %v", e)
	}
	if _, e := (Manager{Home: "relative", Platform: testPlatform}).Prepare(context.Background(), r); e == nil {
		t.Fatal("relative update storage creates project debris")
	}
}

func TestWindowsBundleRequiresSystemRuntimeHelper(t *testing.T) {
	root := t.TempDir()
	if err := extract(bytes.NewReader(fixture(t)), root); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(root, "Caliper")
	if err := os.WriteFile(filepath.Join(app, "runtime", "manifest.json"), []byte(`{"format":1,"platform":"windows-amd64"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validate(app, "windows-amd64"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ensure-windows-runtime.ps1"} {
		path := filepath.Join(app, name)
		if err := os.Rename(path, path+".absent"); err != nil {
			t.Fatal(err)
		}
		if err := validate(app, "windows-amd64"); err == nil || !strings.Contains(err.Error(), filepath.FromSlash(name)) {
			t.Fatalf("missing prerequisite helper %s must refuse: %v", name, err)
		}
		if err := os.Rename(path+".absent", path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWindowsPrerequisiteFailurePreservesSelection(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("actual Windows PowerShell invocation")
	}
	m, r := server(t, fixture(t), false)
	selected, err := m.Prepare(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	prior := []byte("previous selection")
	os.WriteFile(filepath.Join(m.Home, "current.json"), prior, 0600)
	if err := os.WriteFile(filepath.Join(selected.Root, "ensure-windows-runtime.ps1"), []byte("exit 3010\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Activate(selected); err == nil || !strings.Contains(err.Error(), "prerequisite") {
		t.Fatalf("failure accepted: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(m.Home, "current.json"))
	if !bytes.Equal(got, prior) {
		t.Fatal("selection changed after failed prerequisite")
	}
}

func TestLegacyWindowsSelectionNeedsInventoriedRuntime(t *testing.T) {
	root := t.TempDir()
	if err := extract(bytes.NewReader(fixture(t)), root); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(root, "Caliper")
	os.Remove(filepath.Join(app, "ensure-windows-runtime.ps1"))
	os.WriteFile(filepath.Join(app, "runtime/manifest.json"), []byte(`{"format":1,"platform":"windows-amd64"}`), 0600)
	var extra strings.Builder
	for _, name := range []string{"msvcp140.dll", "vcruntime140.dll", "vcruntime140_1.dll", "runtime/decision/msvcp140.dll"} {
		data := []byte("legacy CRT")
		os.WriteFile(filepath.Join(app, name), data, 0600)
		fmt.Fprintf(&extra, "%x  ./%s\n", sha256.Sum256(data), name)
	}
	f, err := os.OpenFile(filepath.Join(app, "MANIFEST.sha256"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(extra.String())
	f.Close()
	inventory, err := os.ReadFile(filepath.Join(app, "MANIFEST.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ending := range []string{"\n", "\r\n"} {
		for _, finalNewline := range []bool{true, false} {
			layout := strings.ReplaceAll(strings.TrimSuffix(string(inventory), "\n"), "\n", ending)
			if finalNewline {
				layout += ending
			}
			if err := os.WriteFile(filepath.Join(app, "MANIFEST.sha256"), []byte(layout), 0600); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(app, "msvcp140.dll"), []byte("legacy CRT"), 0600)
			if err := validate(app, "windows-amd64", true); err != nil {
				t.Fatalf("ending=%q final=%v: %v", ending, finalNewline, err)
			}
			if err := validate(app, "windows-amd64"); err == nil {
				t.Fatal("new release accepted without helper")
			}
			os.WriteFile(filepath.Join(app, "msvcp140.dll"), []byte("changed"), 0600)
			if err := validate(app, "windows-amd64", true); err == nil {
				t.Fatal("legacy selection accepted modified DLL")
			}
		}
	}
}
