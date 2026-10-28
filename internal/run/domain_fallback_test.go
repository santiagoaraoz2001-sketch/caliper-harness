package run

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

func TestProjectDomainFallbackDisclosesEmittedBasisAndPreservesFacts(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	fact := strings.Repeat("Current identity café🚀. ", 200)
	if _, err = memory.PutKnowledge(t.Context(), s, root, "test", "project/design/identity", fact, "", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []string{"", llm.RuntimeOpenAI, llm.RuntimeMLX, llm.RuntimeOllama} {
		t.Run(runtime, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Testing.Guidance = true
			var c session.Continuation
			counter := modelTokenCounter(llm.New("http://127.0.0.1:1", "unused").WithRuntime(runtime))
			if err := projectContext(cfg, s, root, counter)(t.Context(), &c, ""); err != nil {
				t.Fatal(err)
			}
			packet := contextPacket(t, &c)
			if packet.Items[0].Body != fact || packet.Items[0].DomainBudgetBasis != "" || packet.Items[0].Status != "supplied" {
				t.Fatal("ordinary owner facts changed")
			}
			supplied := 0
			for _, item := range packet.Items {
				if item.Source != "caliper-default" {
					continue
				}
				if item.DomainBudgetBasis != memory.DomainUTF8Bytes {
					t.Fatalf("capability basis missing: %+v", item)
				}
				if item.Status == "supplied" {
					supplied++
					raw, _ := json.Marshal(item)
					if len(raw) > memory.DomainTokens || item.Body == "" {
						t.Fatalf("uncharged actual item: %d", len(raw))
					}
				}
			}
			if supplied == 0 {
				t.Fatal("known unsupported provider lost all compact domain entry guidance")
			}
		})
	}
}

func TestSelectedDomainFallbackIncludesLabelsAndLinksInBound(t *testing.T) {
	count := modelTokenCounter(llm.New("http://127.0.0.1:1", "unused").WithRuntime(llm.RuntimeMLX))
	item := projectContextItem{Key: "project/guides/ui/shape", ID: "immutable-v1", Source: "project-authored", Status: "supplied", Section: "shape", Selection: "pinned", Body: "漢🚀"}
	if err := admitGuidanceItem(t.Context(), count, &item); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(item)
	item.Body += strings.Repeat("x", memory.DomainTokens-len(raw))
	if err := admitGuidanceItem(t.Context(), count, &item); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(item)
	if len(raw) != memory.DomainTokens {
		t.Fatal("wrong full selected envelope boundary")
	}
	item.Links = []memory.Reference{{Key: "caliper/guides/ui/layout", Section: "clearance", Label: "Useful next hop"}}
	if err := admitGuidanceItem(t.Context(), count, &item); err == nil {
		t.Fatal("selected guidance links escaped budget")
	}
}
