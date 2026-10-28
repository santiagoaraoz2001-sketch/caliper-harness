package session

import (
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/pathutil"
)

// A matching asserted digest is not proof that the frozen selection bytes match.
// Corrupted historical evidence must not silently describe a different suite.
func TestTestsEvidenceRejectsSameHashChangedFrozenSelection(t *testing.T) {
	for _, field := range []string{"command", "spec_version", "config_revision"} {
		t.Run(field, func(t *testing.T) {
			// Reach the selection guard rather than fail early on a temporary-directory alias.
			root, err := pathutil.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir, id := t.TempDir(), "tests-integrity"
			selection := func(changed bool) map[string]any {
				suite := map[string]any{"id": "smoke", "command": "original command", "spec_key": "project/testing/smoke", "spec_version": "original-version"}
				out := map[string]any{"id": id, "workspace": root, "hash": "unchanged-asserted-hash", "config_revision": "original-config", "suites": []any{suite}}
				if changed {
					if field == "config_revision" {
						out[field] = "different-config"
					} else {
						suite[field] = "different-value"
					}
				}
				return out
			}
			appendEvent := func(kind string, value any) {
				t.Helper()
				text, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := Append(dir, id, Event{Kind: kind, Text: string(text)}); err != nil {
					t.Fatal(err)
				}
			}
			if err := Append(dir, id, Event{Kind: "workspace", Text: root}); err != nil {
				t.Fatal(err)
			}
			appendEvent("test_start", map[string]any{"selection": selection(false)})
			appendEvent("test_result", map[string]any{"selection": selection(true), "status": "passed"})
			if _, _, err := VisitEvidence(t.Context(), dir, id, root, func(EvidenceGroup) error { return nil }); err == nil || !strings.Contains(err.Error(), "Tests archive selection changed") {
				t.Fatalf("changed frozen selection did not reach its integrity guard: %v", err)
			}
		})
	}
}
