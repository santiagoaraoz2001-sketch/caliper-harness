package graph

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/store"
)

func TestDomainFallbackBoundsActualGraphEnvelopeAndExemptsFacts(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	a := projectAPI(t, s, root).WithDomainTokenCounter(llm.New("http://127.0.0.1:1", "unused").WithRuntime(llm.RuntimeOllama).CountTokens)
	key, previous, body := "project/guides/ui/bytes", "", "漢🚀"
	put := func() {
		t.Helper()
		e, err := memory.PutKnowledge(t.Context(), s, root, "test", key, body, previous, "", "")
		if err != nil {
			t.Fatal(err)
		}
		previous = e.ID
	}
	query := `{guide:knowledge(key:"project/guides/ui/bytes"){body}}`
	put()
	raw, err := a.ExecBounded(t.Context(), query, "", nil, config.Defaults().Tools.GraphQL)
	if err != nil {
		t.Fatal(err)
	}
	var result Result
	if json.Unmarshal(raw, &result) != nil || result.DomainBudgetBasis != memory.DomainUTF8Bytes {
		t.Fatalf("missing basis: %s", raw)
	}
	body += strings.Repeat("x", memory.DomainTokens-len(raw))
	put()
	raw, err = a.ExecBounded(t.Context(), query, "", nil, config.Defaults().Tools.GraphQL)
	if err != nil || len(raw) != memory.DomainTokens {
		t.Fatalf("exact emitted boundary: bytes=%d err=%v", len(raw), err)
	}
	_, err = memory.PutKnowledge(t.Context(), s, root, "test", "project/design/identity", strings.Repeat("ordinary ", 2000), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	mixed, err := a.ExecBounded(t.Context(), `{guide:knowledge(key:"project/guides/ui/bytes"){body} ordinary:knowledge(key:"project/design/identity"){body}}`, "", nil, config.Defaults().Tools.GraphQL)
	if err != nil || len(mixed) <= memory.DomainTokens || !strings.Contains(string(mixed), "ordinary ") {
		t.Fatalf("facts charged to domain allowance: %v", err)
	}
	body += "x"
	put()
	raw, err = a.ExecBounded(t.Context(), query, "", nil, config.Defaults().Tools.GraphQL)
	json.Unmarshal(raw, &result)
	if err == nil || !result.DataOmitted || result.DomainBudgetBasis != memory.DomainUTF8Bytes || strings.Contains(string(raw), body) {
		t.Fatalf("overlimit leaked: %s %v", raw, err)
	}
}

func TestDomainFallbackDoesNotEnableNilCounter(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir()).WithDomainTokenCounter(nil)
	raw, err := a.ExecBounded(t.Context(), `{knowledge(key:"caliper/standards/ui",section:"identity"){body}}`, "", nil, config.Defaults().Tools.GraphQL)
	var result Result
	json.Unmarshal(raw, &result)
	if err == nil || !result.DataOmitted || result.DomainBudgetBasis != "" {
		t.Fatalf("nil counter granted fallback: %s %v", raw, err)
	}
}
