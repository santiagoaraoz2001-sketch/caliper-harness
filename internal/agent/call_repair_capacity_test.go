package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
)

func TestCallRepairCapacityLargestHistoricalBodyFirst(t *testing.T) {
	current := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "current", Name: "execute", Arguments: `{"cmd":"npm test"}`}}}
	packet := callRepairPacket{
		OriginalName: "execute", AuthoredArguments: tool.Args{"cmd": "npm test"}, Refusal: "unknown tool",
		Candidates:   []repairCandidate{{Name: "bash", Schema: "complete schema", Arguments: tool.Args{"cmd": "npm test"}}},
		CurrentReply: []llm.Message{current},
		WorkerContext: []llm.Message{
			{Role: "system", Content: strings.Repeat("protected system ", 1000)},
			{Role: "user", Content: strings.Repeat("protected request ", 1000)},
			{Role: "tool", Name: "find_dir", ToolCallID: "old-small", Content: strings.Repeat("no matches ", 30)},
			{Role: "assistant", ReasoningContent: strings.Repeat("old reasoning ", 100)},
			{Role: "tool", Name: "read", ToolCallID: "old-large", Content: strings.Repeat("actual source ", 1000)},
		},
	}
	before, _ := json.Marshal(packet)
	originalLarge, _ := json.Marshal(packet.WorkerContext[4])
	if !packet.reduceForCapacity() {
		t.Fatal("large eligible body was not reduced")
	}
	after, _ := json.Marshal(packet)
	if len(after) >= len(before) || packet.WorkerContext[4].Content != "" || packet.WorkerContext[2].Content == "" || packet.WorkerContext[3].ReasoningContent == "" {
		t.Fatal("capacity reduction removed small history before the largest body")
	}
	if got := packet.Progress.CapacityOmissions; len(got) != 1 || got[0].Field != "WorkerContext[4]" || got[0].SHA256 != fmt.Sprintf("%x", sha256.Sum256(originalLarge)) || got[0].Bytes != len(originalLarge) {
		t.Fatalf("omission provenance changed: %+v", got)
	}
	// Restore only the disclosed historical body; every other field, metadata,
	// user/system instruction, current call and candidate must match exactly.
	var restored callRepairPacket
	if err := json.Unmarshal(before, &restored); err != nil {
		t.Fatal(err)
	}
	packet.WorkerContext[4] = restored.WorkerContext[4]
	packet.Progress.CapacityOmissions = nil
	if !reflect.DeepEqual(packet, restored) {
		t.Fatal("capacity reduction altered immutable or undisclosed evidence")
	}
}

func TestCallRepairCapacityNoUsefulHistoricalReduction(t *testing.T) {
	for _, msg := range []llm.Message{
		{Role: "tool", Name: "read", ToolCallID: "empty"},
		{Role: "assistant", Content: "x"},
		{Role: "user", Content: strings.Repeat("retain ", 1000)},
		{Role: "system", Content: strings.Repeat("retain ", 1000)},
	} {
		packet := callRepairPacket{WorkerContext: []llm.Message{msg}, CurrentReply: []llm.Message{{Role: "assistant", Content: "current"}}}
		before, _ := json.Marshal(packet)
		if packet.reduceForCapacity() {
			t.Fatalf("unhelpful or protected message was reduced: %+v", msg)
		}
		after, _ := json.Marshal(packet)
		if string(after) != string(before) {
			t.Fatal("failed reduction changed the packet")
		}
	}
}
