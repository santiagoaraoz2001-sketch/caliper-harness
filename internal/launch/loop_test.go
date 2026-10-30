package launch

import (
	"context"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
)

// loopModel replays canned replies, then ends the run with plain text.
type loopModel struct {
	replies []string
	seen    [][]llm.Message
	usage   llm.Usage
}

func (m *loopModel) Chat(_ context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	m.seen = append(m.seen, msgs)
	if len(m.replies) == 0 {
		return "out of script", "loop", m.usage, nil
	}
	r := m.replies[0]
	m.replies = m.replies[1:]
	return r, "loop", m.usage, nil
}

func TestSubagentRepeatedRefusalsReceiveAdviceAndContinue(t *testing.T) {
	s, _, ids := setup(t, 1)
	task, err := plan.GetTask(s, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	unknown := "```tool\ntool: nope\nx: 1\n```"
	model := &loopModel{replies: []string{unknown, unknown, unknown, unknown, unknown, unknown, "actual final role reply"}}
	d := depsFor(s, nil)
	d.NewModel = func() agent.Model { return model }
	d.MaxSteps = 8
	judgments := 0
	d.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judgments++
		return decision.Verdict{Option: q.Options[2], Confidence: .9}, nil
	}
	d.Threshold, d.LoopRepeat, d.LoopStall = .7, 3, 3
	res := RunTask(t.Context(), d, task, 0)
	if res.Err != nil || res.Output != "actual final role reply" || res.Outcome.Status != outcome.Completed || len(model.seen) != 7 || judgments != 2 {
		t.Fatalf("progress became a child ceiling: output=%q outcome=%+v turns=%d judgments=%d error=%v", res.Output, res.Outcome, len(model.seen), judgments, res.Err)
	}
	actual, err := plan.GetTask(s, task.ID)
	if err != nil || actual.State == plan.TaskDone || res.Qualified() {
		t.Fatal("standalone loop probe fabricated qualified task", err)
	}
}

