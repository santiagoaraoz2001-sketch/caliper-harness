package session

import (
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/llm"
	"strings"
	"testing"
)

func TestLargeJSONLEventAndContinuationExactReopen(t *testing.T) {
	for _, size := range []int{1000, 1100000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			dir := t.TempDir()
			text := strings.Repeat("x", size) + "\nquoted:\" and slash\\ and é"
			c := Continuation{Version: 1}
			c.Add("intent", "user", text, false)
			encoded, err := c.Encode()
			if err != nil {
				t.Fatal("valid source-sized continuation refused", err)
			}
			for _, event := range []Event{{Kind: "prompt", Text: text}, {Kind: "continuation", Text: encoded}, {Kind: "note", Text: "next"}} {
				if err := Append(dir, "large", event); err != nil {
					t.Fatal(err)
				}
			}
			events, err := Read(dir, "large")
			if err != nil || len(events) != 3 || events[0].Text != text || events[1].Text != encoded {
				t.Fatal("appended event cannot reopen byte-exact", len(events), err)
			}
			restored, _, err := Restore(events, 0)
			if err != nil || restored.Entries[0].Message.Content != text {
				t.Fatal("large intent lost on restore", err)
			}
		})
	}
}
func TestJSONLStreamingPreservesMalformedAndTailErrors(t *testing.T) {
	for _, bad := range []string{"{", "{} {}\n", "\n", "{\"kind\":\"note\"\n"} {
		dir := t.TempDir()
		if err := Append(dir, "corrupt", Event{Kind: "note", Text: "valid"}); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(filepath.Join(dir, "corrupt.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(bad)
		f.Close()
		if _, err := Read(dir, "corrupt"); err == nil {
			t.Fatal("malformed/incomplete stream accepted", bad)
		}
	}
	// Removing a storage-size wall must retain correlation/shape admission.
	c := Continuation{Version: 1, Entries: []Entry{{Kind: "intent", Message: llm.Message{Role: "user", Content: strings.Repeat("x", 1100000)}}, {Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call", Name: "read", Arguments: `{}`}}}}}}
	if c.Validate() == nil {
		t.Fatal("incomplete native group admitted despite no byte ceiling")
	}
}

func TestLargeRawJSONLEventReadWithoutSnapshot(t *testing.T) {
	dir := t.TempDir()
	text := strings.Repeat("\"\\\n", 400000)
	if err := Append(dir, "raw", Event{Kind: "note", Text: text}); err != nil {
		t.Fatal(err)
	}
	events, err := Read(dir, "raw")
	if err != nil || len(events) != 1 || events[0].Text != text {
		t.Fatalf("valid appended large raw record cannot reopen: events%d %v", len(events), err)
	}
}
