package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ConsumptionReceipt identifies only unread records represented in one actual
// successful request. Kinds repairs provenance omitted by legacy text-result
// events; no output body or execution authority is copied.
type ConsumptionReceipt struct {
	Count  int      `json:"count"`
	SHA256 string   `json:"sha256"`
	Kinds  []string `json:"kinds"`
}

func (c Continuation) Consumption() ConsumptionReceipt {
	h := sha256.New()
	var kinds []string
	for _, entry := range c.Entries {
		if !entry.Unread || entry.Kind == "injected" {
			continue
		}
		_ = json.NewEncoder(h).Encode(struct {
			Kind    string
			Message any
		}{entry.Kind, entry.Message})
		kinds = append(kinds, entry.Kind)
	}
	return ConsumptionReceipt{Count: len(kinds), SHA256: hex.EncodeToString(h.Sum(nil)), Kinds: kinds}
}

// Consume requires the exact current projection. Protection is independent of
// consumption and is never removed here.
func (c *Continuation) Consume(receipt ConsumptionReceipt) bool {
	return c.consume(receipt, nil)
}

// rawText identifies only untyped result events replayed since the snapshot.
// Reconcile their omitted provenance on a copy, then commit atomically after
// the entire ordered message/kind projection matches. Native/snapshot records
// cannot be reclassified and already protected kinds cannot be downgraded.
func (c *Continuation) consume(receipt ConsumptionReceipt, rawText map[int]bool) bool {
	if receipt.Count <= 0 || len(receipt.Kinds) != receipt.Count {
		return false
	}
	candidate := *c
	candidate.Entries = append([]Entry(nil), c.Entries...)
	n := 0
	for i := range candidate.Entries {
		e := &candidate.Entries[i]
		if !e.Unread || e.Kind == "injected" {
			continue
		}
		if n >= len(receipt.Kinds) {
			return false
		}
		kind := receipt.Kinds[n]
		switch kind {
		case "output", "schema", "failure", "answer":
		default:
			return false
		}
		if e.Kind != kind {
			if !rawText[i] || e.Kind != "output" || e.Message.Role != "user" || len(e.Message.ToolCalls) != 0 || e.Message.ToolCallID != "" || e.Message.Name != "" {
				return false
			}
			e.Kind = kind
		}
		n++
	}
	actual := candidate.Consumption()
	if n != receipt.Count || actual.SHA256 != receipt.SHA256 {
		return false
	}
	for i := range candidate.Entries {
		if candidate.Entries[i].Kind != "injected" {
			candidate.Entries[i].Unread = false
		}
	}
	c.Entries = candidate.Entries
	return true
}
