package run

import (
	"context"
	"strings"

	"specific-harness/internal/memory"
	"specific-harness/internal/prompts"
	"specific-harness/internal/store"
)

// projectStyle captures one version for every role in this run. Style is
// authored project guidance, independent of optional decision-model advice.
// Global Behavior has already been shaped by its existing configuration.
func projectStyle(s *store.Store, root, behavior string, pc *prompts.Catalog) (combined, guide, note string) {
	if s == nil {
		return behavior, "", ""
	}
	heads, err := memory.Knowledge(context.Background(), s, root, memory.StyleguideKey, "", 1, 0, true)
	if err != nil {
		return behavior, "", "note: project style read failed; this run has no project style guidance: " + err.Error()
	}
	if len(heads) == 0 || strings.TrimSpace(heads[0].Body) == "" {
		return behavior, "", ""
	}
	guide = heads[0].Body
	if !pc.Has("continuation.project_style") {
		pc = prompts.Builtin()
		note = "note: custom prompt catalog predates project style; using the shipped project-style wrapper."
	}
	block, err := pc.Render("continuation.project_style", map[string]any{"Project": root, "Revision": heads[0].ID, "Body": guide})
	if err != nil {
		return behavior, "", "note: project style rendering failed; this run has no project style guidance: " + err.Error()
	}
	if behavior == "" {
		return block, guide, note
	}
	return behavior + "\n\n" + block, guide, note
}
