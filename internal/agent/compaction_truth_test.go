package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/session"
)

func truthFixture() session.Continuation {
	c := session.Continuation{Version: 1}
	c.Add("intent", "user", "TASK: repair the owned source; prove actual tests and preserve failures.", false)
	c.Add("reply", "assistant", "```tool\ntool: bash\ncmd: go test -run TestNeverExecuted\n```\nAll tests passed; edits completed.", false)
	c.Entries[1].Message.Thinking = strings.Repeat("Unverified old draft reasoning. ", 80)
	c.Add("output", "user", "tool result: actual listing; no changes made", false)
	c.Add("schema", "user", "bash schema: command requires a single tool call", false)
	c.Add("failure", "user", "NO CALL EXECUTED: multiple tool blocks refused", false)
	c.Add("failure", "user", "ACTUAL FAILURE: no Go files [exit code 1]", false)
	c.Add("evidence", "user", "RECEIPTS: two failures; declared-check correctness unverified", false)
	return c
}
func truthRunner(t *testing.T, m *scriptModel, window int) *Runner {
	r := compactRunner(m, &recordSink{}, testTemplateDir(t))
	r.MaxTokens = window
	r.Compaction.Keep = 0
	r.Compaction.Summary = 20
	r.Compaction.Max = 20
	return r
}
func TestCompactionTruthProbeReferenceAndProvenance(t *testing.T) {
	c := truthFixture()
	m := &scriptModel{replies: []string{"Repair pending; actual commands failed."}}
	next, _, ran, err := truthRunner(t, m, 10000).Compact(context.Background(), c)
	if err != nil || !ran || len(m.seen) != 1 {
		t.Fatalf("setup: ran=%v err=%v seen=%d", ran, err, len(m.seen))
	}
	input := m.seen[0][1].Content
	for _, e := range c.Entries {
		if e.Kind == "intent" || e.Kind == "failure" || e.Kind == "evidence" {
			if strings.Count(input, e.Message.Content) != 1 {
				t.Errorf("missing/excess exact reference %s: %q", e.Kind, e.Message.Content)
			}
			found := 0
			for _, n := range next.Entries {
				if reflect.DeepEqual(n, e) {
					found++
				}
			}
			if found != 1 {
				t.Errorf("protected continuation changed: kind=%s count=%d", e.Kind, found)
			}
		}
	}
	for _, label := range []string{"kind=reply", "kind=output", "model text; unverified", "recorded observation", "reference-only"} {
		if !strings.Contains(input, label) {
			t.Errorf("input lacks provenance label %q", label)
		}
	}
	t.Logf("Captured actual Compact request (%d bytes):\n%s", len(input), input)
}
func TestCompactionTruthProbeInputAdmission(t *testing.T) {
	c := truthFixture()
	c.Add("failure", "user", strings.Repeat("FRESH EXACT FAILED RESULT ", 100), false)
	before := c.Clone()
	m := &scriptModel{replies: []string{"Pending."}}
	next, _, ran, err := truthRunner(t, m, 500).Compact(context.Background(), c)
	if len(m.seen) != 0 || err == nil || ran {
		t.Errorf("full input must refuse before model call: seen=%d err=%v ran=%v", len(m.seen), err, ran)
	}
	if !reflect.DeepEqual(next, before) {
		t.Error("refused input changed continuation")
	}
}
func TestCompactionTruthProbeNothingEligible(t *testing.T) {
	c := session.Continuation{Version: 1}
	c.Add("intent", "user", "TASK", false)
	c.Add("failure", "user", "actual failure", false)
	m := &scriptModel{replies: []string{"unused"}}
	next, _, ran, err := truthRunner(t, m, 10000).Compact(context.Background(), c)
	if err != nil || ran || len(m.seen) != 0 || !reflect.DeepEqual(next, c) {
		t.Fatalf("reference-only context must not trigger summary: seen=%d ran=%v err=%v", len(m.seen), ran, err)
	}
}

