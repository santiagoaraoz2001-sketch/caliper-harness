package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/tool"
)

type nativeRulithRecorder struct {
	*llm.Client
	requests [][]llm.Message
}

func (m *nativeRulithRecorder) ChatTools(ctx context.Context, messages []llm.Message, defs []llm.ToolDefinition, delta func(string)) (llm.Reply, error) {
	m.requests = append(m.requests, append([]llm.Message(nil), messages...))
	return m.Client.ChatTools(ctx, messages, defs, delta)
}

// Opt-in, serial native gate: no server launch/configuration or game access.
// The caller must establish that the selected existing server is idle.
func TestNativeRulithOutputLimitContinuation(t *testing.T) {
	endpoint := os.Getenv("CALIPER_NATIVE_RULITH_URL")
	model := os.Getenv("CALIPER_NATIVE_RULITH_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("requires an explicitly selected idle existing Rulith server")
	}
	dir := t.TempDir()
	evidence := os.Getenv("CALIPER_NATIVE_RULITH_EVIDENCE")
	if evidence == "" {
		evidence = dir
	}
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	value := "alpha α 🚚\nline two: backslash \\ and quoted \"text\"\n" + strings.Repeat("preserve-this-prefix ", 6)
	encoded, _ := json.Marshal(value)
	path := filepath.Join(dir, "record.txt")
	effects := 0
	reg := tool.NewRegistry()
	reg.Register(tool.Definition{Name: "record", Desc: "Record the exact requested text once.", Params: []tool.Param{{Name: "value", Type: tool.TypeString, Required: true}}, Execute: func(_ context.Context, args tool.Args) (tool.Result, error) {
		effects++
		got := args["value"].(string)
		if got != value {
			t.Errorf("native generated argument differs: got %q want %q", got, value)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return tool.Result{}, err
		}
		defer f.Close()
		_, err = f.WriteString(got)
		return tool.Result{Text: "Recorded successfully. No more tool calls needed."}, err
	}})
	var measurements []llm.RequestMeasurement
	client := llm.New(endpoint, model).WithRuntime(llm.RuntimeRulith).WithParams(llm.Params{Temperature: 0, MaxOutputTokens: 16}).WithRequestObserver(func(m llm.RequestMeasurement) { measurements = append(measurements, m) })
	recorder := &nativeRulithRecorder{Client: client}
	var output bytes.Buffer
	r := &agent.Runner{Model: recorder, Registry: reg, Session: completedLog{dir: evidence}, SessionID: "native-output-boundary", MaxSteps: 2, MaxTokens: 262144, Out: &output}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	final, _, err := r.Run(ctx, "Call record exactly once with value equal to this JSON string: "+string(encoded)+". After the tool succeeds, reply only done. Do not call any other tool.")
	report := map[string]any{"endpoint": endpoint, "model": model, "final": final, "effects": effects, "outcome": r.Outcome, "requests": measurements, "outbound_messages": recorder.requests, "input": r.UsedIn, "output": r.UsedOut, "semantics": "exact rendered/raw byte prefix retained; backend retokenizes; no original-token identity claim"}
	if err != nil {
		report["error"] = err.Error()
	}
	raw, _ := json.MarshalIndent(report, "", "  ")
	_ = os.WriteFile(filepath.Join(evidence, "native-output-boundary-report.json"), raw, 0600)
	if err != nil || r.Outcome.Status != outcome.Completed || effects != 1 {
		t.Fatalf("native result: final=%q err=%v outcome=%+v effects=%d requests=%d output=%s", final, err, r.Outcome, effects, len(measurements), output.String())
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != value {
		t.Fatalf("native effect mismatch/repetition: %q %v", got, readErr)
	}
	if len(measurements) < 4 {
		t.Fatalf("native gate did not cross an output boundary: requests=%d", len(measurements))
	}
	users, results := 0, 0
	for _, m := range recorder.requests[len(recorder.requests)-1] {
		if m.Role == "user" {
			users++
		}
		if m.Role == "tool" {
			results++
		}
		if strings.Contains(m.Content, "Unverified unfinished") {
			t.Fatal("continued raw prefix was added as conversation prose")
		}
	}
	if users != 1 || results != 1 {
		t.Fatalf("native recovery added user prompts/effect receipts: users=%d results=%d", users, results)
	}
	for i := 1; i < len(recorder.requests)-1; i++ {
		if !reflect.DeepEqual(recorder.requests[0], recorder.requests[i]) {
			t.Fatalf("physical continuation %d changed outbound conversation", i)
		}
	}
	t.Logf("native passed: physical requests=%d effects=%d input=%d output=%d", len(measurements), effects, r.UsedIn, r.UsedOut)
}
