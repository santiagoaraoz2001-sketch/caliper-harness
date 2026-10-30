package run

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
)

func ownerPinContinuation(t *testing.T, f *planningFixture, refs []plan.GuidanceRef) session.Continuation {
	t.Helper()
	p, err := plan.CreatePlanContext(plan.WithProject(t.Context(), f.d.Workspace), f.d.Store, "owner pins", selectedGuidanceDocument(t, refs))
	if err != nil {
		t.Fatal(err)
	}
	return session.Continuation{Workflow: &session.WorkflowScope{Store: f.d.Store.Identity(), Kind: "plan", ID: p.ID}}
}

func ordinaryOwnerCounter(t *testing.T) memory.TokenCounter {
	t.Helper()
	return func(_ context.Context, raw string) (int, error) {
		if strings.Contains(raw, "ORDINARY_BODY") {
			t.Fatal("ordinary owner charged against domain budget")
		}
		return 100, nil
	}
}

func TestProjectContextSameVersionOwnerPinsKeepWholeBodyOnceAcrossRefresh(t *testing.T) {
	f := newPlanningFixture(t)
	type ownerFixture struct {
		key, section, body string
		entry              *memory.Entry
		link               memory.Reference
	}
	owners := []ownerFixture{
		{key: "project/design/identity", section: "direction", body: "## Direction\nORDINARY_BODY identity direction.\n## Accessibility\nORDINARY_BODY identity accessibility must remain.\n", link: memory.Reference{Key: "project/guides/ui/deeper", Relation: "explains", SourceSection: "direction", Section: "method", Label: "Useful design hop"}},
		{key: "project/testing/strategy", section: "approach", body: "## Approach\nORDINARY_BODY testing approach.\n## Preservation\nORDINARY_BODY testing preservation must remain.\n", link: memory.Reference{Key: "project/guides/tests/deeper", Relation: "explains", SourceSection: "approach", Section: "oracle", Label: "Useful test hop"}},
	}
	refs := []plan.GuidanceRef{}
	for i := range owners {
		owner := &owners[i]
		var err error
		owner.entry, err = memory.PutKnowledgeReferences(t.Context(), f.d.Store, f.d.Workspace, "user", owner.key, owner.body, "", "", "", []memory.Reference{owner.link})
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, plan.GuidanceRef{Key: owner.key, Version: owner.entry.ID, Section: owner.section})
	}
	for _, cache := range []string{"fresh", "legacy-duplicate", "pinned-subsection-only"} {
		t.Run(cache, func(t *testing.T) {
			c := ownerPinContinuation(t, f, refs)
			if cache != "fresh" {
				previous := projectContextPacket{Project: f.d.Workspace}
				for _, owner := range owners {
					if cache == "legacy-duplicate" {
						previous.Items = append(previous.Items, projectContextItem{Key: owner.key, ID: owner.entry.ID, Source: "project-authored", Status: "supplied", Body: owner.body})
					}
					previous.Items = append(previous.Items, projectContextItem{Key: owner.key, ID: owner.entry.ID, Section: owner.section, Source: "project-authored", Selection: "pinned", Status: "supplied", Body: memory.Sections(owner.body, "", owner.section)[0].Body})
				}
				raw, _ := json.Marshal(previous)
				c.Add("injected", "user", projectContextMarker+string(raw), false)
			}
			project := projectContext(config.Defaults(), f.d.Store, f.d.Workspace, ordinaryOwnerCounter(t))
			for refresh := 0; refresh < 2; refresh++ {
				before := c.Revision
				if err := project(t.Context(), &c, ""); err != nil {
					t.Fatal(err)
				}
				packet := contextPacket(t, &c)
				for _, owner := range owners {
					foundOwner, foundPin := false, false
					for _, item := range packet.Items {
						if item.Key != owner.key {
							continue
						}
						if item.Selection == "" {
							foundOwner = true
							if item.ID != owner.entry.ID || item.Status != "supplied" || item.Section != "" || item.Body != owner.body {
								t.Fatalf("whole current owner changed on refresh%d: %+v", refresh, item)
							}
						} else {
							foundPin = true
							if item.ID != owner.entry.ID || item.Section != owner.section || item.Selection != "pinned" || item.Status != "supplied-in-owner" || item.Body != "" || !reflect.DeepEqual(item.Links, []memory.Reference{owner.link}) {
								t.Fatalf("pin metadata/link lost or body duplicated: %+v", item)
							}
						}
					}
					if !foundOwner || !foundPin {
						t.Fatal("owner/pin locator missing", owner.key)
					}
				}
				messages, _ := json.Marshal(c.Messages())
				for _, marker := range []string{"ORDINARY_BODY identity direction.", "ORDINARY_BODY identity accessibility must remain.", "ORDINARY_BODY testing approach.", "ORDINARY_BODY testing preservation must remain."} {
					if strings.Count(string(messages), marker) != 1 {
						t.Fatalf("body not delivered exactly once on refresh%d: %s", refresh, marker)
					}
				}
				if refresh == 1 && c.Revision != before {
					t.Fatal("unchanged deduplicated packet changed continuation")
				}
			}
		})
	}
}

