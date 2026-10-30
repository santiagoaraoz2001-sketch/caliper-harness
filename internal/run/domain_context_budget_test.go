package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
)

type domainContextCountModel struct {
	agent.Model
	count memory.TokenCounter
}

func (m domainContextCountModel) CountTokens(ctx context.Context, raw string) (int, error) {
	return m.count(ctx, raw)
}

func TestDomainContextAggregatePrefersPinnedOrderAndExemptsOrdinaryFacts(t *testing.T) {
	ordinary := projectContextItem{Key: "project/design/identity", Source: "project-authored", Status: "supplied", Body: strings.Repeat("EXACT_OWNER_FACT ", 400)}
	items := []projectContextItem{ordinary}
	for i := 0; i < 3; i++ {
		items = append(items, projectContextItem{Key: fmt.Sprintf("project/guides/custom/topic%d", i), ID: fmt.Sprintf("version%d", i), Section: "shape", Source: "project-authored", Status: "supplied", Selection: "pinned", Body: strings.Repeat(fmt.Sprint(i), 600)})
	}
	items = append(items, projectContextItem{Key: memory.CodeStandardKey, ID: "default-version", Section: "scope", Source: "caliper-default", Status: "supplied", Body: strings.Repeat("default", 100)})
	calls := 0
	count := func(_ context.Context, raw string) (int, error) {
		calls++
		if strings.Contains(raw, "EXACT_OWNER_FACT") {
			t.Fatal("ordinary facts charged")
		}
		return len(raw), nil
	}
	omitted, err := boundGuidanceItems(t.Context(), count, items)
	if err != nil || !omitted || !reflect.DeepEqual(items[0], ordinary) {
		t.Fatalf("ordinary fact changed or fallback failed: %v", err)
	}
	if items[1].Status != "supplied" || items[1].Body == "" || items[2].Status != "supplied" || items[3].Status != "omitted-budget" || items[4].Status != "omitted-budget" {
		t.Fatalf("authored priority lost: %+v", items[1:])
	}
	for i := 3; i < len(items); i++ {
		if items[i].Key == "" || items[i].ID == "" || items[i].Section == "" || items[i].Body != "" {
			t.Fatal("omitted locator lost", items[i])
		}
	}
	if err := admitGuidanceItems(t.Context(), count, items); err != nil {
		t.Fatal("final aggregate unbounded", err)
	}
	if calls < 3 {
		t.Fatal("overflow fallback did not count its emitted envelope")
	}
	// A normal compact packet adds exactly one aggregate count.
	calls = 0
	compact := []projectContextItem{{Key: memory.CodeStandardKey, Source: "caliper-default", Status: "supplied", Body: "scope"}}
	if omitted, err := boundGuidanceItems(t.Context(), count, compact); err != nil || omitted || calls != 1 {
		t.Fatalf("normal path calls=%d omitted=%v err=%v", calls, omitted, err)
	}
}