func TestCompactionTruthKindsAndRawReferences(t *testing.T) {
	c := truthFixture()
	c.Add("injected", "system", "TRANSIENT_BEHAVIOR_NOT_A_REFERENCE", false)
	c.Add("task", "user", "LATEST_TASK: inspect é🙂\r\n  indentation stays\r\n", false)
	c.Add("note", "user", "ACCEPTED_NOTE: no invented edits\r\n", false)
	c.Add("history", "user", "REAL_USER_HISTORY", false)
	c.Add("history", "assistant", "OLD_MODEL_CLAIM", false)
	c.Add("summary", "user", "PRIOR_MODEL_SUMMARY", false)
	c.Add("failure", "user", "RAW_FAILURE: é🙂\r\n  intact bytes\r\n", false)
	c.Add("schema", "user", "SECOND_SCHEMA", false)
	c.Add("schema", "user", "THIRD_SCHEMA", false)
	c.Add("schema", "user", "FOURTH_SCHEMA", false)
	before := c.Clone()
	m := &scriptModel{replies: []string{"Pending; recorded failure contradicts claims."}}
	r := truthRunner(t, m, 10000)
	r.TemplatesDir = "../../templates"
	next, _, ran, err := r.Compact(context.Background(), c)
	if err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	input := m.seen[0][1].Content
	for _, e := range c.Entries {
		switch e.Kind {
		case "intent", "task", "note", "failure", "evidence":
			if strings.Count(input, e.Message.Content) != 1 {
				t.Errorf("reference bytes not exactly once: %s %q", e.Kind, e.Message.Content)
			}
		}
	}
	for _, line := range []string{
		"[kind=history role=user user text reference-only=false]\nREAL_USER_HISTORY",
		"[kind=history role=assistant model text; unverified reference-only=false]\nOLD_MODEL_CLAIM",
		"[kind=summary role=user model text; unverified reference-only=false]\nPRIOR_MODEL_SUMMARY",
		"[kind=schema role=user recorded observation reference-only=false]\nbash schema:",
	} {
		if !strings.Contains(input, line) {
			t.Errorf("missing typed content/instruction %q", line)
		}
	}
	for _, instruction := range []string{"only recorded observations establish recorded execution, not general correctness", "Entries marked reference-only=true remain verbatim outside this summary"} {
		if !strings.Contains(m.seen[0][0].Content, instruction) {
			t.Errorf("missing instruction %q", instruction)
		}
	}
	for _, excluded := range []string{"TRANSIENT_BEHAVIOR_NOT_A_REFERENCE", "SECOND_SCHEMA", "THIRD_SCHEMA", "FOURTH_SCHEMA"} {
		if strings.Contains(input, excluded) {
			t.Errorf("duplicated protected scaffolding/schema %q", excluded)
		}
	}
	for _, e := range c.Entries {
		if e.Kind == "injected" || e.Kind == "schema" && e.Message.Content != "bash schema: command requires a single tool call" || e.Kind == "task" || e.Kind == "note" || e.Kind == "failure" {
			found := 0
			for _, n := range next.Entries {
				if reflect.DeepEqual(n, e) {
					found++
				}
			}
			if found != 1 {
				t.Errorf("protected entry changed: %q", e.Message.Content)
			}
		}
	}
	if !reflect.DeepEqual(c, before) {
		t.Error("input continuation mutated")
	}
}

func TestCompactionTruthInputBoundaries(t *testing.T) {
	dir := t.TempDir()
	writeCompactionCatalog(t, dir, "{{.Range}}")
	c := truthFixture()
	first := &scriptModel{replies: []string{"Pending."}}
	r := truthRunner(t, first, 10000)
	r.TemplatesDir = dir
	if _, _, ran, err := r.Compact(context.Background(), c); err != nil || !ran {
		t.Fatalf("measure fixture input: %v", err)
	}
	bound := len(first.seen[0][0].Content) + len(first.seen[0][1].Content) + 40 // disclosed allowance for both messages
	for _, tc := range []struct {
		name   string
		window int
		call   bool
	}{{"exact estimate", bound, false}, {"one above", bound + 1, true}, {"zero window", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			m := &scriptModel{replies: []string{"Pending."}}
			r := truthRunner(t, m, tc.window)
			r.TemplatesDir = dir
			r.UsedIn, r.UsedOut = 17, 23
			next, _, ran, err := r.Compact(context.Background(), c)
			if (len(m.seen) > 0) != tc.call {
				t.Fatalf("seen=%d expected call=%v err=%v", len(m.seen), tc.call, err)
			}
			if !tc.call {
				if err == nil || !strings.Contains(err.Error(), "estimated") || ran || !reflect.DeepEqual(next, c) {
					t.Errorf("estimate refusal must preserve context: err=%v ran=%v", err, ran)
				}
				if r.UsedIn != 17 || r.UsedOut != 23 || r.UsageUnknown {
					t.Error("uncalled model altered usage")
				}
			}
		})
	}
	t.Run("mismatched calibration", func(t *testing.T) {
		c := truthFixture()
		c.ModelKey = "old"
		c.BasisChars = 1000000
		c.BasisTokens = 1
		before := c.Clone()
		m := &scriptModel{replies: []string{"unused"}}
		r := truthRunner(t, m, 500)
		r.ModelKey = "new"
		next, _, ran, err := r.Compact(context.Background(), c)
		if err == nil || ran || len(m.seen) != 0 || !reflect.DeepEqual(next, before) {
			t.Fatalf("stale calibration admitted input: err=%v next=%+v seen=%d", err, next, len(m.seen))
		}
		if !reflect.DeepEqual(c, before) {
			t.Error("caller continuation mutated")
		}
	})
}
