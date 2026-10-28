package run

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"specific-harness/internal/config"
	"specific-harness/internal/launch"
	"specific-harness/internal/memory"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

func modelTokenCounter(model any) memory.TokenCounter {
	if c, ok := model.(interface {
		CountTokens(context.Context, string) (int, error)
	}); ok {
		return c.CountTokens
	}
	return nil
}

func testingGuidance(key string) bool {
	return key == memory.TestsStandardKey || strings.HasPrefix(key, "caliper/guides/tests/") || strings.HasPrefix(key, "project/guides/tests/")
}

func guidanceItem(ctx context.Context, cfg config.Config, s *store.Store, root string, ref plan.GuidanceRef) (projectContextItem, error) {
	item := projectContextItem{Key: ref.Key, ID: ref.Version, Section: ref.Section, Source: "project-authored", Status: "supplied"}
	entry := memory.BuiltinKnowledge(ref.Key, ref.Version, "")
	if entry == nil {
		if s == nil {
			return item, fmt.Errorf("guidance store unavailable")
		}
		entries, err := memory.Knowledge(ctx, s, root, "", ref.Version, 1, cfg.Tools.GraphQL.MaxResultBytes, true, []string{"key"})
		if err != nil {
			return item, err
		}
		if len(entries) != 1 || entries[0].Key != ref.Key {
			return item, fmt.Errorf("guidance version unavailable in this project: %s", ref.Version)
		}
		entry = entries[0]
	} else {
		item.Source = "caliper-default"
	}
	parts := memory.Sections(entry.Body, "", ref.Section)
	if len(parts) != 1 {
		return item, fmt.Errorf("guidance section %q unavailable in %s; query its sections first", ref.Section, ref.Key)
	}
	item.Body = parts[0].Body
	if item.Source == "project-authored" {
		refs, err := memory.References(ctx, s, root, entry.ID, false, cfg.Tools.GraphQL.MaxResultBytes, nil, ref.Section)
		if err != nil {
			return item, err
		}
		item.Links = memory.SectionReferences(refs, ref.Section)
	}
	// The section's Markdown already carries exact useful next hops. Loading
	// linked bodies or duplicating those links here would inflate every request.
	return item, nil
}

func planningGuidance(ctx context.Context, d launch.Deps, raw string) (*[]plan.GuidanceRef, error) {
	limits := d.KnowledgeLimits
	if err := limits.Validate(); err != nil {
		return nil, fmt.Errorf("guidance source limits unavailable: %w", err)
	}
	refs := []plan.GuidanceRef{}
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "knowledge:") {
			return nil, fmt.Errorf("guidance needs returned knowledge:key#section references")
		}
		key, section, ok := strings.Cut(strings.TrimPrefix(line, "knowledge:"), "#")
		if !ok || key == "" || section == "" {
			return nil, fmt.Errorf("guidance needs a key and exact section after #")
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		entry := memory.BuiltinKnowledge(key, "", "")
		if entry == nil {
			if d.Store == nil {
				return nil, fmt.Errorf("guidance store unavailable")
			}
			entries, err := memory.Knowledge(ctx, d.Store, d.Workspace, key, "", 1, limits.MaxResultBytes, false, []string{})
			if err != nil {
				return nil, err
			}
			if len(entries) != 1 {
				return nil, fmt.Errorf("guidance key %q unavailable in this project", key)
			}
			entry = entries[0]
		}
		refs = append(refs, plan.GuidanceRef{Key: key, Version: entry.ID, Section: section})
		if len(refs) > limits.MaxRows {
			return nil, fmt.Errorf("guidance selection exceeds configured source row budget")
		}
	}
	var count memory.TokenCounter
	if d.NewModel != nil {
		count = modelTokenCounter(d.NewModel())
	}
	cfg := config.Config{}
	cfg.Tools.GraphQL = limits
	for _, ref := range refs {
		item, err := guidanceItem(ctx, cfg, d.Store, d.Workspace, ref)
		item.Selection = "pinned"
		if err != nil {
			return nil, err
		}
		if err := admitGuidanceItem(ctx, count, &item); err != nil {
			return nil, err
		}
	}
	encoded, _ := json.Marshal(refs)
	if len(encoded) > limits.MaxResultBytes {
		return nil, fmt.Errorf("guidance selection exceeds configured source byte budget")
	}
	return &refs, nil
}

// selectedGuidance reads context from the explicitly selected owner. It never
// claims a task. A claimed caller is already admitted by bindProjectContext.
func selectedGuidance(s *store.Store, root string, c *session.Continuation, taskID string) ([]plan.GuidanceRef, error) {
	if s == nil {
		return nil, nil
	}
	kind, id := "", ""
	if taskID != "" {
		kind, id = "task", taskID
	} else if c.Workflow != nil {
		if c.Workflow.Store != s.Identity() {
			return nil, fmt.Errorf("selected guidance belongs to another store")
		}
		kind, id = c.Workflow.Kind, c.Workflow.ID
	}
	if kind == "" || kind == "root" {
		return nil, nil
	}
	if err := plan.CheckProject(s, kind, id, root); err != nil {
		return nil, err
	}
	var p *plan.Plan
	var a *plan.Approach
	var t *plan.Task
	var err error
	switch kind {
	case "task":
		t, err = plan.GetTask(s, id)
		if err == nil {
			a, err = plan.TaskApproach(s, id)
		}
	case "approach":
		a, err = plan.GetApproach(s, id)
	case "plan":
		p, err = plan.GetPlan(s, id)
	default:
		return nil, fmt.Errorf("unsupported guidance owner %q", kind)
	}
	if err == nil && a != nil {
		p, err = plan.ApproachPlan(s, a.ID)
	}
	if err != nil {
		return nil, err
	}
	if p == nil || p.State == plan.PlanRetired || (a != nil && a.State == plan.ApproachRetired) {
		return nil, fmt.Errorf("selected guidance owner is retired or unavailable")
	}
	documents := []string{}
	if t != nil {
		documents = append(documents, t.Sketch)
	}
	if a != nil {
		documents = append(documents, a.Title)
	}
	documents = append(documents, p.Goal)
	return plan.EffectiveGuidance(documents...)
}

func admitGuidanceItem(ctx context.Context, count memory.TokenCounter, item *projectContextItem) error {
	if item.Source != "caliper-default" && !memory.ProjectGuide(item.Key) {
		return nil
	}
	basis, err := memory.AdmitDomainEnvelope(ctx, count, func(basis string) ([]byte, error) {
		item.DomainBudgetBasis = basis
		return json.Marshal(item)
	})
	item.DomainBudgetBasis = basis
	return err
}
