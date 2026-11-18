package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/tool"
)

// A frozen node --test acceptance command naming files that do not exist
// must refuse before executing: node silently skips missing file arguments
// and exits 0, so the episode would report a pass over tests that never ran.
// (c52 Task 2 acceptance named test/physics.test.js and test/world.test.js,
// which were never written, and still exited 0.)
func TestCheckRefusesMissingNodeTestInputs(t *testing.T) {
	_, def, _, dir := episodeRunner(t, "node --test test/physics.test.js test/world.test.js test/docking.test.js")
	if err := os.MkdirAll(filepath.Join(dir, "test"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test", "docking.test.js"), []byte("import {test} from 'node:test';\ntest('ok', () => {});\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args, repair := tool.ValidateJSON(def, `{}`)
	if repair != "" {
		t.Fatal(repair)
	}
	_, err := def.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "input missing") || !strings.Contains(err.Error(), "test/physics.test.js") {
		t.Fatalf("missing acceptance input not refused: %v", err)
	}
	if _, e := os.Stat(filepath.Join(dir, "suite.txt")); e == nil {
		t.Fatal("configured suite executed despite the missing acceptance input")
	}
}

// When every named input exists the episode runs normally, and glob
// arguments are left to the shell/node expansion instead of existence checks.
func TestCheckAdmitsExistingAndGlobNodeTestInputs(t *testing.T) {
	for _, acceptance := range []string{
		"node --test test/docking.test.js",
		`node --test "test/*.test.js"`,
	} {
		_, def, _, dir := episodeRunner(t, acceptance)
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "test"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "test", "docking.test.js"), []byte("import {test} from 'node:test';\ntest('ok', () => {});\n"), 0600); err != nil {
			t.Fatal(err)
		}
		args, repair := tool.ValidateJSON(def, `{}`)
		if repair != "" {
			t.Fatal(repair)
		}
		if _, err := def.Execute(context.Background(), args); err != nil && strings.Contains(err.Error(), "input missing") {
			t.Fatalf("acceptance %q refused as missing input: %v", acceptance, err)
		}
		if _, e := os.Stat(filepath.Join(dir, "suite.txt")); e != nil {
			t.Fatalf("episode did not execute for %q: %v", acceptance, e)
		}
	}
}
