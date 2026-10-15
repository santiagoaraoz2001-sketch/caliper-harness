package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/tools/files"
)

func TestDiscoveryNavigationReceivesAdviceWithoutRequiringSourceRead(t *testing.T) {
	m := &discoveryModel{}
	calls := 0
	sink := &recordSink{}
	r := discoveryRunner(t, m, func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		calls++
		var packet struct {
			LatestSourceText string
			Checkpoint       ProgressCheckpoint
		}
		if err := json.Unmarshal([]byte(q.Context), &packet); err != nil {
			t.Fatal(err)
		}
		if packet.Checkpoint.RunStep != 5 || packet.LatestSourceText != "" {
			t.Errorf("navigation bypassed advisory cadence or fabricated source: step%d source%q", packet.Checkpoint.RunStep, packet.LatestSourceText)
		}
		return decision.Verdict{Option: discoveryActOption, Confidence: .9}, nil
	}, sink)
	r.Registry.Register((&files.Workspace{Path: r.Workspace, Caps: config.Defaults().Tools.Bash}).FindDirDefinition())
	navigation := llm.Reply{Message: llm.Message{Role: "assistant"}, FinishReason: "tool_calls"}
	for i := 0; i < 5; i++ {
		path := fmt.Sprintf("navigation%d", i)
		if err := os.MkdirAll(filepath.Join(r.Workspace, path, "child"), 0700); err != nil {
			t.Fatal(err)
		}
		navigation.Message.ToolCalls = append(navigation.Message.ToolCalls, llm.ToolCall{ID: path, Name: "find_dir", Arguments: fmt.Sprintf(`{"path":%q}`, path)})
	}
	m.replies = []llm.Reply{navigation, discoveryReply("0")}
	if _, _, err := r.Run(context.Background(), "inspect the owner then repair this task"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(m.seen) != 3 {
		t.Fatalf("navigation cadence failed to continue: judgments%d requests%d", calls, len(m.seen))
	}
	// These real directory effects succeeded; navigation remained available.
	results, directories := 0, 0
	for i, k := range sink.kinds {
		if k == "native_result" {
			results++
			var e session.Entry
			if err := json.Unmarshal([]byte(sink.texts[i]), &e); err != nil {
				t.Fatal(err)
			}
			if e.Kind != "output" {
				t.Fatal("navigation/source fixture did not succeed", e.Kind, e.Message.Content)
			}
			if e.Message.Name == "find_dir" {
				directories++
			}
		}
	}
	if directories != 5 {
		t.Fatalf("successful navigation receipts%d; want5", directories)
	}
	if results != 6 {
		t.Fatalf("actual settled receipts%d; want5 navigation plus first source", results)
	}
	if got := m.seen[1]; len(got) == 0 || !strings.Contains(got[len(got)-1].Content, r.Prompts.Text("continuation.discovery.act")) {
		t.Fatal("navigation direction did not reach next actual request")
	}
}
