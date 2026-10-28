package run

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/files"
)

// Execute the exact documented recipes through actual role-scoped GraphQL and
// reopen the Kuzu store. This proves the interface/persistence path, not that an
// unscripted model chooses it or applies its contents to a rendered interface.
func TestR3DesignIdentityRecipesReopenAndPreserve(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	db := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	cfg := config.Defaults()
	cfg.PromptCatalog = prompts.Builtin()
	cfg.Behavior.Path = ""
	cfg.Decision.Disabled = true
	// This recipe/persistence control supplies an explicit tokenizer double;
	// exact counts for a real model remain a separate native gate.
	_, tokenizerURL, _, tokenizations := selectedGuidanceProvider(t)
	cfg.Model.Runtime = "rulith"
	defer func() {
		if tokenizations.Load() == 0 {
			t.Error("domain recipe bypassed the configured tokenizer")
		}
	}()
	makeRole := func(project, author string, reviewer bool) (tool.Definition, *tool.Registry) {
		t.Helper()
		w := &files.Workspace{Path: project, Caps: cfg.Tools.Bash}
		d := launchDeps(cfg, s, tokenizerURL, "tokenizer-fixture", 0, 32768, strings.NewReader(""), io.Discard, w, "")
		def, err := d.NewKnowledge(author, reviewer)
		if err != nil {
			t.Fatal(err)
		}
		reg := d.NewRegistry()
		reg.Register(def)
		return def, reg
	}
	type entry struct {
		ID, Key, Body, PreviousID, SessionRef string
		References                            []struct{ Key, Relation string }
		Sections                              []struct {
			Body       string
			References []struct{ Key, Section, Label string }
		}
	}
	type reply struct {
		Data struct {
			Identity, Standard, Knowledge []entry
			AppendKnowledge               entry
		}
	}
	execute := func(def tool.Definition, query string) reply {
		t.Helper()
		args, repair := tool.ValidateJSON(def, fmt.Sprintf(`{"query":%q}`, query))
		if repair != "" {
			t.Fatal(repair)
		}
		result, err := def.Execute(t.Context(), args)
		if err != nil || result.Failed {
			t.Fatalf("query=%s result=%s error=%v", query, result.Text, err)
		}
		var got reply
		if err := json.Unmarshal([]byte(result.Text), &got); err != nil {
			t.Fatal(result.Text, err)
		}
		return got
	}
	fences := func(text string) []string {
		t.Helper()
		var out []string
		for {
			_, rest, found := strings.Cut(text, "```graphql\n")
			if !found {
				return out
			}
			q, next, closed := strings.Cut(rest, "```")
			if !closed {
				t.Fatal("unclosed documentation fence")
			}
			out = append(out, strings.TrimSpace(q))
			text = next
		}
	}
	pc := cfg.PromptCatalog
	readExamples := fences(pc.Text("help.graphql.readonly"))
	writes := fences(pc.Text("help.graphql.worker_write"))
	if len(readExamples) != 1 || len(writes) != 2 {
		t.Fatal("missing exact design recipes", readExamples, writes)
	}
	worker, reg := makeRole(root, "design-author", false)
	help, _ := reg.Lookup("help")
	helpResult, err := help.Execute(t.Context(), tool.Args{"topic": "graphql.knowledge"})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range append(readExamples, writes...) {
		if !strings.Contains(helpResult.Text, q) {
			t.Fatal("actual worker help omitted recipe", q)
		}
	}
	missing := execute(worker, readExamples[0])
	if len(missing.Data.Identity) != 0 || len(missing.Data.Standard) != 1 || len(missing.Data.Standard[0].Sections) != 1 {
		t.Fatalf("first-use read lost empty identity/selected standard boundary: %+v", missing)
	}
	section := missing.Data.Standard[0].Sections[0]
	if !strings.Contains(section.Body, "## identity") || strings.Contains(section.Body, "## typography") || len(section.References) != 1 || section.References[0].Key != "caliper/guides/ui/direction" || section.References[0].Section != "brief-to-direction" || section.References[0].Label == "" {
		t.Fatalf("first-use read lost selected identity section or labelled next hop: %+v", section)
	}
	created := execute(worker, writes[0]).Data.AppendKnowledge
	if created.ID == "" || created.Key != "project/design/identity" {
		t.Fatalf("create receipt: %+v", created)
	}
	s.Close()
	s, err = store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	worker, _ = makeRole(root, "feature-author", false)
	reopened := execute(worker, readExamples[0])
	if len(reopened.Data.Identity) != 1 || reopened.Data.Identity[0].ID != created.ID || reopened.Data.Identity[0].Body != created.Body {
		t.Fatalf("reopen lost identity: %+v", reopened)
	}
	feature := execute(worker, `mutation { appendKnowledge(key:"project/features/status",body:"Status panel preserves the field-instrument identity; rendering remains unverified.",expectedPreviousId:"",references:[{key:"project/design/identity",relation:"implements"}]) { id key references { key relation } } }`).Data.AppendKnowledge
	if len(feature.References) != 1 || feature.References[0].Key != created.Key || feature.References[0].Relation != "implements" {
		t.Fatalf("feature lost identity link: %+v", feature)
	}
	unchanged := execute(worker, readExamples[0]).Data.Identity
	if len(unchanged) != 1 || unchanged[0].ID != created.ID || unchanged[0].Body != created.Body {
		t.Fatal("feature write replaced identity")
	}
	correctedQuery := strings.ReplaceAll(writes[1], "HEAD_FROM_READ", created.ID)
	corrected := execute(worker, correctedQuery).Data.AppendKnowledge
	if corrected.ID == created.ID || !strings.Contains(corrected.Body, "orange for warnings") {
		t.Fatalf("correction missing: %+v", corrected)
	}
	for _, preserved := range []string{"field technicians", "cream text on charcoal", "clear sans-serif labels", "dense instrument panels", "only status feedback"} {
		if !strings.Contains(corrected.Body, preserved) {
			t.Fatal("unrelated identity fact lost", preserved)
		}
	}
	if result, err := worker.Execute(t.Context(), tool.Args{"query": correctedQuery}); err == nil || !result.Failed {
		t.Fatal("stale update silently replaced newer identity", result.Text, err)
	}
	historical := execute(worker, fmt.Sprintf(`{ knowledge(id:%q,limit:1) { id body sessionRef } }`, created.ID)).Data.Knowledge
	if len(historical) != 1 || historical[0].Body != created.Body || historical[0].SessionRef != "design-author" {
		t.Fatalf("historical identity/author lost: %+v", historical)
	}
	foreign, _ := makeRole(t.TempDir(), "foreign-author", false)
	if got := execute(foreign, readExamples[0]); len(got.Data.Identity) != 0 {
		t.Fatal("identity leaked across projects", got)
	}
	reviewer, reviewRegistry := makeRole(root, "review-author", true)
	if got := execute(reviewer, readExamples[0]); len(got.Data.Identity) != 1 || got.Data.Identity[0].ID != corrected.ID {
		t.Fatal("reviewer lost current read", got)
	}
	if _, err := reviewer.Execute(t.Context(), tool.Args{"query": writes[0]}); err == nil {
		t.Fatal("reviewer gained write authority")
	}
	reviewHelp, _ := reviewRegistry.Lookup("help")
	result, err := reviewHelp.Execute(t.Context(), tool.Args{"topic": "graphql.knowledge"})
	if err != nil || strings.Contains(result.Text, "appendKnowledge") {
		t.Fatal("review help exposed writes", result.Text, err)
	}

	// Fresh ordinary run: capture the actual outbound model messages. No fixture
	// claims model comprehension/application or causes any source/artifact change.
	r, _, err := New(Deps{Tools: "full", Cfg: cfg, Store: s, Server: "http://unused", Model: "not-requested", SessionID: "fresh-feature-session", In: strings.NewReader(""), Out: io.Discard}, "Add a status panel using the field technicians palette and layout.")
	if err != nil {
		t.Fatal(err)
	}
	ordinaryHelp, _ := r.Registry.Lookup("help")
	ordinaryResult, err := ordinaryHelp.Execute(t.Context(), tool.Args{"topic": "graphql.knowledge"})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range append(readExamples, writes...) {
		if !strings.Contains(ordinaryResult.Text, q) {
			t.Fatal("actual ordinary help omitted authorized recipe", q)
		}
	}
	model := &recalledRequest{}
	r.Model = model
	_, _, _ = r.Run(t.Context(), "Add a status panel using the field technicians palette and layout.")
	currentCount := 0
	for _, message := range model.messages {
		if strings.Contains(message.Content, corrected.Body) {
			currentCount++
			if message.Role != "user" {
				t.Fatal("authored identity gained system authority")
			}
		}
		if strings.Contains(message.Content, created.Body) {
			t.Fatal("retired identity supplied as current")
		}
	}
	if currentCount != 1 {
		for i, message := range model.messages {
			if strings.Contains(message.Content, corrected.Body) {
				t.Logf("current identity entry%d role=%s message_bytes=%d automatic_recall=%t project_packet=%t", i, message.Role, message.Size(), message.Content == r.Memories, strings.HasPrefix(message.Content, projectContextMarker))
			}
		}
		t.Fatalf("current identity not delivered exactly once: %d", currentCount)
	}

	nonvisual, _, err := New(Deps{Tools: "full", Cfg: cfg, Store: s, Server: "http://unused", Model: "not-requested", SessionID: "nonvisual", In: strings.NewReader(""), Out: io.Discard}, "Explain integer overflow in multiplication.")
	if err != nil {
		t.Fatal(err)
	}
	context := session.Continuation{Version: 1}
	if err := nonvisual.ProjectContext(t.Context(), &context, ""); err != nil {
		t.Fatal(err)
	}
	if recall := contextPacket(t, &context).Recall; recall != "" || nonvisual.Memories != "" {
		t.Fatal("unrelated nonvisual prompt received topical design recall", recall)
	}
}
