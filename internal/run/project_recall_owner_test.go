package run

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

func TestProjectPacketOwnsCurrentRecallWithoutChangingHistory(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	put := func(scope, key, body, previous string) *memory.Entry {
		t.Helper()
		e, err := memory.PutKnowledge(t.Context(), s, scope, "author", key, body, previous, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	old := put(root, "project/design/identity", "palette copper current", "")
	other := put(root, "project/features/navigation", "palette navigation detail", "")
	put(t.TempDir(), "project/design/identity", "palette FOREIGN", "")
	global, err := memory.Add(s, "decision", "palette legacy decision", "old-session")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Recall.TopN, cfg.Recall.MaxChars = 10, 4000
	recall := &projectRecall{query: "palette"}
	project := projectContext(cfg, s, root, fixtureTokenCounter, recall)
	historical := session.Entry{Kind: "output", Message: llm.Message{Role: "tool", ToolCallID: "prior-read", Name: "graphql", Content: old.Body}}
	c := session.Continuation{Version: 1, Entries: []session.Entry{historical}}
	if err := project(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	packet := contextPacket(t, &c)
	if packet.Items[0].ID != old.ID || packet.Items[0].Body != old.Body || strings.Contains(packet.Recall, old.Body) {
		t.Fatalf("current identity has multiple owners: %+v", packet)
	}
	for _, body := range []string{other.Body, global.Body} {
		if !strings.Contains(packet.Recall, body) {
			t.Fatalf("unrelated recall lost %q: %s", body, packet.Recall)
		}
	}
	before := c.Snapshot()
	revision := c.Revision
	if err := project(t.Context(), &c, ""); err != nil || c.Revision != revision {
		t.Fatalf("unchanged context was republished: %v %d -> %d", err, revision, c.Revision)
	}
	if !reflect.DeepEqual(before.Entries, []session.Entry{historical}) {
		t.Fatal("publication removed or changed historical tool output", before.Entries)
	}
	current := put(root, old.Key, "palette olive current", old.ID)
	added := put(root, "project/features/status", "palette status detail", "")
	if err := project(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	packet = contextPacket(t, &c)
	if packet.Items[0].ID != current.ID || packet.Items[0].Body != current.Body || strings.Contains(packet.Recall, old.Body) || strings.Contains(packet.Recall, current.Body) || !strings.Contains(packet.Recall, added.Body) || strings.Contains(packet.Recall, "FOREIGN") {
		t.Fatalf("head refresh retained stale or duplicate facts: %+v", packet)
	}
	if !reflect.DeepEqual(c.Snapshot().Entries, []session.Entry{historical}) {
		t.Fatal("head refresh changed historical tool output")
	}
	// A role projection without the main-only owner cannot inherit topical recall.
	worker := session.Continuation{Version: 1}
	if err := projectContext(cfg, s, root, fixtureTokenCounter)(t.Context(), &worker, ""); err != nil || contextPacket(t, &worker).Recall != "" {
		t.Fatal("worker/reviewer projection gained main recall", err)
	}
}

func TestProjectRecallFallbackBudgetUnavailableAndCache(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	identity, err := memory.PutKnowledge(t.Context(), s, root, "author", "project/design/identity", "palette oversized "+strings.Repeat("é", 8000), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Recall.MaxChars, cfg.Recall.TopN = 240, 3
	// Canonical project facts do not inherit the topical recall character cap.
	full := session.Continuation{Version: 1}
	if err := projectContext(cfg, s, root, fixtureTokenCounter)(t.Context(), &full, ""); err != nil || contextPacket(t, &full).Items[0].Body != identity.Body {
		t.Fatal("recall cap suppressed the full authored identity", err)
	}
	// Exercise fallback through the independent ordinary source/packet byte
	// budget. Keep that budget large enough to retain identity metadata.
	cfg.Tools.GraphQL.MaxResultBytes = 8192
	warnings := 0
	recall := &projectRecall{query: "palette", warn: func(error) { warnings++ }}
	project := projectContext(cfg, s, root, fixtureTokenCounter, recall)
	c := session.Continuation{Version: 1}
	if err := project(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	packet := contextPacket(t, &c)
	if packet.Items[0].ID != identity.ID || packet.Items[0].Status != "omitted-budget" || !strings.Contains(packet.Recall, "palette oversized") || !strings.HasSuffix(packet.Recall, "…") || !utf8.ValidString(packet.Recall) || utf8.RuneCountInString(packet.Recall) > cfg.Recall.MaxChars {
		t.Fatalf("omitted whole head lost permitted bounded recall: %+v", packet)
	}
	s.Close()
	// With the exact same projection, the recall owner does not query again.
	if text, status, err := recall.project(t.Context(), cfg, s, root, packet, packet); err != nil || text != packet.Recall || status != "" || warnings != 0 {
		t.Fatalf("unchanged projection re-read unavailable store: %q %q warnings%d %v", text, status, warnings, err)
	}
	if err := project(t.Context(), &c, ""); err != nil {
		t.Fatal("recall availability incorrectly failed the run", err)
	}
	packet = contextPacket(t, &c)
	if packet.Items[0].Status != "unavailable" || packet.RecallStatus != "cached-unavailable" || !strings.Contains(packet.Recall, "palette oversized") || warnings != 1 {
		t.Fatalf("unavailable current projection erased captured recall or hid warning: %+v warnings%d", packet, warnings)
	}
	revision := c.Revision
	if err := project(t.Context(), &c, ""); err != nil || warnings != 1 || c.Revision != revision {
		t.Fatal("unchanged unavailable projection retried/republished recall", err, warnings)
	}
	// Even if refresh fails, a known newer head must not resurrect its retired ID.
	packet.Items[0].ID, packet.Items[0].Status = "new-current-id", "omitted-budget"
	if text, status, err := recall.project(t.Context(), cfg, s, root, packet, packet); err != nil || text != "" || status != "unavailable" || warnings != 2 {
		t.Fatalf("failed refresh reintroduced known old head: %q %q warnings%d %v", text, status, warnings, err)
	}
}
