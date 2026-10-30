package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"strings"
	"testing"
	"time"
)

func evidenceAPI(t *testing.T, s *store.Store, dir, root string) *API {
	t.Helper()
	a, err := New(s, "evidence-test", dir).Scoped(root, "current", true)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func evidenceAppend(t *testing.T, dir, id, kind, text string) {
	t.Helper()
	if err := session.Append(dir, id, session.Event{Kind: kind, Text: text}); err != nil {
		t.Fatal(err)
	}
}
func evidenceNative(t *testing.T, dir, id, kind string, m llm.Message) {
	t.Helper()
	raw, err := json.Marshal(session.Entry{Message: m, Kind: "output"})
	if err != nil {
		t.Fatal(err)
	}
	evidenceAppend(t, dir, id, kind, string(raw))
}
func evidenceData(t *testing.T, a *API, query string) string {
	t.Helper()
	raw, err := a.ExecBounded(t.Context(), query, "", nil, config.Defaults().Tools.GraphQL)
	if err != nil {
		t.Fatalf("query %s returned %s: %v", query, raw, err)
	}
	var result Result
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Effects) != 0 {
		t.Fatalf("read evidence mutated state: %s %v", raw, err)
	}
	return string(result.Data)
}

// The command really fails. Message transport is an explicitly authored fixture;
// JSONL, Kuzu, reopen, typed GraphQL projection and failure bytes are real.
func evidenceArchive(t *testing.T, dir, root, id string) string {
	t.Helper()
	evidenceAppend(t, dir, id, "workspace", root)
	evidenceAppend(t, dir, id, "prompt", strings.Repeat("unselected α🙂", 30000))
	command := "printf 'ACTUAL_FAILURE α🙂\\n'; exit 7"
	process := exec.Command("bash", "-c", command)
	process.Dir = root
	output, err := process.CombinedOutput()
	if code, ok := err.(*exec.ExitError); !ok || code.ExitCode() != 7 {
		t.Fatalf("negative command was not real: %q %v", output, err)
	}
	argument, _ := json.Marshal(map[string]string{"cmd": command})
	evidenceNative(t, dir, id, "native_reply", llm.Message{Role: "assistant", Thinking: strings.Repeat("large unselected reasoning", 20000), ToolCalls: []llm.ToolCall{{ID: "failed-call", Name: "bash", Arguments: string(argument)}, {ID: "unexecuted-call", Name: "read", Arguments: `{"file":"after.txt"}`}}})
	evidenceAppend(t, dir, id, "tool", "bash "+string(argument))
	evidenceNative(t, dir, id, "native_result", llm.Message{Role: "tool", Name: "bash", ToolCallID: "failed-call", Content: "exit 7\n" + string(output)})
	evidenceNative(t, dir, id, "native_result", llm.Message{Role: "tool", Name: "read", ToolCallID: "unexecuted-call", Content: "call not executed: preceding call failed-call did not complete successfully"})
	evidenceNative(t, dir, id, "native_reply", llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "denied-call", Name: "write", Arguments: `{"file":"denied.txt","content":"x"}`}}})
	evidenceNative(t, dir, id, "native_result", llm.Message{Role: "tool", Name: "write", ToolCallID: "denied-call", Content: "authorization denied; no file written"})
	zero := 0
	coding := session.CodingState{TaskID: "ordinary", Workspace: root, Revision: 2, Command: "true", Receipt: &session.CheckReceipt{Direct: true, Command: "true", Cwd: root, Revision: 1, Started: "earlier", Ended: "earlier-end", EpisodeStarted: "earlier", EpisodeEnded: "earlier-end", ExitCode: &zero, Status: "stale", Reason: "later file mutation"}}
	raw, _ := json.Marshal(coding)
	evidenceAppend(t, dir, id, "coding", string(raw))
	evidenceAppend(t, dir, id, "done", "model says PASS; this is an authored claim")
	return string(output)
}

