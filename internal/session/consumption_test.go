package session

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/llm"
)

func TestConsumptionSnapshotRawTailParity(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "native"}[native], func(t *testing.T) {
			base := Continuation{Version: 1}
			base.Add("intent", "user", "Preserve the actual user's choices", false)
			encoded, _ := base.Encode()
			events := []Event{{Kind: "continuation", Text: encoded}}
			live := base.Clone()
			add := func(entry Entry, kind string) {
				live.Entries = append(live.Entries, entry)
				raw, _ := json.Marshal(entry)
				if kind == "result" {
					raw = []byte(entry.Message.Content)
				}
				events = append(events, Event{Kind: kind, Text: string(raw)})
			}
			if native {
				add(Entry{Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "read", Name: "read", Arguments: `{}`}, {ID: "ask", Name: "ask", Arguments: `{"question":"Aesthetic?"}`}}}}, "native_reply")
				add(Entry{Kind: "output", Unread: true, Message: llm.Message{Role: "tool", Name: "read", ToolCallID: "read", Content: "actual source"}}, "native_result")
				add(Entry{Kind: "output", Unread: true, Message: llm.Message{Role: "tool", Name: "ask", ToolCallID: "ask", Content: "Keep the original identity"}}, "native_result")
			} else {
				add(Entry{Kind: "output", Unread: true, Message: llm.Message{Role: "user", Content: "actual source"}}, "result")
			}
			receipt := live.Consumption()
			raw, _ := json.Marshal(receipt)
			if len(raw) > 160 || strings.Contains(string(raw), "actual source") {
				t.Fatal("receipt copied output", string(raw))
			}
			events = append(events, Event{Kind: "consumed", Text: string(raw)})
			if !live.Consume(receipt) {
				t.Fatal("matching request not consumed")
			}
			// A result produced after that successful request was never represented.
			if native {
				add(Entry{Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "next", Name: "read", Arguments: `{}`}}}}, "native_reply")
				add(Entry{Kind: "output", Unread: true, Message: llm.Message{Role: "tool", Name: "read", ToolCallID: "next", Content: "not yet seen"}}, "native_result")
			} else {
				add(Entry{Kind: "output", Unread: true, Message: llm.Message{Role: "user", Content: "not yet seen"}}, "result")
			}
			restored, _, err := Restore(events, 10000)
			if err != nil || !reflect.DeepEqual(restored.Entries, live.Entries) {
				t.Fatalf("tail/snapshot mismatch: %v\ngot %+v\nwant %+v", err, restored.Entries, live.Entries)
			}
			if !restored.Entries[len(restored.Entries)-1].Unread {
				t.Fatal("new result consumed prematurely")
			}
			if native {
				keep := restored.Protected(3)
				if !keep[1] || !keep[3] || restored.Entries[3].Unread {
					t.Fatal("consumed ask association/protection lost", keep)
				}
			}
			encoded, _ = live.Encode()
			snapshot, _, err := Restore([]Event{{Kind: "continuation", Text: encoded}}, 10000)
			if err != nil || !reflect.DeepEqual(snapshot.Entries, restored.Entries) {
				t.Fatal("snapshot disagrees with tail", err)
			}
		})
	}
}

func TestConsumptionMissingFailedAndMismatchedEvidenceStaysUnread(t *testing.T) {
	base := Continuation{Version: 1}
	base.Add("intent", "user", "actual request", false)
	base.Add("output", "user", "original source", true)
	encoded, _ := base.Encode()
	raw, _ := json.Marshal(base.Consumption())
	for _, event := range []Event{
		{Kind: "reply", Text: "legacy response without processing receipt"},
		{Kind: "partial", Text: "unfinished response"},
		{Kind: "failed", Text: "capacity refusal"},
		{Kind: "cancelled", Text: "cancelled request"},
		{Kind: "model_request", Text: `{"error":"EOF"}`},
		{Kind: "consumed", Text: `{"count":1,"sha256":"wrong"}`},
		{Kind: "consumed", Text: "invalid"},
	} {
		got, _, err := Restore([]Event{{Kind: "continuation", Text: encoded}, event}, 10000)
		if err != nil || !got.Entries[1].Unread {
			t.Fatal("unknown/failed processing consumed source", event.Kind, err)
		}
	}
	// An exact old receipt cannot consume a changed output or an extra unseen one.
	for _, change := range []bool{false, true} {
		c := base.Clone()
		if change {
			c.Entries[1].Message.Content = "changed source"
		} else {
			c.Add("output", "user", "extra source", true)
		}
		encoded, _ := c.Encode()
		got, _, err := Restore([]Event{{Kind: "continuation", Text: encoded}, {Kind: "consumed", Text: string(raw)}}, 10000)
		if err != nil || !got.Entries[1].Unread {
			t.Fatal("stale receipt minted consumption", err)
		}
	}
}

func TestConsumptionProvenanceReconciliationIsAtomicAndRestricted(t *testing.T) {
	live := Continuation{Version: 1}
	live.Add("failure", "user", "first result", true)
	live.Add("schema", "user", "second result", true)
	receipt := live.Consumption()
	for _, mode := range []string{"match", "changed", "unknown", "short", "extra", "native", "snapshot", "downgrade"} {
		t.Run(mode, func(t *testing.T) {
			c := live.Clone()
			c.Entries[0].Kind = "output"
			c.Entries[1].Kind = "output"
			raw := map[int]bool{0: true, 1: true}
			r := receipt
			r.Kinds = append([]string(nil), receipt.Kinds...)
			switch mode {
			case "changed":
				c.Entries[1].Message.Content = "changed second result"
			case "unknown":
				r.Kinds[1] = "intent"
			case "short":
				r.Kinds = r.Kinds[:1]
			case "extra":
				c.Add("output", "user", "newer unread", true)
			case "native":
				c.Entries[0].Message.Role = "tool"
			case "snapshot":
				delete(raw, 0)
			case "downgrade":
				c.Entries[0].Kind = "failure"
				target := c.Clone()
				target.Entries[0].Kind = "output"
				r = target.Consumption()
			}
			before := c.Clone()
			ok := c.consume(r, raw)
			if mode == "match" {
				if !ok || c.Entries[0].Kind != "failure" || c.Entries[1].Kind != "schema" || c.Entries[0].Unread || c.Entries[1].Unread {
					t.Fatal("exact provenance failed", c)
				}
			} else if ok || !reflect.DeepEqual(c, before) {
				t.Fatal("invalid projection mutated context", mode, c, before)
			}
		})
	}
}
