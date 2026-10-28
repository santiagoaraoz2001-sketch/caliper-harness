package pathutil

import (
	"os"
	"path/filepath"
	"testing"

	"specific-harness/internal/testfs"
)

func TestPhysicalJunctionIdentityAndEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "source α.txt"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "junction")
	if err := testfs.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || !IsLinkMode(info.Mode()) {
		t.Fatalf("junction must be recognized as a link: %v %v", info, err)
	}
	want, err := EvalSymlinks(filepath.Join(outside, "source α.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := EvalSymlinks(filepath.Join(link, "source α.txt"))
	if err != nil || got != want {
		t.Fatalf("junction alias identity: got %q want %q err=%v", got, want, err)
	}
	rel, err := filepath.Rel(root, got)
	if err != nil || filepath.IsLocal(rel) {
		t.Fatalf("physical target must be outside root: rel=%q err=%v", rel, err)
	}
	if _, err := EvalSymlinks(filepath.Join(link, "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing target loses filesystem error: %v", err)
	}
	t.Chdir(root)
	got, err = EvalSymlinks(filepath.Join("junction", "source α.txt"))
	if err != nil || got != rel {
		t.Fatalf("relative path contract: got %q want %q err=%v", got, rel, err)
	}
}