func TestDomainContextRefusesLocatorOverflowAndPreservesCountFailures(t *testing.T) {
	items := []projectContextItem{}
	for i := 0; i < 20; i++ {
		items = append(items, projectContextItem{Key: fmt.Sprintf("project/guides/custom/%s%d", strings.Repeat("x", 150), i), ID: "version", Section: "shape", Selection: "pinned", Status: "supplied", Body: "body"})
	}
	if _, err := boundGuidanceItems(t.Context(), func(_ context.Context, raw string) (int, error) { return len(raw), nil }, items); !errors.Is(err, memory.ErrDomainBudget) || !strings.Contains(err.Error(), "locator inventory") {
		t.Fatalf("oversized locators accepted: %v", err)
	}
	want := errors.New("native tokenizer failed")
	calls := 0
	if _, err := boundGuidanceItems(t.Context(), func(context.Context, string) (int, error) { calls++; return 0, want }, items); !errors.Is(err, want) || calls != 1 {
		t.Fatalf("tokenizer failure retried or swallowed: calls=%d err=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := boundGuidanceItems(ctx, func(context.Context, string) (int, error) { t.Fatal("cancelled count invoked"); return 0, nil }, items); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	calls = 0
	legacy := []projectContextItem{{Key: "project/guides/custom/topic", ID: "version", Section: "shape", Status: "supplied", Selection: "pinned", Body: "body"}}
	if _, err := boundGuidanceItems(t.Context(), func(context.Context, string) (int, error) {
		calls++
		if calls == 1 {
			return memory.DomainTokens + 1, nil
		}
		if calls == 2 {
			return 100, nil
		}
		return 0, want
	}, legacy); !errors.Is(err, want) || calls != 3 {
		t.Fatalf("fallback tokenizer failure swallowed: calls=%d err=%v", calls, err)
	}
}

func TestDomainContextCollectiveExactNativeBoundary(t *testing.T) {
	for _, n := range []int{memory.DomainTokens - 1, memory.DomainTokens, memory.DomainTokens + 1} {
		items := []projectContextItem{{Key: "project/design/identity", Body: "ordinary owner fact"}, {Key: "project/guides/custom/topic", ID: "version", Section: "shape", Selection: "pinned", Status: "supplied", Body: "漢🚀", Links: []memory.Reference{{Key: "project/guides/custom/deep", Section: "example", Label: "Useful next hop"}}}}
		calls := 0
		err := admitGuidanceItems(t.Context(), func(_ context.Context, raw string) (int, error) {
			calls++
			for _, want := range []string{`"items":`, `"Useful next hop"`, `"section":"shape"`, `"domainBudgetBasis":"native-token-ids"`} {
				if !strings.Contains(raw, want) {
					t.Fatal("missing collective metadata", raw)
				}
			}
			if strings.Contains(raw, "ordinary owner fact") {
				t.Fatal("ordinary owner charged")
			}
			return n, nil
		}, items)
		if calls != 1 || (err != nil) != (n > memory.DomainTokens) {
			t.Fatalf("count=%d calls=%d err=%v", n, calls, err)
		}
	}
}

func TestDomainSelectionAggregateRefusedBeforePlanningCommit(t *testing.T) {
	f := newPlanningFixture(t)
	f.d.KnowledgeLimits = config.Defaults().Tools.GraphQL
	f.d.NewModel = func() agent.Model {
		return domainContextCountModel{count: func(_ context.Context, raw string) (int, error) { return len(raw), nil }}
	}
	locators := []string{}
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("project/guides/custom/topic%d", i)
		if _, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", key, "## shape\n"+strings.Repeat("x", 600), "", "", ""); err != nil {
			t.Fatal(err)
		}
		locators = append(locators, "knowledge:"+key+"#shape")
	}
	if _, err := planningGuidance(t.Context(), f.d, strings.Join(locators, "\n")); !errors.Is(err, memory.ErrDomainBudget) {
		t.Fatalf("collective overbudget selection bound: %v", err)
	}
	plans, err := plan.ListPlans(f.d.Store, "")
	if err != nil || len(plans) != 0 {
		t.Fatal("selection admission changed planning store", err)
	}
}

func TestSelectedDomainEnrichedFallbackUsesFetchedPreviousSectionLineage(t *testing.T) {
	f := newPlanningFixture(t)
	key := "project/guides/custom/shape"
	first, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", key, "## shape\nFirst version.", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", key, "## shape\n漢🚀\n## new section\nNew advice.", first.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := guidanceItem(t.Context(), config.Defaults(), f.d.Store, f.d.Workspace, plan.GuidanceRef{Key: key, Version: second.ID, Section: "shape"})
	if err != nil || item.domainLimit != memory.DomainTokens {
		t.Fatalf("previousId provenance not loaded: limit=%d err=%v", item.domainLimit, err)
	}
	item.Selection = "pinned"
	count := modelTokenCounter(llm.New("http://127.0.0.1:1", "unused").WithRuntime(llm.RuntimeMLX))
	if err := admitGuidanceItem(t.Context(), count, &item); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(item)
	item.Body += strings.Repeat("x", memory.DomainTokens-len(raw))
	if err := admitGuidanceItem(t.Context(), count, &item); err != nil {
		t.Fatal("enriched boundary refused", err)
	}
	item.Body += "x"
	if err := admitGuidanceItem(t.Context(), count, &item); !errors.Is(err, memory.ErrDomainBudget) {
		t.Fatal("enriched boundary escaped", err)
	}
	newItem, err := guidanceItem(t.Context(), config.Defaults(), f.d.Store, f.d.Workspace, plan.GuidanceRef{Key: key, Version: second.ID, Section: "new-section"})
	if err != nil || newItem.domainLimit != memory.DomainInitialTokens {
		t.Fatalf("new section inherited expanded allowance: %d %v", newItem.domainLimit, err)
	}
}

