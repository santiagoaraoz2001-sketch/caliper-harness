package session

import (
	"encoding/json"
	"testing"
)

func TestTestsEvidenceValidatesImmutableOwnerAndPreservesRaw(t *testing.T) {
	root := t.TempDir()
	for _, kind := range []string{"valid", "changed-selection", "foreign-workspace", "missing-start", "duplicate-start", "same-hash-changed-spec", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			id := "tests-owner"
			add := func(k, text string) {
				if err := Append(dir, id, Event{Kind: k, Text: text}); err != nil {
					t.Fatal(err)
				}
			}
			raw := func(hash, workspace string) string {
				b, _ := json.Marshal(map[string]any{"selection": map[string]any{"id": id, "hash": hash, "workspace": workspace}, "results": []any{map[string]any{"suite_id": "smoke", "status": "passed", "coverage": map[string]string{"status": "unknown"}}}})
				return string(b)
			}
			add("workspace", root)
			if kind != "missing-start" {
				add("test_start", raw("original", root))
			}
			if kind == "duplicate-start" {
				add("test_start", raw("original", root))
			}
			result := raw("original", root)
			if kind == "changed-selection" {
				result = raw("changed", root)
			}
			if kind == "foreign-workspace" {
				result = raw("original", t.TempDir())
			}
			if kind == "same-hash-changed-spec" {
				var changed map[string]any
				_ = json.Unmarshal([]byte(result), &changed)
				changed["selection"].(map[string]any)["suites"] = []any{map[string]string{"spec_version": "another-version", "command": "another-command"}}
				b, _ := json.Marshal(changed)
				result = string(b)
			}
			if kind == "corrupt" {
				result = "not JSON"
			}
			add("test_result", result)
			seen := ""
			_, pending, err := VisitEvidence(t.Context(), dir, id, root, func(g EvidenceGroup) error {
				for _, r := range g.Records {
					if r.Event.Kind == "test_result" {
						seen = r.Event.Text
					}
				}
				return nil
			})
			if kind == "valid" {
				if err != nil || pending != nil || seen != result {
					t.Fatalf("valid immutable projection failed: %v %v %s", err, pending, seen)
				}
			} else if err == nil {
				t.Fatal("invalid Tests ownership accepted")
			}
		})
	}
}