func TestProjectContextHistoricalOwnerPinKeepsBodyWhenCurrentDiffersOrIsUnavailable(t *testing.T) {
	for _, largeCurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "different-version", true: "automatic-body-unavailable"}[largeCurrent], func(t *testing.T) {
			f := newPlanningFixture(t)
			key := "project/design/identity"
			oldBody := "## Direction\nORDINARY_BODY historical selected direction.\n## Other\nUnselected historical content stays lazy."
			old, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", key, oldBody, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			currentBody := "## Current\nORDINARY_BODY current authority.\n## Other\nCurrent preservation facts."
			if largeCurrent {
				currentBody += strings.Repeat(" ordinary large current fact", 1000)
			}
			current, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", key, currentBody, old.ID, "", "")
			if err != nil {
				t.Fatal(err)
			}
			c := ownerPinContinuation(t, f, []plan.GuidanceRef{{Key: key, Version: old.ID, Section: "direction"}})
			cfg := config.Defaults()
			if largeCurrent {
				cfg.Tools.GraphQL.MaxResultBytes = 8192
			}
			project := projectContext(cfg, f.d.Store, f.d.Workspace, ordinaryOwnerCounter(t))
			for refresh := 0; refresh < 2; refresh++ {
				if err := project(t.Context(), &c, ""); err != nil {
					t.Fatal(err)
				}
				packet := contextPacket(t, &c)
				foundOwner, foundPin := false, false
				for _, item := range packet.Items {
					if item.Key == key {
						if item.Selection == "pinned" {
							foundPin = true
							if item.ID != old.ID || item.Body != memory.Sections(oldBody, "", "direction")[0].Body || item.Status != "supplied" {
								t.Fatal("historical pin suppressed", item)
							}
						} else {
							foundOwner = true
							if item.ID != current.ID {
								t.Fatal("current owner replaced by historical pin")
							}
							if largeCurrent {
								if item.Status != "omitted-budget" || item.Body != "" {
									t.Fatal("ordinary source budget misreported")
								}
							} else if item.Status != "supplied" || item.Body != currentBody {
								t.Fatal("current full authority lost")
							}
						}
					}
				}
				if !foundOwner || !foundPin {
					t.Fatal("distinct versions not both retained")
				}
				messages, _ := json.Marshal(c.Messages())
				if strings.Contains(string(messages), "Unselected historical content stays lazy.") {
					t.Fatal("historical sibling preloaded")
				}
			}
		})
	}
}

