package bundle

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writeInventory records files in MANIFEST.sha256 exactly as the builder's
// write_manifest.py does, so verification is tested against an independent
// fixture rather than the repair writer's own output.
func writeInventory(t *testing.T, root string, files map[string]string) {
	t.Helper()
	rels := make([]string, 0, len(files))
	for rel, contents := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	var lines []string
	for _, rel := range rels {
		sum := sha256.Sum256([]byte(files[rel]))
		lines = append(lines, fmt.Sprintf("%s  ./%s", hex.EncodeToString(sum[:]), rel))
	}
	if err := os.WriteFile(filepath.Join(root, ManifestFile), []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyManifestCleanThenChangedAndMissing(t *testing.T) {
	root := t.TempDir()
	writeInventory(t, root, map[string]string{
		"caliper.exe":                    "binary",
		"runtime/decision/python.exe":    "interpreter",
		"bin/" + rgFileName():            "search",
		"runtime/decision/some file.txt": "spaced name",
	})
	checked, failures, err := VerifyManifest(root)
	if err != nil || len(failures) != 0 || checked != 4 {
		t.Fatalf("clean bundle: checked=%d failures=%v err=%v", checked, failures, err)
	}
	if err := os.WriteFile(filepath.Join(root, "runtime", "decision", "python.exe"), []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "bin", rgFileName())); err != nil {
		t.Fatal(err)
	}
	_, failures, err = VerifyManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, failure := range failures {
		got[failure.Path] = failure.Kind
	}
	want := map[string]string{
		"runtime/decision/python.exe": FailureChanged,
		"bin/" + rgFileName():         FailureMissing,
	}
	if len(got) != len(want) {
		t.Fatalf("failures=%v want %v", got, want)
	}
	for path, kind := range want {
		if got[path] != kind {
			t.Fatalf("failures=%v want %v", got, want)
		}
	}
}

func TestVerifyManifestProgressReportsEveryFile(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{}
	for i := range 50 {
		files[fmt.Sprintf("runtime/decision/file-%02d.txt", i)] = fmt.Sprintf("contents %d", i)
	}
	writeInventory(t, root, files)
	type report struct{ done, total int }
	var reports []report
	checked, failures, err := VerifyManifestProgress(root, func(done, total int) {
		reports = append(reports, report{done, total})
	})
	if err != nil || len(failures) != 0 || checked != 50 {
		t.Fatalf("progress verify: checked=%d failures=%v err=%v", checked, failures, err)
	}
	if len(reports) != 50 {
		t.Fatalf("progress reports=%d want one per file: %v", len(reports), reports)
	}
	for i, r := range reports {
		if r.total != 50 || r.done != i+1 {
			t.Fatalf("progress reports must count 1..50 of 50 in order, got %v", reports)
		}
	}
}

func TestVerifyManifestWithoutInventoryIsError(t *testing.T) {
	if _, _, err := VerifyManifest(t.TempDir()); err == nil || !strings.Contains(err.Error(), "install inventory missing") {
		t.Fatalf("missing inventory accepted: %v", err)
	}
}

func TestClassifyFailuresTrustAnchorsAreNotDownloadable(t *testing.T) {
	repairable, unrepairable := ClassifyFailures([]Failure{
		{Path: "runtime/decision/Lib/site-packages/torch/__init__.py", Kind: FailureChanged},
		{Path: "bin/" + rgFileName(), Kind: FailureMissing},
		{Path: "runtime/sources.json", Kind: FailureChanged},
		{Path: "runtime/decision/requirements.lock", Kind: FailureMissing},
		{Path: "caliper.exe", Kind: FailureChanged},
	})
	if len(repairable) != 2 || len(unrepairable) != 3 {
		t.Fatalf("repairable=%v unrepairable=%v", repairable, unrepairable)
	}
	for _, failure := range unrepairable {
		if failure.Path == "runtime/decision/Lib/site-packages/torch/__init__.py" || failure.Path == "bin/"+rgFileName() {
			t.Fatalf("downloadable dependency classified unrepairable: %v", failure)
		}
	}
}

func TestDownloadVerifiedAcceptsPinnedBytesAndRejectsOthers(t *testing.T) {
	payload := []byte("pinned dependency bytes")
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	dst := filepath.Join(t.TempDir(), "download.bin")
	if err := downloadVerified(server.Client(), server.URL, hex.EncodeToString(sum[:]), dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); string(got) != string(payload) {
		t.Fatalf("downloaded %q want %q", got, payload)
	}
	bad := filepath.Join(t.TempDir(), "bad.bin")
	err := downloadVerified(server.Client(), server.URL, strings.Repeat("0", 64), bad)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("tampered download accepted: %v", err)
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("rejected download left on disk")
	}
}

func TestRepairRefusesUnrepairableFailuresBeforeDownloading(t *testing.T) {
	err := Repair(t.TempDir(), []Failure{{Path: "caliper.exe", Kind: FailureChanged}}, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot be downloaded") {
		t.Fatalf("unrepairable repair attempted: %v", err)
	}
}

func TestInstallRipgrepFromZipArchive(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "rg.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("ripgrep-release/" + rgFileName())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("fake rg")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := installRipgrep(root, archive, "https://example.invalid/ripgrep.zip"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "bin", rgFileName()))
	if err != nil || string(got) != "fake rg" {
		t.Fatalf("installed rg=%q err=%v", got, err)
	}
}
