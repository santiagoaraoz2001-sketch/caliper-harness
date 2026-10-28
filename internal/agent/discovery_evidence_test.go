package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

func TestCapturedReviewerRetainsCrossCallEvidence(t *testing.T) {
	raw, err := os.ReadFile("testdata/reviewer-discovery-candidate9.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Receipts []struct {
			CallID     string `json:"call_id"`
			Line       int
			Tool       string
			Args       tool.Args
			Projection string
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Receipts) != 100 {
		t.Fatal("incomplete captured sequence")
	}
	w := &watcher{historyBudget: 8192}
	for _, receipt := range fixture.Receipts {
		w.discoveryReceipt(&observed{CallID: receipt.CallID, Tool: receipt.Tool, Args: receipt.Args, Result: tool.Result{Text: receipt.Projection}}, 5)
	}
	r := &Runner{Prompts: prompts.Builtin(), Reviewing: true}
	p, err := r.progressEvidence(t.Context(), w)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.RecentReceipts) != 5 || p.OmittedReceipts != 95 || len(p.Discovery.Sources) != 5 {
		t.Fatalf("lost historical coverage outside recent window: %+v", p.Discovery)
	}
	lines := 0
	for _, source := range p.Discovery.Sources {
		if !source.CompleteRead || source.EOFLine == nil {
			t.Fatalf("lost complete source: %+v", source)
		}
		lines += *source.EOFLine
	}
	if lines != 357 {
		t.Fatalf("captured complete lines=%d", lines)
	}
	found := false
	for _, query := range p.Discovery.RepeatedObservations {
		if query.Tool == "grep" && query.Occurrences == 6 && query.Args["pattern"] == "scaffold ok" && query.LastCallID != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("interleaved sixfold exact query forgotten: %+v", p.Discovery.RepeatedObservations)
	}
	if p.Checkpoint.Phase != "REVIEW" || p.Settled || p.Coding != nil {
		t.Fatal("discovery manufactured qualification")
	}
}

func deliveredRead(path, hash, span string, eof, truncated bool) *observed {
	return &observed{Tool: "read", Args: tool.Args{"path": path}, Result: tool.Result{
		Text: fmt.Sprintf("%s sha256=%s\nactual body\n[host metadata; shown_range=%s; EOF=%t; truncated=%t; metadata not source lines]", path, hash, span, eof, truncated), Truncated: truncated}}
}

func TestDiscoveryCoverageRequiresSameVersionAndCompleteRange(t *testing.T) {
	h := discoveryHistory{}
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	h.record(deliveredRead("x.go", a, "1-3", false, true), 8192)
	h.record(deliveredRead("x.go", a, "5-7", true, false), 8192)
	if h.evidence().Sources[0].CompleteRead {
		t.Fatal("gap counted complete")
	}
	h.record(deliveredRead("./x.go", a, "4-4", false, false), 8192)
	if s := h.evidence().Sources[0]; !s.CompleteRead || s.Reads != 3 {
		t.Fatalf("same-version range merge: %+v", s)
	}
	h.record(deliveredRead("x.go", b, "5-7", true, false), 8192)
	if s := h.evidence().Sources[0]; s.CompleteRead || s.Reads != 1 || s.FileSHA256 != b {
		t.Fatalf("old-version coverage leaked: %+v", s)
	}
	h.record(deliveredRead("x.go", b, "1-4", true, true), 8192)
	// The previous untruncated EOF plus complementary delivered bytes are enough,
	// even when the latter read stops at a configured output boundary.
	if !h.evidence().Sources[0].CompleteRead {
		t.Fatal("real complementary bytes discarded")
	}
	other := deliveredRead("x.go", b, "1-7", true, false)
	other.Tool = "read_outside"
	h.record(other, 8192)
	if len(h.evidence().Sources) != 2 {
		t.Fatal("outside and project scopes conflated")
	}
	outline := deliveredRead("outline.go", b, "1-7", true, false)
	outline.Args["outline"] = true
	h.record(outline, 8192)
	if len(h.evidence().Sources) != 2 {
		t.Fatal("selected outline counted as complete source")
	}
	h.record(&observed{Tool: "write", Result: tool.Result{FileEffect: &tool.FileEffect{Path: "x.go"}}}, 8192)
	if e := h.evidence(); len(e.Sources) != 0 || e.OmittedFacts == 0 {
		t.Fatal("write did not invalidate current comparison epoch")
	}
}

func TestDiscoveryQueryScopeAndEvictionStayExplicit(t *testing.T) {
	h := discoveryHistory{}
	query := func(path string, truncated bool) *observed {
		return &observed{Tool: "grep", Args: tool.Args{"path": path, "pattern": "x"}, Result: tool.Result{Text: "same bytes", Truncated: truncated}}
	}
	h.record(query("one", false), 8192)
	h.record(query("two", false), 8192)
	h.record(query("one", true), 8192)
	if len(h.evidence().RepeatedObservations) != 0 {
		t.Fatal("different path or output boundary counted identical")
	}
	h.record(query("one", false), 8192)
	if e := h.evidence(); len(e.RepeatedObservations) != 1 || e.RepeatedObservations[0].Occurrences != 2 {
		t.Fatal("interleaved exact observation lost", e)
	}
	h.record(&observed{Tool: "bash"}, 8192)
	h.record(query("one", false), 8192)
	if len(h.evidence().RepeatedObservations) != 0 {
		t.Fatal("unknown shell effects preserved duplicate certainty")
	}
	for i := 0; i < 20; i++ {
		h.record(query(fmt.Sprint(i), false), 512)
	}
	settledSize := len(h.facts)
	for i := 20; i < 1000; i++ {
		h.record(query(fmt.Sprint(i), false), 512)
	}
	if h.evidence().OmittedFacts == 0 || len(h.facts) > settledSize {
		t.Fatal("cache silently unbounded", h)
	}
	h.record(query("one", false), 512)
	if len(h.evidence().RepeatedObservations) != 0 {
		t.Fatal("evicted observation falsely remembered")
	}
}

func TestRepeatedQueryLocatorSurvivesRecentWindow(t *testing.T) {
	w := &watcher{historyBudget: 8192}
	for _, call := range []struct{ id, pattern string }{{"a1", "missing-a"}, {"b1", "missing-b"}, {"a2", "missing-a"}, {"b2", "missing-b"}} {
		w.discoveryReceipt(&observed{CallID: call.id, Tool: "grep", Args: tool.Args{"path": "src", "pattern": call.pattern}, Result: tool.Result{Text: "[]", NoMatches: true}}, 5)
	}
	for i := 0; i < 6; i++ {
		w.discoveryReceipt(&observed{Tool: "grep", Args: tool.Args{"pattern": fmt.Sprint(i)}, Result: tool.Result{Text: "other result"}}, 5)
	}
	queries := w.history.evidence().RepeatedObservations
	if len(queries) != 2 {
		t.Fatal("different questions with same empty output lost", queries)
	}
	for _, query := range queries {
		want := map[string]string{"missing-a": "a2", "missing-b": "b2"}[query.Args["pattern"].(string)]
		if want == "" || query.LastCallID != want || query.Occurrences != 2 {
			t.Fatal("opaque or misattributed repeated query", query)
		}
	}
	for i := 0; i < 2; i++ {
		w.discoveryReceipt(&observed{Tool: "grep", Args: tool.Args{"pattern": strings.Repeat("long", 4096)}, Result: tool.Result{Text: "[]"}}, 5)
	}
	if w.history.evidence().OmittedFacts == 0 {
		t.Fatal("oversized exact locator was silently clipped or retained beyond budget")
	}
}
