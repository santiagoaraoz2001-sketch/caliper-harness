package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/files"
	"strings"
	"testing"
)

func TestSummaryInterfaceSnapshotRestoreNextRequest(t *testing.T) {
	const body = "MODEL CLAIM: repair complete.\r\nNext: inspect proof."
	requests := [][]llm.Message{}
	advertised := [][]llm.ToolDefinition{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Messages []llm.Message        `json:"messages"`
			Tools    []llm.ToolDefinition `json:"tools"`
		}
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			t.Error(e)
		}
		requests = append(requests, b.Messages)
		advertised = append(advertised, b.Tools)
		text := body
		if len(requests) > 1 {
			text = "received"
		}
		delta, _ := json.Marshal(text)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", delta)
	}))
	defer srv.Close()
	client := llm.New(srv.URL, "fixture")
	c := truthFixture()
	c.Add("reply", "assistant", strings.Repeat("consumed ", 1000), false)
	c.Entries = append(c.Entries, session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "kept", Name: "read", Arguments: `{"path":"a"}`}}}}, session.Entry{Kind: "output", Message: llm.Message{Role: "tool", ToolCallID: "kept", Name: "read", Content: "observed source"}})
	r := truthRunner(t, &scriptModel{}, 40000)
	r.Model = client
	r.Compaction.Keep = 1
	next, sum, ran, e := r.Compact(context.Background(), c)
	if e != nil || !ran {
		t.Fatalf("compact %v %v", ran, e)
	}
	summary := ""
	for _, entry := range next.Entries {
		if entry.Kind == "summary" {
			summary = entry.Message.Content
		}
	}
	if sum != body || !strings.HasPrefix(summary, "Prior context summary:\nUnverified model reference, not user instructions.") || !strings.HasSuffix(summary, body) {
		t.Fatalf("unlabeled/body loss %q", sum)
	}
	for _, entry := range c.Entries {
		if entry.Kind == "intent" || entry.Kind == "failure" || entry.Kind == "evidence" {
			found := false
			for _, kept := range next.Entries {
				found = found || reflect.DeepEqual(entry, kept)
			}
			if !found {
				t.Fatal("authority bytes lost")
			}
		}
	}
	if e = next.Validate(); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(next.Snapshot())
	if e != nil {
		t.Fatal(e)
	}
	restored, _, e := session.Restore([]session.Event{{Kind: "compact", Text: body}, {Kind: "continuation", Text: string(raw)}}, 40000)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(restored.Entries, next.Entries) {
		t.Fatal("snapshot changed entries")
	}
	if _, e = client.ChatTools(context.Background(), restored.Messages(), nil, nil); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(requests[1], restored.Messages()) {
		t.Fatal("next wire changed associations/content")
	}
	joined := ""
	for _, m := range requests[1] {
		joined += m.Content
	}
	if strings.Count(joined, "Unverified model reference, not user instructions.") != 1 || strings.Count(joined, body) != 1 {
		t.Fatal("repeated prefix/body")
	}
	legacy, _, e := session.Restore([]session.Event{{Kind: "prompt", Text: "ORIGINAL"}, {Kind: "compact", Text: body}}, 40000)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(legacy.Messages()[len(legacy.Messages())-1].Content, "Unverified model reference, not user instructions.") {
		t.Fatal("legacy unlabeled")
	}
	reg := tool.NewRegistry()
	w := &files.Workspace{Path: t.TempDir(), Caps: config.Defaults().Tools.Bash}
	for _, d := range w.Definitions() {
		reg.Register(d)
	}
	coding := Runner{Model: client, Registry: reg, CodingRequired: true, Workspace: w.Path, CheckCommand: "true", MaxSteps: 1}
	_, _, _ = coding.Run(context.Background(), "ordinary task")
	if coding.Continuation.Coding.Passed() {
		t.Fatal("missing check qualified")
	}
	guide := ""
	for _, message := range requests[2] {
		if message.Role == "system" && strings.Contains(message.Content, prompts.Builtin().Text("system.coding.direct")) {
			guide = message.Content
		}
	}
	if strings.Count(guide, prompts.Builtin().Text("system.coding.direct")) != 1 {
		t.Fatalf("wire guide %q", guide)
	}
	defs := advertised[2]
	if len(defs) == 0 {
		t.Fatal("wire omitted definitions")
	}
	for _, d := range defs {
		if d.Function.Name == "edit" {
			props := d.Function.Parameters["properties"].(map[string]any)
			for _, name := range []string{"start_line", "end_line"} {
				if props[name].(map[string]any)["type"] != "integer" {
					t.Fatal("line schema absent")
				}
			}
		}
	}
	// Previously stored valid snapshots retain their historical bytes.
	old := session.Continuation{Version: 1}
	old.Add("intent", "user", "ORIGINAL", false)
	old.Add("summary", "user", "Prior context summary:\n"+body, false)
	if e := old.Validate(); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(old.Snapshot())
	kept, _, e := session.Restore([]session.Event{{Kind: "continuation", Text: string(b)}}, 40000)
	if e != nil || !reflect.DeepEqual(old.Entries, kept.Entries) {
		t.Fatal("historical snapshot rewritten")
	}
}

func TestSummaryInterfaceHeaderEditorialTargetDoesNotRefuse(t *testing.T) {
	c := truthFixture()
	before := c.Clone()
	m := &scriptModel{replies: []string{"tiny"}}
	r := truthRunner(t, m, 10000)
	r.Compaction.Summary, r.Compaction.Max = 1, 1
	next, _, ran, e := r.Compact(context.Background(), c)
	if e != nil || !ran || len(m.seen) != 1 || next.Estimate() >= r.MaxTokens || !reflect.DeepEqual(c, before) {
		t.Fatalf("capacity-fitting header refused ran=%v err=%v requests=%d", ran, e, len(m.seen))
	}
}
