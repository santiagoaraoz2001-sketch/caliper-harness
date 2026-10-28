package run

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

// These projection tests use an explicit tokenizer fixture; native exact counts
// and the 1000/1001 boundary are qualified separately.
func fixtureTokenCounter(context.Context, string) (int, error) { return 100, nil }

func contextPacket(t *testing.T, c *session.Continuation) projectContextPacket {
	t.Helper()
	var packet projectContextPacket
	count := 0
	for _, e := range c.Entries {
		if strings.HasPrefix(e.Message.Content, projectContextMarker) {
			count++
			if err := json.Unmarshal([]byte(strings.TrimPrefix(e.Message.Content, projectContextMarker)), &packet); err != nil {
				t.Fatal(err)
			}
		}
	}
	if count != 1 {
		t.Fatalf("want one current projection, got %d", count)
	}
	return packet
}

func TestProjectContextRefreshesScopedHeadsWithoutDuplicatingOrPreloading(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Defaults()
	cfg.Testing.Guidance = true
	project := projectContext(cfg, s, root, fixtureTokenCounter)
	c := session.Continuation{Version: 1}
	if err := project(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	first := contextPacket(t, &c)
	if first.Items[0].Status != "absent" || first.Items[1].Status != "absent" || len(first.Items) != 5 {
		t.Fatalf("fresh scope: %+v", first)
	}
	for _, item := range first.Items[2:] {
		wantHop := map[string]string{
			"caliper/standards/code":  "knowledge:caliper/guides/code/boundaries#ownership",
			"caliper/standards/ui":    "knowledge:caliper/guides/ui/direction#brief-to-direction",
			"caliper/standards/tests": "knowledge:caliper/guides/tests/contract#contract",
		}[item.Key]
		if item.Source != "caliper-default" || item.Section == "" || wantHop == "" || !strings.Contains(item.Body, wantHop) {
			t.Fatalf("entry omitted useful next hop: %+v", item)
		}
		if len(item.Links) != 0 {
			t.Fatal("default next hop duplicated beside its authored Markdown link")
		}
	}
	before := c.Revision
	if err := project(t.Context(), &c, ""); err != nil || c.Revision != before {
		t.Fatalf("unchanged projection changed conversation: %d -> %d, %v", before, c.Revision, err)
	}
	put := func(scope, body, previous string) *memory.Entry {
		t.Helper()
		e, err := memory.PutKnowledge(t.Context(), s, scope, "author", "project/design/identity", body, previous, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	put(t.TempDir(), "FOREIGN_IDENTITY", "")
	old := put(root, "Copper accents with readable type", "")
	if err := project(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	current := contextPacket(t, &c).Items[0]
	if current.ID != old.ID || current.Body != old.Body || current.Status != "supplied" {
		t.Fatalf("stored identity not delivered: %+v", current)
	}
	changed := put(root, "Olive accents; preserve the same readable type", old.ID)
	if err := project(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	current = contextPacket(t, &c).Items[0]
	if current.ID != changed.ID || current.Body != changed.Body {
		t.Fatalf("new head did not replace projection: %+v", current)
	}
	raw, _ := json.Marshal(c.Messages())
	for _, forbidden := range []string{"FOREIGN_IDENTITY", old.Body, "Start with the person, task and setting"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("foreign/stale/deep content was preloaded: %q", forbidden)
		}
	}
	cleared := put(root, "", changed.ID)
	if err := project(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	current = contextPacket(t, &c).Items[0]
	if current.ID != cleared.ID || current.Status != "supplied" || current.Body != "" {
		t.Fatalf("deliberately empty head misrepresented: %+v", current)
	}
}

func TestProjectContextBudgetAndGuidanceOffKeepFactsDistinct(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = memory.PutKnowledge(t.Context(), s, root, "author", "project/design/identity", strings.Repeat("長い設計 ", 500), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Recall.MaxChars, cfg.Testing.Guidance = 40, false
	c := session.Continuation{Version: 1}
	if err := projectContext(cfg, s, root, fixtureTokenCounter)(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	packet := contextPacket(t, &c)
	if packet.Items[0].Status != "supplied" || packet.Items[0].ID == "" || packet.Items[0].Body != strings.Repeat("長い設計 ", 500) {
		t.Fatalf("ordinary identity incorrectly inherited the recall character budget: %+v", packet.Items[0])
	}
	for _, item := range packet.Items {
		if item.Key == memory.TestsStandardKey {
			t.Fatal("disabled testing advice was delivered")
		}
	}
	if packet.Items[1].Key != "project/testing/strategy" || packet.Items[1].Status != "absent" {
		t.Fatal("guidance preference hid project fact status")
	}
	s.Close()
	if err := projectContext(cfg, s, root, fixtureTokenCounter)(t.Context(), &c, ""); err != nil {
		t.Fatal(err)
	}
	if contextPacket(t, &c).Items[0].Status != "unavailable" {
		t.Fatal("failed lookup was presented as absent or budget omission")
	}
}

func TestActualModelRequestReceivesFreshProjectContext(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	identity, err := memory.PutKnowledge(t.Context(), s, root, "author", "project/design/identity", "Distinct amber linework; no ornamental glow", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	captured := make(chan []llm.Message, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct{ Messages []llm.Message }
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		select {
		case captured <- body.Messages:
		default:
			t.Error("unexpected repeated provider requests")
		}
		http.Error(w, "capture complete; no inference", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	cfg := config.Defaults()
	cfg.Model.Runtime, cfg.Model.ToolProtocol, cfg.Recall.TopN = "openai", "structured", 0
	r, _, err := New(Deps{Cfg: cfg, Store: s, Server: server.URL, Model: "capture", MaxSteps: 1, Window: 32768}, "Build the requested surface")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = r.Run(t.Context(), "Build the requested surface")
	if len(captured) != 1 || err == nil || r.Continuation.Assignment != nil {
		t.Fatalf("capture count/error/authority: %d %v %+v", len(captured), err, r.Continuation.Assignment)
	}
	c := session.Continuation{}
	messages := <-captured
	if len(messages) < 2 || messages[len(messages)-2].Content != "Build the requested surface" || !strings.HasPrefix(messages[len(messages)-1].Content, projectContextMarker) {
		t.Fatal("current facts were not appended after the unchanged actual user request")
	}
	for _, message := range messages {
		c.Entries = append(c.Entries, session.Entry{Message: message})
	}
	packet := contextPacket(t, &c)
	if packet.Project != root || packet.Items[0].ID != identity.ID || packet.Items[0].Body != identity.Body {
		t.Errorf("actual request lacks scoped body/version: %+v", packet)
	}
}
