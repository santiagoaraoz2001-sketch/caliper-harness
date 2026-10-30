package run

import (
	"context"
	"encoding/json"
	"errors"
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
		entries, err := memory.Knowledge(ctx, s, root, "", ref.Version, 1, cfg.Tools.GraphQL.MaxResultBytes, true, []string{"key", "previousId"})
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
	item.domainLimit = memory.DomainInitialTokens
	if memory.ProjectGuide(entry.Key) && entry.PreviousID != "" {
		previous, err := memory.Knowledge(ctx, s, root, "", entry.PreviousID, 1, cfg.Tools.GraphQL.MaxResultBytes, true, []string{"body"})
		if err != nil || len(previous) != 1 {
			return item, fmt.Errorf("guidance section lineage unavailable for %s", entry.ID)
		}
		item.domainLimit = memory.DomainSectionTokens(previous[0].Body, ref.Section)
	}
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

const guidanceLocatorExample = "caliper/guides/ui/direction#brief-to-direction"
const guidanceLocatorHelp = "Use a returned key#section (knowledge: prefix optional), e.g. " + guidanceLocatorExample + ". Use the key, not its immutable version ID."

func parseGuidanceLocator(line string) (string, string, error) {
	key, section, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "knowledge:"), "#")
	if !ok || key == "" || section == "" || strings.Contains(section, "#") {
		return "", "", fmt.Errorf("invalid guidance locator %q; %s", line, guidanceLocatorHelp)
	}
	return key, section, nil
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
		key, section, err := parseGuidanceLocator(line)
		if err != nil {
			return nil, err
		}
		locator := key + "#" + section
		if seen[locator] {
			continue
		}
		seen[locator] = true
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
				return nil, fmt.Errorf("guidance key %q unavailable in this project; %s", key, guidanceLocatorHelp)
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
	items := make([]projectContextItem, 0, len(refs))
	for _, ref := range refs {
		item, err := guidanceItem(ctx, cfg, d.Store, d.Workspace, ref)
		item.Selection = "pinned"
		if err != nil {
			return nil, err
		}
		if err := admitGuidanceItem(ctx, count, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := admitGuidanceItems(ctx, count, items); err != nil {
		return nil, fmt.Errorf("guidance selection cannot fit together; select fewer or narrower sections: %w", err)
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
	limit := item.domainLimit
	if limit == 0 {
		limit = memory.DomainInitialTokens
	}
	basis, err := memory.AdmitDomainEnvelopeLimit(ctx, count, limit, func(basis string) ([]byte, error) {
		item.DomainBudgetBasis = basis
		return json.Marshal(item)
	})
	item.DomainBudgetBasis = basis
	return err
}

func domainContextItem(item projectContextItem) bool {
	return item.Source == "caliper-default" || memory.ProjectGuide(item.Key) || strings.HasPrefix(item.Key, "caliper/standards/") || strings.HasPrefix(item.Key, "caliper/guides/")
}

// Count the same serialized domain items delivered in the project packet.
// Ordinary facts and recall retain their independent source/context limits.
func admitGuidanceItems(ctx context.Context, count memory.TokenCounter, items []projectContextItem) error {
	domain := []projectContextItem{}
	for _, item := range items {
		if domainContextItem(item) {
			domain = append(domain, item)
		}
	}
	if len(domain) == 0 {
		return nil
	}
	basis, err := memory.AdmitDomainEnvelope(ctx, count, func(basis string) ([]byte, error) {
		for i := range domain {
			domain[i].DomainBudgetBasis = basis
		}
		return json.Marshal(projectContextPacket{Items: domain})
	})
	for i := range items {
		if domainContextItem(items[i]) {
			items[i].DomainBudgetBasis = basis
		}
	}
	return err
}

// New selections are admitted together at binding. Legacy selections or changed
// accounting can still overflow: keep their exact locators, then fit pinned
// bodies in authored order before optional default orientation sections.
func boundGuidanceItems(ctx context.Context, count memory.TokenCounter, items []projectContextItem) (bool, error) {
	err := admitGuidanceItems(ctx, count, items)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, memory.ErrDomainBudget) {
		return false, err
	}
	original := append([]projectContextItem(nil), items...)
	for i := range items {
		if domainContextItem(items[i]) {
			items[i].Body, items[i].Links = "", nil
			if original[i].Body != "" {
				items[i].Status = "omitted-budget"
			}
		}
	}
	if err := admitGuidanceItems(ctx, count, items); err != nil {
		return false, fmt.Errorf("domain guidance locator inventory cannot fit; select fewer sections: %w", err)
	}
	omitted := false
	for _, pinned := range []bool{true, false} {
		for i := range items {
			if !domainContextItem(original[i]) || original[i].Body == "" || (original[i].Selection == "pinned") != pinned {
				continue
			}
			placeholder := items[i]
			items[i] = original[i]
			if err := admitGuidanceItems(ctx, count, items); err != nil {
				items[i] = placeholder
				if !errors.Is(err, memory.ErrDomainBudget) {
					return false, err
				}
				omitted = true
			}
		}
	}
	return omitted, nil
}
