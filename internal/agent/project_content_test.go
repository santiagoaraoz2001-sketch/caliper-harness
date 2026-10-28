package agent_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"specific-harness/internal/agent"
	"testing"
)

func TestFingerprintIgnoredAncestorStillObservesProject(t *testing.T) {
	parent := t.TempDir()
	if out, err := exec.Command("git", "init", parent).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("fixture/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "fixture")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "source.js")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := (&agent.Runner{Workspace: root}).WorkspaceFingerprint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "unrelated.txt"), []byte("sibling"), 0600); err != nil {
		t.Fatal(err)
	}
	sibling, err := (&agent.Runner{Workspace: root}).WorkspaceFingerprint(context.Background())
	if err != nil || sibling != before {
		t.Fatalf("ancestor repository expanded project scope: %v", err)
	}
	info, _ := os.Stat(path)
	if err := os.WriteFile(path, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := (&agent.Runner{Workspace: root}).WorkspaceFingerprint(context.Background())
	if err != nil || before == after {
		t.Fatalf("same-size edit in ignored ancestor escaped: %v", err)
	}
}

func TestFingerprintExactExclusionsAndGitIgnoredOutputs(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("generated/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"generated", "data"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	excluded := filepath.Join(root, "data", "session.jsonl")
	before, err := (&agent.Runner{Workspace: root, WorkspaceExcludes: []string{excluded}}).WorkspaceFingerprint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{excluded, filepath.Join(root, "generated", "result")} {
		if err := os.WriteFile(path, []byte("output"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	after, err := (&agent.Runner{Workspace: root, WorkspaceExcludes: []string{excluded}}).WorkspaceFingerprint(context.Background())
	if err != nil || before != after {
		t.Fatalf("owned state or ignored output changed fingerprint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "source.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err = (&agent.Runner{Workspace: root, WorkspaceExcludes: []string{excluded}}).WorkspaceFingerprint(context.Background())
	if err != nil || before == after {
		t.Fatalf("blanket data exclusion: %v", err)
	}
}