// Portable old-code control: this entire test file uses only pre-existing public
// Go APIs. Copying it alone onto the immutable-source clone fails on the absent
// GraphQL field, before any candidate implementation can influence the result.
func TestEvidenceRecallSelectedProjectArchiveReopen(t *testing.T) {
	db, dir, root := filepath.Join(t.TempDir(), "db"), t.TempDir(), t.TempDir()
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	a := evidenceAPI(t, s, dir, root)
	root = a.CapturedProject()
	actual := evidenceArchive(t, dir, root, "prior")
	path := filepath.Join(dir, "prior.jsonl")
	before, _ := os.ReadFile(path)
	s.Close()
	s, err = store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a = evidenceAPI(t, s, dir, root)
	query := `{ sessionEvidence(sessionId:"prior",offset:0,limit:1,callId:"unexecuted-call") { projectRoot sessionId historical nextOffset unsettledOffset groups { reference offset callIds records { offset kind source message { role content toolCallId name toolCalls { id name arguments } } } } } }`
	data := evidenceData(t, a, query)
	for _, want := range []string{`"historical":true`, `"reference":"session:prior:2"`, `"callIds":["failed-call","unexecuted-call"]`, `ACTUAL_FAILURE α🙂`, `"toolCallId":"failed-call"`, `"toolCallId":"unexecuted-call"`, "call not executed:", "model-authored", "recorded tool result"} {
		if !strings.Contains(data, want) {
			t.Fatalf("missing %q in %s", want, data)
		}
	}
	if strings.Contains(data, "thinking") || strings.Contains(data, "unselected") || len(data) > 10000 {
		t.Fatalf("unselected reasoning/history imported: %d", len(data))
	}
	t.Logf("actual failed command output=%q; reopened selected page=%s", actual, data)
	denied := evidenceData(t, a, `{ sessionEvidence(sessionId:"prior",offset:0,limit:1,callId:"denied-call") { historical groups { reference records { message { content toolCallId } } } } }`)
	if !strings.Contains(denied, "authorization denied; no file written") {
		t.Fatal(denied)
	}
	stale := evidenceData(t, a, `{ sessionEvidence(sessionId:"prior",offset:8,limit:2) { historical groups { records { kind source text } } } }`)
	if !strings.Contains(stale, `\"status\":\"stale\"`) || !strings.Contains(stale, "model says PASS") {
		t.Fatal(stale)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("historical recall rewrote archive or renewed a receipt")
	}
}

