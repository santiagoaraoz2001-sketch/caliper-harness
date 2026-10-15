package graph

import (
	"context"
	"fmt"
	"strings"

	api "specific-harness/internal/graph"
	"specific-harness/internal/memory"
	"specific-harness/internal/prompts"
	"specific-harness/internal/store"
)

const referencePublisher = "harness/reference"

// PublishReferences is an explicit startup operation, separate from read-only
// tool construction/help. YAML is canonical; the existing knowledge owner keeps
// selected graph documents versioned and scoped without adding another schema.
func PublishReferences(ctx context.Context, a *api.API, s *store.Store, root string, catalogs ...*prompts.Catalog) error {
	pc := prompts.Select(catalogs...)
	scoped, err := a.Scoped(root, referencePublisher, true)
	if err != nil {
		return err
	}
	root = scoped.CapturedProject()
	read := func(key string) (*memory.Entry, error) {
		// Host startup selects one canonical key; model request/result budgets
		// belong to later GraphQL reads, not installation of authored guides.
		entries, err := memory.Knowledge(ctx, s, root, key, "", 1, 0, true)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return &memory.Entry{}, nil
		}
		return entries[0], nil
	}
	type document struct{ key, body, previous string }
	var changes []document
	for _, sourceKey := range pc.Keys("help.reference.") {
		key := referencePublisher + "/" + strings.TrimPrefix(sourceKey, "help.reference.")
		current, err := read(key)
		if err != nil {
			return fmt.Errorf("reference publication read %q: %w", key, err)
		}
		if current.ID != "" && current.SessionRef != referencePublisher {
			return fmt.Errorf("reference publication namespace collision at %q: current author %q; preserve the existing head and resolve the collision explicitly", key, current.SessionRef)
		}
		body := pc.Text(sourceKey)
		if current.ID == "" || current.Body != body {
			changes = append(changes, document{key, body, current.ID})
		}
	}
	for _, doc := range changes {
		_, err := memory.PutKnowledge(ctx, s, root, referencePublisher, doc.key, doc.body, doc.previous, "", "")
		if err != nil {
			// A concurrent startup may publish this exact source version first.
			// Accept only its actual matching owner/body; never blind-overwrite it.
			current, readErr := read(doc.key)
			if readErr == nil && current.SessionRef == referencePublisher && current.Body == doc.body {
				continue
			}
			return fmt.Errorf("reference publication %q: %w", doc.key, err)
		}
	}
	return nil
}