func writeLegacyLaunchCatalog(t *testing.T, dir string, changes map[string]string) {
	t.Helper()
	doc := map[string]map[string]string{}
	if err := yaml.Unmarshal([]byte(prompts.Builtin().YAML()), &doc); err != nil {
		t.Fatal(err)
	}
	for key, text := range changes {
		doc["texts"][key] = text
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompts.yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSubagentInheritsCompaction(t *testing.T) {
	s, _, ids := setup(t, 1)
	task, err := plan.GetTask(s, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeLegacyLaunchCatalog(t, dir, map[string]string{"templates.compaction": "WORDS={{.Words}}\n{{.Range}}", "templates.subagent": "{{.Title}} {{.Prompt}}"})
	// A substantial brief and roughly four content bytes per reported input
	// token exercise compaction without making its rendered request over budget.
	task.Prompt = strings.Repeat("source context ", 140)
	const window = 4000
	big := "```tool\ntool: nope\nx: " + strings.Repeat("z", 12800) + "\n```"
	model := &loopModel{replies: []string{big, "Requested tool was unavailable; no work was executed.", "done"},
		usage: llm.Usage{In: 850, Out: 5, Reported: true}}
	d := depsFor(s, nil)
	d.NewModel = func() agent.Model { return model }
	d.Compaction = agent.Compaction{Trigger: 90, Keep: 40, Summary: 10, Step: 1, Max: 20}
	readyJudge := d.Judge
	d.Judge = func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
		if req.Options[0] == "the summary is factually consistent with essential anchors" {
			// Explicit component acceptance, not native summary quality evidence.
			return decision.Verdict{Option: req.Options[0], Confidence: 1}, nil
		}
		return readyJudge(ctx, req)
	}
	d.TemplatesDir = dir
	res := RunTask(context.Background(), d, task, window)
	if res.Err != nil {
		t.Fatalf("err=%v", res.Err)
	}
	if res.Output != "done" {
		t.Fatalf("child did not continue after compaction: %q", res.Output)
	}
	requestBytes := 0
	for _, msg := range model.seen[0] {
		requestBytes += len(msg.Content)
	}
	if requestBytes < 3*model.usage.In || requestBytes > 5*model.usage.In {
		t.Fatalf("unrealistic fixture calibration: %d bytes / %d input tokens", requestBytes, model.usage.In)
	}
	t.Logf("initial request=%d bytes, reported input=%d tokens; child outcome=%s", requestBytes, model.usage.In, res.Outcome.Status)
	// The rendered initial context calibrates this canned reply's next pressure.
	nextBytes := requestBytes + len(big)
	failureBytes := 0
	for _, msg := range res.Transcript {
		if msg.Role == "user" && strings.HasPrefix(msg.Content, "unknown tool ") {
			failureBytes += len(msg.Content)
		}
	}
	if failureBytes == 0 {
		t.Fatal("actual refusal missing from retained transcript")
	}
	nextBytes += failureBytes
	nextEstimate := (nextBytes*model.usage.In + requestBytes - 1) / requestBytes
	t.Logf("actual next pressure=%d bytes (%d refusal bytes), estimate=%d tokens", nextBytes, failureBytes, nextEstimate)
	if nextEstimate <= 3600 || nextEstimate >= window {
		t.Fatalf("reply pressure estimate=%d must trigger above 3600 and stay below %d", nextEstimate, window)
	}
	for _, msgs := range model.seen {
		if len(msgs) == 2 && strings.Contains(msgs[0].Content, "WORDS=") {
			for _, original := range model.seen[0] {
				if original.Role == "system" && strings.Contains(msgs[1].Content, original.Content) {
					t.Error("host system instructions entered summary source")
				}
			}
			if !strings.Contains(msgs[1].Content, "zzzz") {
				t.Errorf("summary range missing the compacted block")
			}
			summaryBytes := len(msgs[0].Content) + len(msgs[1].Content)
			estimate := (summaryBytes*model.usage.In + requestBytes - 1) / requestBytes
			t.Logf("actual summary call=%d bytes, content estimate=%d tokens, window=%d; range contains original reply", summaryBytes, estimate, window)
			if estimate >= window {
				t.Errorf("summary fixture input estimate %d reaches window %d", estimate, window)
			}
			for _, msg := range res.Transcript {
				if msg.Content == session.SummaryPrefix+"Requested tool was unavailable; no work was executed." {
					return
				}
			}
			t.Error("child transcript missing the applied summary")
			return
		}
	}
	t.Errorf("no compaction summary call in %d seen calls", len(model.seen))
}

func TestSubagentAdviceAtConfidenceFloorContinuesHeadless(t *testing.T) {
	s, _, ids := setup(t, 1)
	task, err := plan.GetTask(s, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	unknown := "```tool\ntool: nope\nx: 1\n```"
	model := &loopModel{replies: []string{unknown, unknown, unknown, unknown, unknown, unknown, "actual final role reply"}}
	d := depsFor(s, nil)
	d.NewModel = func() agent.Model { return model }
	d.MaxSteps = 8
	judgments := 0
	d.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judgments++
		return decision.Verdict{Option: q.Options[2], Confidence: .5}, nil
	}
	d.Threshold, d.LoopRepeat, d.LoopStall = .7, 3, 3
	res := RunTask(t.Context(), d, task, 0)
	if res.Err != nil || res.Output != "actual final role reply" || len(model.seen) != 7 || judgments != 2 {
		t.Fatalf("advisory direction became stop: %+v %v", res.Outcome, res.Err)
	}
	advice := false
	for _, message := range model.seen[6] {
		advice = advice || message.Role == "user" && strings.Contains(message.Content, prompts.Builtin().Text("continuation.discovery.reconsider"))
		if message.Role == "user" && strings.Contains(message.Content, prompts.Builtin().Text("continuation.discovery.uncertain_direction_prefix")) {
			t.Fatal("authorization risk threshold still qualified the valid advisory nudge as uncertain")
		}
	}
	if !advice || res.Qualified() {
		t.Fatal("valid floor-confidence direction lost or qualification fabricated")
	}
}