func TestEvidenceScopeCorruptionAndReferences(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir, root, other := t.TempDir(), t.TempDir(), t.TempDir()
	a := evidenceAPI(t, s, dir, root)
	root = a.CapturedProject()
	evidenceArchive(t, dir, root, "good")
	data := evidenceData(t, a, `{ sessionEvidence(sessionId:"good",offset:0,limit:1) { nextOffset groups { offset reference } } }`)
	if !strings.Contains(data, `"nextOffset":1`) || !strings.Contains(data, `"offset":0`) {
		t.Fatal(data)
	}
	page := evidenceData(t, a, `{ sessionEvidence(sessionId:"good",offset:1,limit:1) { nextOffset groups { offset reference } } }`)
	if !strings.Contains(page, `"nextOffset":2`) || !strings.Contains(page, `"offset":1`) {
		t.Fatal(page)
	}
	for _, tc := range []struct{ id, workspace, suffix string }{{"foreign", other, ""}, {"mixed", root, other}, {"legacy", "", ""}} {
		if tc.workspace != "" {
			evidenceAppend(t, dir, tc.id, "workspace", tc.workspace)
		}
		evidenceAppend(t, dir, tc.id, "prompt", "model mentions "+root+"; prose cannot associate an archive")
		if tc.suffix != "" {
			evidenceAppend(t, dir, tc.id, "workspace", tc.suffix)
		}
	}
	evidenceAppend(t, dir, "corrupt", "workspace", root)
	if err := os.WriteFile(filepath.Join(dir, "corrupt.jsonl"), []byte("{bad json}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`{ sessionEvidence(sessionId:"foreign",offset:0,limit:1) { historical } }`, `{ sessionEvidence(sessionId:"mixed",offset:0,limit:1) { groups { reference } } }`, `{ sessionEvidence(sessionId:"legacy",offset:0,limit:1) { historical } }`, `{ sessionEvidence(sessionId:"corrupt",offset:0,limit:1) { historical } }`, `{ sessionEvidence(sessionId:"../good",offset:0,limit:1) { historical } }`, `{ sessionEvidence(sessionId:"missing",offset:0,limit:1) { historical } }`, `{ sessionEvidence(sessionId:"good",offset:3,limit:1) { historical } }`, `{ sessionEvidence(sessionId:"good",offset:999,limit:1) { historical } }`, `{ sessionEvidence(sessionId:"good",offset:0,limit:1,callId:"ACTUAL_FAILURE") { historical } }`, `{ sessionEvidence(sessionId:"good",offset:-1,limit:1) { historical } }`, `{ sessionEvidence(sessionId:"good",offset:0,limit:0) { historical } }`} {
		if raw, err := a.ExecBounded(t.Context(), q, "", nil, config.Defaults().Tools.GraphQL); err == nil {
			t.Fatalf("invalid/scopeless evidence admitted: %s %s", q, raw)
		}
	}
	for _, mutate := range []string{"mismatch", "duplicate", "orphan"} {
		evidenceAppend(t, dir, mutate, "workspace", root)
		if mutate != "orphan" {
			calls := []llm.ToolCall{{ID: "x", Name: "read", Arguments: `{}`}}
			if mutate == "duplicate" {
				calls = append(calls, calls[0])
			}
			evidenceNative(t, dir, mutate, "native_reply", llm.Message{Role: "assistant", ToolCalls: calls})
		}
		evidenceNative(t, dir, mutate, "native_result", llm.Message{Role: "tool", Name: "read", ToolCallID: "wrong", Content: "PASS"})
		q := fmt.Sprintf(`{ sessionEvidence(sessionId:%q,offset:0,limit:1) { historical } }`, mutate)
		if _, err := a.ExecBounded(t.Context(), q, "", nil, config.Defaults().Tools.GraphQL); err == nil {
			t.Fatalf("corrupt native %s admitted by metadata-only read", mutate)
		}
	}
}

func TestEvidenceActiveAndCrashedTailNeverInventsResults(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir, root := t.TempDir(), t.TempDir()
	a := evidenceAPI(t, s, dir, root)
	root = a.CapturedProject()
	evidenceArchive(t, dir, root, "current")
	evidenceNative(t, dir, "current", "native_reply", llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "in-flight-graphql", Name: "graphql", Arguments: `{"query":"{ sessionEvidence(sessionId:\"current\",offset:0,limit:1) { historical } }"}`}}})
	evidenceAppend(t, dir, "current", "tool", "graphql saved before resolver; result is still in flight")
	path := filepath.Join(dir, "current.jsonl")
	before, _ := os.ReadFile(path)
	q := `{ sessionEvidence(sessionId:"current",offset:0,limit:1,callId:"failed-call") { historical nextOffset unsettledOffset groups { offset records { message { content toolCallId } } } } }`
	data := evidenceData(t, a, q)
	if !strings.Contains(data, `"unsettledOffset":10`) || !strings.Contains(data, `"nextOffset":10`) || !strings.Contains(data, "ACTUAL_FAILURE") || strings.Contains(data, "in-flight-graphql") {
		t.Fatal(data)
	}
	boundary := evidenceData(t, a, `{ sessionEvidence(sessionId:"current",offset:10,limit:1) { nextOffset unsettledOffset groups { reference } } }`)
	if !strings.Contains(boundary, `"groups":[]`) || !strings.Contains(boundary, `"unsettledOffset":10`) {
		t.Fatal(boundary)
	}
	if _, err := a.ExecBounded(t.Context(), `{ sessionEvidence(sessionId:"current",offset:0,limit:1,callId:"in-flight-graphql") { historical } }`, "", nil, config.Defaults().Tools.GraphQL); err == nil || !strings.Contains(err.Error(), "unsettled") {
		t.Fatalf("unfinished call received fake result: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("selected read settled or rewrote an active/crash tail")
	}
	// Reopening the same bytes preserves the unsettled boundary, without healing.
	reopened := evidenceData(t, evidenceAPI(t, s, dir, root), q)
	if reopened != data {
		t.Fatalf("reopen changed evidence: %s\n%s", data, reopened)
	}
	t.Logf("complete earlier group and unsettled tail: %s", data)
}

