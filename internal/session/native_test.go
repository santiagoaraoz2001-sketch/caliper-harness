package session

import (
	"encoding/json"
	"reflect"
	"specific-harness/internal/llm"
	"strings"
	"testing"
)

func nativeFixture() Continuation {
	return Continuation{Version: 1, SchemaBytes: 100, SchemaKey: "schema", Entries: []Entry{{Message: llm.Message{Role: "user", Content: "intent"}, Kind: "intent"}, {Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "one", Name: "bash", Arguments: `{"cmd":"echo  x\r\n"}`}}}, Kind: "reply"}, {Message: llm.Message{Role: "tool", ToolCallID: "one", Name: "bash", Content: "real output"}, Kind: "output"}}}
}
func TestNativeContinuationGroupsCloneAndRestore(t *testing.T) {
	c := nativeFixture()
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	cl := c.Clone()
	cl.Entries[1].Message.ToolCalls[0].Arguments = "{}"
	if c.Entries[1].Message.ToolCalls[0].Arguments == "{}" {
		t.Fatal("Clone aliased calls")
	}
	msgs := c.Messages()
	msgs[1].ToolCalls[0].ID = "changed"
	if c.Entries[1].Message.ToolCalls[0].ID != "one" {
		t.Fatal("Messages aliased calls")
	}
	protected := c.Clone()
	c.Entries[2].Unread = true
	keep := c.Protected(3)
	if !keep[1] || !keep[2] {
		t.Fatal("unread group not protected")
	}
	encoded, e := protected.Encode()
	if e != nil {
		t.Fatal(e)
	}
	restored, _, e := Restore([]Event{{Kind: "continuation", Text: encoded}}, 0)
	if e != nil || restored.Entries[1].Message.ToolCalls[0].Arguments != protected.Entries[1].Message.ToolCalls[0].Arguments {
		t.Fatalf("restore %+v %v", restored, e)
	}
	raw, _ := json.Marshal(protected.Entries[1])
	crash, _, e := Restore([]Event{{Kind: "prompt", Text: "intent"}, {Kind: "native_reply", Text: string(raw)}}, 0)
	if e != nil || len(crash.Entries) != 3 || crash.Entries[2].Kind != "failure" || !strings.Contains(crash.Entries[2].Message.Content, "outcome unknown") {
		t.Fatalf("crash %+v %v", crash, e)
	}
	c = protected
	c.SchemaBytes = 0
	c.SchemaKey = ""
	retained, notice, e := Restore([]Event{{Kind: "continuation", Text: mustEncode(t, c)}}, c.Entries[0].Message.Size()+20)
	if e != nil || !reflect.DeepEqual(retained.Entries, c.Entries) || !strings.Contains(notice, "retained history") {
		t.Fatalf("native group was lost during pressure resume %+v %v %s", retained, e, notice)
	}
}
func mustEncode(t *testing.T, c Continuation) string {
	t.Helper()
	s, e := c.Encode()
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestNativeContinuationRejectsInvalidAssociations(t *testing.T) {
	for _, mutate := range []func(*Continuation){func(c *Continuation) { c.Entries[1].Message.ToolCallID = "stray" }, func(c *Continuation) { c.Entries[1].Message.ToolCalls[0].ID = "bad id" }, func(c *Continuation) { c.Entries[2].Message.Name = "different" }, func(c *Continuation) { c.Entries = c.Entries[:2] }, func(c *Continuation) { c.Entries[1].Message.ToolCalls[0].Arguments = `{"cmd":{}}` }} {
		c := nativeFixture()
		mutate(&c)
		if e := c.Validate(); e == nil {
			t.Fatalf("invalid admitted %+v", c)
		}
	}
}
