package agent

import (
	"strings"
	"testing"
)

// Capacity reduction must keep knowledge entries visible (key + digest)
// after stubbing their bodies; dropping the entries entirely makes the
// judge see no product knowledge and request appends that already exist.
func TestReduceForCapacityKeepsKnowledgeHeads(t *testing.T) {
	p := progressPacket{Facts: BookkeepingFacts{Knowledge: []map[string]string{
		{"key": "project/design/identity", "body": strings.Repeat("a", 5000), "task": "", "path": ""},
		{"key": "project/simulation/design", "body": strings.Repeat("b", 6000), "task": "", "path": ""},
		{"key": "project/testing/strategy", "body": strings.Repeat("c", 4000), "task": "", "path": ""},
	}}}
	// Reduction is one disclosed step per capacity retry; stop as soon as
	// bodies are stubbed, mirroring a packet that then fits admission.
	for i := 0; i < 10; i++ {
		if !p.reduceForCapacity() {
			break
		}
		if f, ok := p.Facts.(BookkeepingFacts); ok && len(f.Knowledge) > 0 && f.Knowledge[0]["body"] == "" && f.Knowledge[0]["sha256"] != "" {
			break
		}
	}
	facts, ok := p.Facts.(BookkeepingFacts)
	if !ok {
		t.Fatalf("facts type changed: %T", p.Facts)
	}
	if len(facts.Knowledge) != 3 {
		t.Fatalf("knowledge entries dropped under capacity reduction: %d", len(facts.Knowledge))
	}
	for _, e := range facts.Knowledge {
		if e["key"] == "" {
			t.Fatal("knowledge key lost")
		}
		if len(e["body"]) > 200 {
			t.Fatalf("knowledge body for %s was not stubbed (%d chars)", e["key"], len(e["body"]))
		}
		if e["sha256"] == "" || e["bytes"] == "" {
			t.Fatalf("knowledge head for %s lacks digest metadata: %v", e["key"], e)
		}
	}
}