func TestEvidenceBudgetsNarrowingAliasesAndUse(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir, root := t.TempDir(), t.TempDir()
	a := evidenceAPI(t, s, dir, root)
	root = a.CapturedProject()
	evidenceArchive(t, dir, root, "prior")
	path := filepath.Join(dir, "prior.jsonl")
	old := time.Now().AddDate(0, -4, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	for _, fields := range []string{`records { text }`, `records { message { thinking } }`} {
		q := `{ sessionEvidence(sessionId:"prior",offset:0,limit:1,callId:"failed-call") { groups { ` + fields + ` } } }`
		// This fixture exceeds the explicitly configured historical 64 KiB cap,
		// not the expanded default. Keep proving overrides and failed-read retention.
		limits := config.Defaults().Tools.GraphQL
		limits.MaxResultBytes = 65536
		if raw, err := a.ExecBounded(t.Context(), q, "", nil, limits); err == nil {
			t.Fatalf("oversized selected body admitted: %s", raw)
		}
		st, _ := os.Stat(path)
		if !st.ModTime().Equal(old) {
			t.Fatal("failed selected read refreshed retention")
		}
	}
	q := `{ sessionEvidence(sessionId:"prior",offset:0,limit:1,callId:"failed-call") { groups { a:records { first:message { content } } b:records { second:message { toolCallId toolCalls { a:arguments b:arguments } } } } } }`
	data := evidenceData(t, a, q)
	if !strings.Contains(data, "ACTUAL_FAILURE") || !strings.Contains(data, `"toolCallId":"failed-call"`) {
		t.Fatal(data)
	}
	st, _ := os.Stat(path)
	if !st.ModTime().After(old) {
		t.Fatal("successful explicit read did not refresh retention")
	}
	limits := config.Defaults().Tools.GraphQL
	limits.MaxRows = 8
	if _, err := a.ExecBounded(t.Context(), q, "", nil, limits); err == nil {
		t.Fatal("aliased nested lists did not charge existing row budget")
	}
	// Encoder expansion can fail after source charging; that failed operation must
	// not refresh use time either.
	evidenceAppend(t, dir, "escape", "workspace", root)
	evidenceNative(t, dir, "escape", "native_reply", llm.Message{Role: "assistant", Content: strings.Repeat("\t", config.Defaults().Tools.GraphQL.MaxResultBytes/2+1)})
	escape := filepath.Join(dir, "escape.jsonl")
	if err := os.Chtimes(escape, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ExecBounded(t.Context(), `{ sessionEvidence(sessionId:"escape",offset:1,limit:1) { groups { records { message { content } } } } }`, "", nil, config.Defaults().Tools.GraphQL); err == nil {
		t.Fatal("JSON escaping bypassed final byte budget")
	}
	st, _ = os.Stat(escape)
	if !st.ModTime().Equal(old) {
		t.Fatal("encoded-result failure refreshed use")
	}
}

func TestEvidenceTaskAttemptPagingScopeAndStalePass(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	dir, root := t.TempDir(), t.TempDir()
	a := evidenceAPI(t, s, dir, root)
	ctx := plan.WithProject(t.Context(), a.CapturedProject())
	p, err := plan.CreatePlanContext(ctx, s, "plan", "goal")
	if err != nil {
		t.Fatal(err)
	}
	app, err := plan.CreateApproach(s, p.ID, "approach")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, app.ID, "task", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(t.TempDir(), "historical-worker-worktree")
	if err := os.Mkdir(worker, 0755); err != nil {
		t.Fatal(err)
	}
	evidenceArchive(t, dir, worker, "planned-events")
	events, err := session.Read(dir, "planned-events")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(worker); err != nil {
		t.Fatal(err)
	}
	log, _ := json.Marshal([]any{map[string]any{"Kind": "execution", "Attempt": 1, "Output": strings.Repeat("large α🙂", 30000), "ExecutionEvents": events}, map[string]any{"Kind": "check", "Attempt": 1, "Command": "real old check", "Verification": "passed", "ExitCode": 0, "Output": "OLD_PASS"}, map[string]any{"Kind": "check", "Attempt": 2, "Verification": "failed", "ExitCode": 7, "Output": "ACTUAL_FAIL_CURRENT_ATTEMPT", "CheckEvents": events}, map[string]any{"Kind": "review", "Attempt": 2, "Verification": "failed", "ReviewerEvents": events}})
	if err := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempt_log=$log,t.attempts=2`, map[string]any{"id": task.ID, "log": string(log)}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a = evidenceAPI(t, s, dir, root)
	q := fmt.Sprintf(`{ taskEvidence(taskId:%q,offset:0,limit:1,kind:"check") { taskId historical nextOffset groups { reference offset records { kind source text } } } }`, task.ID)
	// Select only the old small check first; the second full raw check is large.
	first := evidenceData(t, a, q)
	if !strings.Contains(first, `"nextOffset":2`) || !strings.Contains(first, "OLD_PASS") || !strings.Contains(first, `"historical":true`) || strings.Contains(first, "large") {
		t.Fatal(first)
	}
	var second string
	for _, selection := range []struct {
		index int
		phase string
	}{{0, "execution"}, {2, "check"}, {3, "reviewer"}} {
		query := fmt.Sprintf(`{ taskEvidence(taskId:%q,record:%d,phase:%q,offset:0,limit:1,callId:"unexecuted-call") { projectRoot historical groups { reference offset callIds records { kind workspace message { content toolCallId toolCalls { id name arguments } } } } } }`, task.ID, selection.index, selection.phase)
		second = evidenceData(t, a, query)
		want := fmt.Sprintf("task:%s:%d:%s:2", task.ID, selection.index, selection.phase)
		if !strings.Contains(second, "ACTUAL_FAILURE") || !strings.Contains(second, want) || !strings.Contains(second, "unexecuted-call") || !containsJSONValue(second, worker) || !containsJSONValue(second, a.CapturedProject()) || strings.Contains(second, "thinking") || strings.Contains(second, "large") {
			t.Fatalf("large planned events could not narrow to actual calls/results: %s", second)
		}
	}
	for _, args := range []string{`record:1,phase:"execution"`, `record:99,phase:"execution"`, `record:0,phase:"unknown"`, `record:0`, `phase:"execution"`, `record:0,phase:"execution",attempt:2`, `callId:"failed-call"`} {
		query := fmt.Sprintf(`{ taskEvidence(taskId:%q,offset:0,limit:1,%s) { historical } }`, task.ID, args)
		if _, err := a.ExecBounded(t.Context(), query, "", nil, config.Defaults().Tools.GraphQL); err == nil {
			t.Fatalf("unknown/missing planned event source admitted: %s", args)
		}
	}
	current, err := plan.GetTask(s, task.ID)
	if err != nil || current.Checked() || current.Attempts != 2 || current.AttemptLog != string(log) {
		t.Fatalf("recall renewed old authority or changed bytes: %+v %v", current, err)
	}
	for _, api := range []*API{evidenceAPI(t, s, dir, t.TempDir()), New(s, "unscoped", dir)} {
		if _, err := api.ExecBounded(t.Context(), q, "", nil, config.Defaults().Tools.GraphQL); err == nil {
			t.Fatal("foreign/unscoped task evidence admitted")
		}
	}
	if err := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempt_log='[{"Kind":"check"},broken]'`, map[string]any{"id": task.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ExecBounded(context.Background(), fmt.Sprintf(`{ taskEvidence(taskId:%q,offset:0,limit:1) { historical } }`, task.ID), "", nil, config.Defaults().Tools.GraphQL); err == nil {
		t.Fatal("metadata-only task read ignored corrupt later source")
	}
	t.Logf("task selected old PASS remains historical: %s; selected large reviewer native failure: %s", first, second)
}

func TestReviewerTranscriptEvidenceIsBoundAndHistorical(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root, dir := t.TempDir(), t.TempDir()
	base := evidenceAPI(t, s, dir, root)
	p, err := plan.CreatePlanContext(plan.WithProject(t.Context(), base.CapturedProject()), s, "plan", "goal")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "approach")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "task", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	messages := []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "bash", Arguments: `{"cmd":"probe"}`}}}, {Role: "tool", ToolCallID: "c1", Name: "bash", Content: "tool result (truncated):\nobserved failure"}, {Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "pending", Name: "bash", Arguments: `{}`}}}}
	log, _ := json.Marshal([]any{map[string]any{"Kind": "execution", "Attempt": 1, "Transcript": messages}})
	if err := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempt_log=$log,t.attempts=1`, map[string]any{"id": task.ID, "log": string(log)}); err != nil {
		t.Fatal(err)
	}
	api, err := New(s, "test", dir).WithAllowedFields("taskEvidence").WithReviewerEvidence(task.ID, 1).Scoped(root, task.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	query := func(id, args string) string {
		return fmt.Sprintf(`{taskEvidence(taskId:%q,%s){historical unsettledOffset groups{callIds records{time source workspace message{content toolCallId toolCalls{id arguments}}}}}}`, id, args)
	}
	got := evidenceData(t, api, query(task.ID, `attempt:1,record:0,phase:"execution",offset:0,limit:1,callId:"c1"`))
	if !strings.Contains(got, "observed failure") || !strings.Contains(got, "truncated") || !strings.Contains(got, `"historical":true`) || !strings.Contains(got, `"unsettledOffset":2`) {
		t.Fatal(got)
	}
	for _, args := range []string{`attempt:2,offset:0,limit:1`, `offset:0,limit:1`, `attempt:1,record:0,phase:"execution",offset:0,limit:1,callId:"pending"`} {
		if _, err := api.ExecBounded(t.Context(), query(task.ID, args), "", nil, config.Defaults().Tools.GraphQL); err == nil {
			t.Fatalf("admitted %s", args)
		}
	}
	if _, err := api.ExecBounded(t.Context(), query("other", `attempt:1,offset:0,limit:1`), "", nil, config.Defaults().Tools.GraphQL); err == nil {
		t.Fatal("foreign task admitted")
	}
	current, _ := plan.GetTask(s, task.ID)
	if current.Checked() || current.Qualified() || current.AttemptLog != string(log) {
		t.Fatal("evidence changed owner")
	}
	if err := s.Write(`MATCH(t:Task) WHERE t.id=$id SET t.attempts=2`, map[string]any{"id": task.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.ExecBounded(t.Context(), query(task.ID, `attempt:1,offset:0,limit:1`), "", nil, config.Defaults().Tools.GraphQL); err == nil {
		t.Fatal("stale attempt admitted")
	}
}

func TestTranscriptEvidenceRejectsUnavailableAndAmbiguousCalls(t *testing.T) {
	for _, messages := range [][]llm.Message{
		{{Role: "assistant", Content: "tool: bash"}, {Role: "user", Content: "tool result: text mode"}},
		{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "same", Name: "bash"}, {ID: "same", Name: "bash"}}}},
	} {
		if _, _, err := visitTranscript(t.Context(), messages, func(session.EvidenceGroup) error { return nil }); err == nil {
			t.Fatal("unavailable or ambiguous transcript accepted")
		}
	}
}

func containsJSONValue(raw, value string) bool {
	encoded, _ := json.Marshal(value)
	return strings.Contains(raw, string(encoded))
}
