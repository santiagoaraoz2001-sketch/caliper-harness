package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

func TestMainProjectRecallArchiveChangedUnchangedAndClear(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	identity, err := memory.PutKnowledge(t.Context(), s, root, "author", "project/design/identity", "palette amber "+strings.Repeat("é", 8000), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Decision.Disabled, cfg.Recall.MaxChars = true, 240
	// The initial identity exceeds the ordinary source byte budget, not the
	// domain token limit or the independent topical-recall character budget.
	cfg.Tools.GraphQL.MaxResultBytes = 8192
	dir := t.TempDir()
	r, _, err := New(Deps{Cfg: cfg, Store: s, Server: "http://unused", Model: "capture", Session: &codingSink{dir: dir}, SessionID: "recall-events", In: strings.NewReader(""), Out: io.Discard}, "palette")
	if err != nil {
		t.Fatal(err)
	}
	model := &recalledRequest{}
	r.Model = model
	_, _, _ = r.Run(t.Context(), "palette")
	if len(model.messages) == 0 {
		t.Fatal("first actual request was not captured", r.Outcome)
	}
	captured := session.Continuation{}
	for _, message := range model.messages {
		captured.Entries = append(captured.Entries, session.Entry{Message: message})
	}
	first := contextPacket(t, &captured).Recall
	if first == "" || r.Memories != "" {
		t.Fatal("single-packet recall was not supplied")
	}
	for i := 0; i < 2; i++ {
		if err := r.ProjectContext(t.Context(), &r.Continuation, ""); err != nil {
			t.Fatal(err)
		}
	}
	// Rebuilding injected context after compaction must not repeat the event.
	r.Continuation = r.Continuation.Snapshot()
	if err := r.ProjectContext(t.Context(), &r.Continuation, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.PutKnowledge(t.Context(), s, root, "author", identity.Key, "palette amber", identity.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.ProjectContext(t.Context(), &r.Continuation, ""); err != nil || contextPacket(t, &r.Continuation).Recall != "" {
		t.Fatal("new supplied head did not clear redundant recall", err)
	}
	events, err := session.Read(dir, "recall-events")
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, event := range events {
		if event.Kind == "recall" {
			texts = append(texts, event.Text)
		}
	}
	if len(texts) != 2 || texts[0] != first || texts[1] != "" {
		t.Fatalf("changed/unchanged/clear recall event contract: %#v", texts)
	}
}

type refuseRecallArchive struct{ dir string }

func (s refuseRecallArchive) Append(_ context.Context, id, kind, text string) error {
	if kind == "recall" {
		return fmt.Errorf("controlled recall archive failure")
	}
	return session.Append(s.dir, id, session.Event{Kind: kind, Text: text})
}

func TestMainProjectRecallArchiveFailurePreventsProvider(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := memory.PutKnowledge(t.Context(), s, root, "author", "project/features/map", "palette map selection", "", "", ""); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Decision.Disabled = true
	r, _, err := New(Deps{Cfg: cfg, Store: s, Server: "http://unused", Model: "capture", Session: refuseRecallArchive{dir: t.TempDir()}, SessionID: "refused", In: strings.NewReader(""), Out: io.Discard}, "palette")
	if err != nil {
		t.Fatal(err)
	}
	model := &recalledRequest{}
	r.Model = model
	_, _, err = r.Run(t.Context(), "palette")
	if err == nil || !strings.Contains(err.Error(), "controlled recall archive failure") || len(model.messages) != 0 {
		t.Fatalf("unarchived recall reached provider: error=%v messages=%d", err, len(model.messages))
	}
}

func TestProjectRecallRetainsOnlyBoundedSelectedFragments(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 25; i++ {
		if _, err := memory.PutKnowledge(t.Context(), s, root, "author", fmt.Sprintf("project/notes/%d", i), strings.Repeat("é", 2000)+" palette", "", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Defaults()
	cfg.Recall.TopN, cfg.Recall.MaxChars = 3, 240
	recall := &projectRecall{query: "palette"}
	c := session.Continuation{Version: 1}
	project := projectContext(cfg, s, root, fixtureTokenCounter, recall)
	if err := project(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	if len(recall.records) == 0 || len(recall.records) > cfg.Recall.TopN || utf8.RuneCountInString(recall.text) > cfg.Recall.MaxChars {
		t.Fatalf("unbounded retained selection: count%d text%d", len(recall.records), utf8.RuneCountInString(recall.text))
	}
	retained := 0
	for _, record := range recall.records {
		retained += utf8.RuneCountInString(record.Entry.Body)
		if strings.Contains(record.Entry.Body, "palette") {
			t.Fatal("fixture did not place matched word beyond retained body")
		}
	}
	if retained > cfg.Recall.MaxChars {
		t.Fatal("unbounded candidate bodies retained", retained)
	}
	before := recall.text
	s.Close()
	if err := project(t.Context(), &c, ""); err != nil || recall.text != before || recall.status != "cached-unavailable" {
		t.Fatalf("bounded fallback reranked away its original match: %q %q %v", recall.text, recall.status, err)
	}
}

func TestProjectRecallUnavailableFallsBackToPreviouslySuppliedFact(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body := "palette previously supplied amber"
	if _, err := memory.PutKnowledge(t.Context(), s, root, "author", "project/design/identity", body, "", "", ""); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	recall := &projectRecall{query: "palette"}
	project := projectContext(cfg, s, root, fixtureTokenCounter, recall)
	c := session.Continuation{Version: 1}
	if err := project(t.Context(), &c, ""); err != nil || recall.text != "" || len(recall.records) != 0 {
		t.Fatal("current fact also retained as automatic recall", err)
	}
	s.Close()
	if err := project(t.Context(), &c, ""); err != nil || !strings.Contains(recall.text, body) || recall.status != "cached-unavailable" {
		t.Fatalf("unavailable projection lost previously supplied bounded fact: %q %q %v", recall.text, recall.status, err)
	}
}

// Shipped CLI startup opens a store first; this checks helper consistency with
// the existing unavailable projection, not an observed production crash.
func TestProjectRecallNilStoreIsUnavailableWithoutRepeatedWarning(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%t", fallback), func(t *testing.T) {
			root := t.TempDir()
			cfg := config.Defaults()
			warnings := 0
			recall := &projectRecall{query: "palette", warn: func(error) { warnings++ }}
			project := projectContext(cfg, nil, root, fixtureTokenCounter, recall)
			c := session.Continuation{Version: 1}
			body := "palette previously supplied amber"
			if fallback {
				previous := projectContextPacket{Project: root, Items: []projectContextItem{{Key: "project/design/identity", ID: "captured-head", Source: "project-authored", Status: "supplied", Body: body}}}
				raw, err := json.Marshal(previous)
				if err != nil {
					t.Fatal(err)
				}
				c.Add("injected", "user", projectContextMarker+string(raw), false)
			}
			if err := project(t.Context(), &c, ""); err != nil {
				t.Fatal("nil store failed optional recall", err)
			}
			packet := contextPacket(t, &c)
			status := "unavailable"
			if fallback {
				status = "cached-unavailable"
			}
			if packet.RecallStatus != status || strings.Contains(packet.Recall, body) != fallback || warnings != 1 {
				t.Fatalf("nil-store status/fallback/warning: %+v warnings%d", packet, warnings)
			}
			revision := c.Revision
			if err := project(t.Context(), &c, ""); err != nil || warnings != 1 || c.Revision != revision {
				t.Fatalf("unchanged nil-store recall retried: %v warnings%d revision%d", err, warnings, c.Revision)
			}
		})
	}
	// Disabled automatic recall does not report a query failure for no query.
	cfg := config.Defaults()
	cfg.Recall.TopN = 0
	warnings := 0
	c := session.Continuation{Version: 1}
	if err := projectContext(cfg, nil, t.TempDir(), fixtureTokenCounter, &projectRecall{query: "palette", warn: func(error) { warnings++ }})(t.Context(), &c, ""); err != nil || contextPacket(t, &c).RecallStatus != "" || warnings != 0 {
		t.Fatal("disabled recall reported a store failure", err, warnings)
	}
}