func TestProjectContextOrdinaryPinWithoutAutomaticOwnerKeepsSelectedBody(t *testing.T) {
	f := newPlanningFixture(t)
	entry, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", "project/features/status", "## Chosen\nORDINARY_BODY necessary selected fact.\n## Other\nUnselected sibling.", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	c := ownerPinContinuation(t, f, []plan.GuidanceRef{{Key: entry.Key, Version: entry.ID, Section: "chosen"}})
	if err := projectContext(config.Defaults(), f.d.Store, f.d.Workspace, ordinaryOwnerCounter(t))(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	items := pinnedGuidance(t, contextPacket(t, &c))
	if len(items) != 1 || items[0].Body != memory.Sections(entry.Body, "", "chosen")[0].Body || items[0].Status != "supplied" {
		t.Fatal("pin without full automatic owner was suppressed", items)
	}
}

func TestProjectContextPacketOverflowDoesNotClaimDeduplicatedOwnerSupplied(t *testing.T) {
	f := newPlanningFixture(t)
	body := "## Direction\nORDINARY_BODY compact current identity.\n## Other\nPreserved current owner fact."
	entry, err := memory.PutKnowledge(t.Context(), f.d.Store, f.d.Workspace, "user", "project/design/identity", body, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	refs := []plan.GuidanceRef{{Key: entry.Key, Version: entry.ID, Section: "direction"}}
	// Establish that this exact owner and same-version pin fit individually.
	// The later packet limit includes the generic roots and every locator too.
	roomy := ownerPinContinuation(t, f, refs)
	cfg := config.Defaults()
	cfg.Testing.Guidance = true
	if err := projectContext(cfg, f.d.Store, f.d.Workspace, ordinaryOwnerCounter(t))(t.Context(), &roomy, ""); err != nil {
		t.Fatal(err)
	}
	full := contextPacket(t, &roomy)
	pins := pinnedGuidance(t, full)
	if full.Items[0].Body != body || len(pins) != 1 || pins[0].Status != "supplied-in-owner" {
		t.Fatal("fixture did not establish supplied ordinary owner and reused pin")
	}
	fullBytes, _ := json.Marshal(full)
	stripped := full
	stripped.Items = append([]projectContextItem(nil), full.Items...)
	for i := range stripped.Items {
		if stripped.Items[i].Status == "supplied" || stripped.Items[i].Status == "supplied-in-owner" {
			stripped.Items[i].Status = "omitted-budget"
		}
		stripped.Items[i].Body = ""
		stripped.Items[i].Links = nil
	}
	metadataBytes, _ := json.Marshal(stripped)
	cfg.Tools.GraphQL.MaxResultBytes = len(metadataBytes) + 32
	if cfg.Tools.GraphQL.MaxResultBytes >= len(fullBytes) {
		t.Fatal("fixture packet does not overflow")
	}
	if owners, err := memory.Knowledge(t.Context(), f.d.Store, f.d.Workspace, "", entry.ID, 1, cfg.Tools.GraphQL.MaxResultBytes, true, []string{}); err != nil || len(owners) != 1 || owners[0].Body != body {
		t.Fatal("ordinary owner did not fit before aggregate packet projection", err)
	}
	c := ownerPinContinuation(t, f, refs)
	if err := projectContext(cfg, f.d.Store, f.d.Workspace, ordinaryOwnerCounter(t))(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	packet := contextPacket(t, &c)
	if len(packet.Items) == 0 {
		t.Fatal("locator metadata was unnecessarily discarded")
	}
	foundOwner, foundPin := false, false
	for _, item := range packet.Items {
		if item.Status == "supplied" || item.Status == "supplied-in-owner" {
			t.Fatal("packet claims unavailable body was supplied", item.Key, item.Status)
		}
		if item.Body != "" {
			t.Fatal("packet overflow left a body")
		}
		if item.Key == entry.Key {
			if item.ID != entry.ID || item.Status != "omitted-budget" {
				t.Fatal("owner locator/status lost", item)
			}
			if item.Selection == "pinned" {
				foundPin = true
				if item.Section != "direction" {
					t.Fatal("selected anchor lost")
				}
			} else {
				foundOwner = true
			}
		}
	}
	if !foundOwner || !foundPin {
		t.Fatal("ordinary owner or selected locator disappeared")
	}
	raw, _ := json.Marshal(packet)
	if len(raw) > cfg.Tools.GraphQL.MaxResultBytes {
		t.Fatal("emitted packet exceeds final byte limit")
	}
}
