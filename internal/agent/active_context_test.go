package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func activeFixture() session.Continuation {
	c := session.Continuation{Version: 1}
	c.Add("intent", "user", "Fix current behavior; preserve required regression.", false)
	c.Add("note", "user", "User comment stays exact.", false)
	c.Add("failure", "user", "Unresolved compile failure.", false)
	c.Add("evidence", "user", "Current check receipt exit 1.", false)
	c.Entries = append(c.Entries, session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", Thinking: strings.Repeat("unverified speculative implementation ", 800), ToolCalls: []llm.ToolCall{{ID: "read-1", Name: "read", Arguments: `{"path":"main.go","start_line":1,"end_line":5}`}}}}, session.Entry{Kind: "output", Message: llm.Message{Role: "tool", ToolCallID: "read-1", Name: "read", Content: "actual source range1-5 sha256=actual-current-hash\nSOURCE MUST STAY"}})
	return c
}
func activeRunner(m *scriptModel, s *recordSink, c session.Continuation) *Runner {
	return &Runner{Model: m, Session: s, SessionID: "active", Prompts: prompts.Builtin(), Registry: tool.NewRegistry(), MaxSteps: 2, MaxTokens: 262144, Continuation: c, Judge: acceptCompaction, Threshold: .7, Compaction: Compaction{Trigger: 90, Keep: 40, Summary: 10, Step: 1, Max: 20, WordsPerToken: 1}, Prune: Prune{OutputsPct: 20, SchemasPct: 5, SchemasKeep: 3}}
}

func TestActiveDiscoveryKeepsExactAndChangedSourceExchanges(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(map[bool]string{false: "same", true: "changed-source"}[different], func(t *testing.T) {
			c := activeFixture()
			c.Entries[4].Message.Thinking = ""
			fresh := c.Entries[4].Message.Clone()
			fresh.ToolCalls[0].ID = "read-2"
			receipt := c.Entries[5]
			receipt.Message.ToolCallID = "read-2"
			receipt.Unread = true
			if different {
				receipt.Message.Content = "different observed source/hash: contradiction retained"
			}
			c.Entries = append(c.Entries, session.Entry{Kind: "reply", Message: fresh}, receipt)
			r := activeRunner(&scriptModel{}, &recordSink{}, c)
			changed, err := r.maintainDiscovery(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				t.Fatal("routine discovery retirement is disabled", changed)
			}
			if err = r.Continuation.Snapshot().Validate(); err != nil {
				t.Fatal(err)
			}
			want := 8
			if len(r.Continuation.Entries) != want {
				t.Fatal("wrong group removal", len(r.Continuation.Entries))
			}
		})
	}
}

// Disabled discovery maintenance never publishes a replacement or consumes a
// summary/decision response, even when a sink could cancel after publication.
func TestDiscoveryMaintenanceDoesNotPublish(t *testing.T) {
	c := duplicateActiveFixture()
	before := c.Clone()
	sink := &recordSink{}
	r := activeRunner(&scriptModel{}, sink, c)
	changed, err := r.maintainDiscovery(context.Background())
	if err != nil || changed || len(sink.kinds) != 0 || !reflect.DeepEqual(before, r.Continuation) {
		t.Fatalf("discovery no-op published: changed=%t err=%v events=%v", changed, err, sink.kinds)
	}
}

func TestPreparationPromptCompositionSeparator(t *testing.T) {
	pc := prompts.Builtin()
	r := &Runner{Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "controlled-store", Kind: "root"}}, Prompts: pc, Registry: tool.NewRegistry(), Assigned: &AssignedOps{}}
	r.Registry.Register(tool.Definition{Name: "help"})
	if !strings.Contains(r.basePrompt(), pc.Text("system.help_available")+"\n"+pc.Text("system.task.prepare")) {
		t.Fatal("host-composed prompt sections concatenate without separator")
	}
}

func duplicateActiveFixture() session.Continuation {
	c := activeFixture()
	m := c.Entries[4].Message.Clone()
	m.ToolCalls[0].ID = "new-read"
	e := c.Entries[5]
	e.Message.ToolCallID = "new-read"
	e.Unread = true
	c.Entries = append(c.Entries, session.Entry{Kind: "reply", Message: m}, e)
	return c
}

func TestActiveDeterministicSafety(t *testing.T) {
	for _, mode := range []string{"pass", "unread", "failure", "mixed", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			c := duplicateActiveFixture()
			switch mode {
			case "unread":
				c.Entries[5].Unread = true
			case "failure":
				c.Entries[5].Kind = "failure"
			case "mixed":
				c.Entries[4].Message.ToolCalls[0].Name = "edit"
			}
			m, sink := &scriptModel{}, &recordSink{}
			r := activeRunner(m, sink, c)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			before := r.Continuation.Clone()
			changed, err := r.maintainDiscovery(ctx)
			if err != nil || changed || !reflect.DeepEqual(before, r.Continuation) || len(m.seen) != 0 || len(sink.kinds) != 0 {
				t.Fatalf("disabled routine retirement did work: changed=%t err=%v", changed, err)
			}
		})
	}
}
