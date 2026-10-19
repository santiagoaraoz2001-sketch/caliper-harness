package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func TestPruneBatchRetainsPrefixUntilExistingBoundary(t *testing.T) {
	for _, boundary := range []string{"system", "tools", "model"} {
		t.Run(boundary, func(t *testing.T) {
			r := agePolicyRunner(t)
			r.Compaction.Trigger = 90
			c := agePolicyFixture(49999)
			var batch pruneBatch
			if _, report := batch.apply(r, &c); report != "" {
				t.Fatal(report)
			}
			c.Add("reply", "assistant", strings.Repeat("new reasoning ", 1000), false)
			before := c.Clone()
			// Reproduce the old per-request behavior on the same context.
			old := c.Clone()
			if _, report := r.prune(&old); report == "" {
				t.Fatal("fixture did not reproduce premature pruning")
			}
			if _, report := batch.apply(r, &c); report != "" || !reflect.DeepEqual(before, c) {
				t.Fatal("ordinary tool turn rewrote its cached history", report)
			}
			switch boundary {
			case "system":
				c.Entries[0].Message.Content = "Next claimed task instructions"
			case "tools":
				c.SchemaKey = "new legal tool definitions"
			case "model":
				c.ModelKey = "new selected model"
			}
			if _, report := batch.apply(r, &c); report == "" {
				t.Fatal("boundary did not batch old outputs")
			}
			if !strings.HasPrefix(c.Entries[3].Message.Content, "[pruned tool output") {
				t.Fatal("old output not pruned")
			}
			if !reflect.DeepEqual(c.Entries[1], before.Entries[1]) || !reflect.DeepEqual(c.Entries[2], before.Entries[2]) {
				t.Fatal("intent or native call changed")
			}
			settled := c.Clone()
			if _, report := batch.apply(r, &c); report != "" || !reflect.DeepEqual(settled, c) {
				t.Fatal("same boundary repeated pruning")
			}
			if err := c.Snapshot().Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPruneBatchExistingPressureThreshold(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger float64
		prompt  int
		want    bool
	}{
		{"below90", 90, 89999, false}, {"at90", 90, 90000, true},
		{"summary_disabled", 0, 90000, false}, {"full_window", 0, 100000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := agePolicyRunner(t)
			r.MaxTokens = 100000
			r.Compaction.Trigger = tc.trigger
			c := agePolicyFixture(49999)
			var batch pruneBatch
			batch.apply(r, &c)
			c.Add("reply", "assistant", strings.Repeat("x", 2000), false)
			// Server measurement, not just the smaller content estimate, schedules pressure cleanup.
			c.Measured, c.Prompt = true, tc.prompt
			_, report := batch.apply(r, &c)
			if (report != "") != tc.want {
				t.Fatalf("report=%q want pruning=%t", report, tc.want)
			}
		})
	}
}

func TestPruneBatchRunnerKeepsConsumedOutputBetweenToolTurns(t *testing.T) {
	var calls []string
	reg := tool.NewRegistry()
	reg.Register(echoTool(&calls))
	c := agePolicyFixture(49000).Snapshot()
	c.ModelKey = "fixture"
	model := &scriptModel{replies: []string{echoBlock(strings.Repeat("fresh output ", 1000)), "done"}}
	r := Runner{Model: model, ModelKey: "fixture", Registry: reg, Continuation: c, MaxTokens: 200000, Prune: Prune{OutputsAgeTokens: 50000}, Compaction: Compaction{Trigger: 90}}
	if _, _, err := r.Run(context.Background(), "Continue current work."); err != nil {
		t.Fatal(err)
	}
	if len(model.seen) != 2 {
		t.Fatalf("requests=%d", len(model.seen))
	}
	for _, m := range model.seen[1] {
		if m.ToolCallID == "old-read" {
			if strings.HasPrefix(m.Content, "[pruned") {
				t.Fatal("actual second request invalidated old cached output")
			}
			// Verify this is genuinely an age-eligible body under the previous policy.
			prior := r.Continuation.Clone()
			prior.Entries = append([]session.Entry{{Kind: "injected", Message: llm.Message{Role: "system", Content: "base"}}}, prior.Entries...)
			if _, report := r.prune(&prior); report == "" {
				t.Fatal("regression fixture never reached pruning age")
			}
			return
		}
	}
	t.Fatal("actual second request lost old result")
}