func TestDomainContextLegacySelectionRetainsBoundedPinnedBodiesAndLocators(t *testing.T) {
	f := newPlanningFixture(t)
	fact := strings.Repeat("EXACT_CURRENT_OWNER ", 400)
	if _, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", "project/design/identity", fact, "", "", ""); err != nil {
		t.Fatal(err)
	}
	refs := []plan.GuidanceRef{}
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("project/guides/custom/topic%d", i)
		entry, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", key, "## shape\n"+strings.Repeat(fmt.Sprint(i), 600), "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, plan.GuidanceRef{Key: key, Version: entry.ID, Section: "shape"})
	}
	p, err := plan.CreatePlan(f.d.Store, "legacy", selectedGuidanceDocument(t, refs))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.BindProject(t.Context(), f.d.Store, p.ID, f.d.Workspace); err != nil {
		t.Fatal(err)
	}
	c := session.Continuation{Workflow: &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "plan", ID: p.ID}}
	count := func(_ context.Context, raw string) (int, error) { return len(raw), nil }
	if err := projectContext(config.Defaults(), f.d.Store, f.d.Workspace, count)(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	packet := contextPacket(t, &c)
	items := pinnedGuidance(t, packet)
	if len(items) != 3 || items[0].Status != "supplied" || items[0].Body == "" || packet.GuidanceStatus == "" {
		t.Fatalf("legacy selection silently lost bodies/status: %+v", packet)
	}
	if err := admitGuidanceItems(t.Context(), count, packet.Items); err != nil {
		t.Fatal("emitted aggregate escaped", err)
	}
	if packet.Items[0].Body != fact || packet.Items[0].Status != "supplied" {
		t.Fatal("aggregate domain bound changed current owner fact")
	}
	for _, item := range packet.Items {
		if item.Source == "caliper-default" && item.Selection != "pinned" {
			t.Fatal("generic roots crowded out explicit selected guidance")
		}
	}
	for i, item := range items {
		if item.ID != refs[i].Version || item.Key != refs[i].Key || item.Section != refs[i].Section {
			t.Fatal("legacy locator changed", item)
		}
	}
}

func TestDomainContextAdmissionFailurePreservesExactOrdinaryFacts(t *testing.T) {
	f := newPlanningFixture(t)
	facts := map[string]string{
		"project/design/identity":  strings.Repeat("Current identity café🚀. ", 160),
		"project/testing/strategy": strings.Repeat("Current strategy: assert exact public behavior. ", 100),
	}
	ids := map[string]string{}
	for key, body := range facts {
		entry, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", key, body, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		ids[key] = entry.ID
	}
	cfg := config.Defaults()
	cfg.Testing.Guidance = true
	for _, failure := range []string{"native-count-unavailable", "native-count-timeout", "missing-counter", "locator-budget"} {
		t.Run(failure, func(t *testing.T) {
			var count memory.TokenCounter
			if failure != "missing-counter" {
				count = func(_ context.Context, raw string) (int, error) {
					for _, body := range facts {
						if strings.Contains(raw, body) {
							t.Fatal("ordinary current facts charged against domain allowance")
						}
					}
					if failure == "locator-budget" {
						return memory.DomainTokens + 1, nil
					}
					if failure == "native-count-timeout" {
						return 0, context.DeadlineExceeded
					}
					return 0, errors.New("native count unavailable")
				}
			}
			var c session.Continuation
			if err := projectContext(cfg, f.d.Store, f.d.Workspace, count)(t.Context(), &c, ""); err != nil {
				t.Fatal("domain failure withheld ordinary facts", err)
			}
			packet := contextPacket(t, &c)
			if len(packet.Items) != len(facts) || !strings.Contains(packet.GuidanceStatus, "domain items omitted:") {
				t.Fatalf("domain failure not explicit: %+v", packet)
			}
			for _, item := range packet.Items {
				if domainContextItem(item) {
					t.Fatal("uncounted domain payload delivered", item)
				}
				if item.ID != ids[item.Key] || item.Body != facts[item.Key] || item.Status != "supplied" || item.DomainBudgetBasis != "" {
					t.Fatal("ordinary fact was changed", item.Key, item.ID, item.Status)
				}
			}
		})
	}
}

func TestDomainContextCancellationStillStopsOrdinaryProjection(t *testing.T) {
	f := newPlanningFixture(t)
	if _, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", "project/design/identity", "Current owner fact.", "", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, duringCount := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		if !duringCount {
			cancel()
		}
		var c session.Continuation
		err := projectContext(config.Defaults(), f.d.Store, f.d.Workspace, func(context.Context, string) (int, error) { cancel(); return 0, context.Canceled })(ctx, &c, "")
		cancel()
		if !errors.Is(err, context.Canceled) || len(c.Entries) != 0 {
			t.Fatalf("cancellation suppressed or ordinary facts injected: duringCount=%v err=%v entries=%d", duringCount, err, len(c.Entries))
		}
	}
}
