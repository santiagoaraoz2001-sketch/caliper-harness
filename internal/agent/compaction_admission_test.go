package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

// Decision/model outputs here are controlled components, never native capability proof.
func acceptCompaction(_ context.Context, q decision.Request) (decision.Verdict, error) {
	return decision.Verdict{Option: q.Options[0], Confidence: 1}, nil
}
func passedCompactionFixture() session.Continuation {
	zero := 0
	c := session.Continuation{Version: 1, ModelKey: "old-model", Revision: 7, Measured: true, Prompt: 900}
	c.Add("intent", "user", "Repair converter; verify actual behavior.", false)
	c.Add("task", "user", "Keep the failed exit and heard note; continue the current repair.", false)
	c.Add("note", "user", "By the way: preserve κ\r\n", false)
	c.Add("evidence", "user", "Host configured check: python3 verify.py\nFIXTURE_CHECK_PASS\n[exit code 0]", false)
	c.Add("evidence", "user", "Assigned task settlement: task=task-α attempt=1 qualified=true (owner current-check and independent-review contract).", false)
	c.Add("failure", "user", "./fail.sh [exit code 7]", false)
	c.Add("guidance", "user", "Old unexecuted advice: run python3 verify.py next.", false)
	c.Entries = append(c.Entries, session.Entry{Kind: "reply", Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "receipt-α", Name: "read", Arguments: `{"path":"converter.py"}`}}}}, session.Entry{Kind: "output", Message: llm.Message{Role: "tool", Name: "read", ToolCallID: "receipt-α", Content: "SOURCE_OMITTED_FROM_JUDGE_RAW_BODY"}})
	c.Entries[7].Message.Reasoning = strings.Repeat("Unverified historical source analysis. ", 80)
	c.Coding = &session.CodingState{TaskID: "task-α", Workspace: "/owned", Command: "python3 verify.py", Revision: 2, Receipt: &session.CheckReceipt{Command: "python3 verify.py", Cwd: "/owned", Revision: 2, Started: "start", Ended: "end", Status: "passed", ExitCode: &zero}}
	c.Assignment = &session.Assignment{Store: "/owned/store", Workspace: "/owned", TaskID: "task-α", Attempt: 1}
	c.Workflow = &session.WorkflowScope{Kind: "task", ID: "task-α", Store: "/owned/store"}
	return c
}
func TestFactualCompactionContradiction(t *testing.T) {
	const bad = "Current: python3 verify.py was never executed; no successful verification recorded. Next: run it."
	original := passedCompactionFixture()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []llm.Message
			Tools    []llm.ToolDefinition
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Messages) != 2 || len(body.Tools) != 0 {
			t.Error("summary request lost roles/no-tools contract")
		}
		content, _ := json.Marshal(bad)
		fmt.Fprintf(w, "data: {\"model\":\"fixture\",\"choices\":[{\"delta\":{\"content\":%s,\"reasoning\":\"original κ\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":19,\"completion_tokens\":7}}\n\ndata: [DONE]\n\n", content)
	}))
	defer srv.Close()
	r := truthRunner(t, &scriptModel{}, 32768)
	r.Model = llm.New(srv.URL, "fixture")
	r.ModelKey = "new-model"
	r.Threshold = .7
	judged := false
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		judged = true
		var input struct {
			Summary       string
			Anchors       []session.Entry
			Coding        *session.CodingState
			Assignment    *session.Assignment
			Workflow      *session.WorkflowScope
			SourceSHA256  string
			SourceCallIDs []string
			SessionID     string
		}
		if err := json.Unmarshal([]byte(q.Context), &input); err != nil {
			t.Fatal(err)
		}
		if input.Summary != bad || !reflect.DeepEqual(input.Coding, original.Coding) || !reflect.DeepEqual(input.Assignment, original.Assignment) || !reflect.DeepEqual(input.Workflow, original.Workflow) || input.SessionID != r.SessionID || len(input.SourceSHA256) != 64 || !reflect.DeepEqual(input.SourceCallIDs, []string{"receipt-α"}) {
			t.Fatalf("inexact task/receipt/source anchors: %+v", input)
		}
		if strings.Contains(q.Context, "SOURCE_OMITTED_FROM_JUDGE_RAW_BODY") {
			t.Error("old transcript was dumped into factual judge")
		}
		if !reflect.DeepEqual(input.Anchors, original.Entries[:7]) {
			t.Error("protected facts/guidance changed or omitted")
		}
		return decision.Verdict{Option: q.Options[1], Confidence: .95}, nil
	}
	next, _, ran, err := r.Compact(context.Background(), original)
	if err == nil || ran || !judged || !reflect.DeepEqual(next, original) {
		t.Fatalf("contradiction replaced context: judged=%t ran=%t err=%v", judged, ran, err)
	}
	sink := r.Session.(*recordSink)
	raw := ""
	for i, k := range sink.kinds {
		if k == "reasoning" && strings.HasPrefix(sink.texts[i], "offset=") {
			_, part, _ := strings.Cut(sink.texts[i], "\n")
			raw += part
		}
	}
	var archived llm.Reply
	if err := json.Unmarshal([]byte(raw), &archived); err != nil || archived.Message.Content != bad || archived.Message.Reasoning != "original κ" || archived.Usage.In != 19 || archived.Usage.Out != 7 || archived.FinishReason != "stop" {
		t.Fatalf("original summary fields lost: %+v %v", archived, err)
	}
	for _, k := range sink.kinds {
		if k == "compact" || k == "continuation" || k == "native_reply" {
			t.Error("refused summary committed/replayed")
		}
	}
}

func TestFactualCompactionAdmissionAndFailures(t *testing.T) {
	original := passedCompactionFixture()
	for _, scenario := range []string{"accept", "reject", "uncertain", "unknown", "nil", "service", "overflow", "canceled", "record"} {
		t.Run(scenario, func(t *testing.T) {
			m := &scriptModel{replies: []string{"Current: recorded check passed; independent review qualified the task. Failed: fail.sh exited 7; retain the note."}}
			r := truthRunner(t, m, 32768)
			r.Threshold = .7
			r.ModelKey = "new-model"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r.Judge = func(actual context.Context, q decision.Request) (decision.Verdict, error) {
				if actual != ctx {
					t.Error("judge did not share owning deadline/cancellation")
				}
				switch scenario {
				case "service":
					return decision.Verdict{}, errors.New("selected judge unavailable")
				case "overflow":
					return decision.Verdict{}, errors.New("full input exceeds selected 32k context")
				case "canceled":
					cancel()
					return decision.Verdict{Option: q.Options[0], Confidence: 1}, nil
				case "unknown":
					return decision.Verdict{Option: "invented answer", Confidence: 1}, nil
				case "reject":
					return decision.Verdict{Option: q.Options[1], Confidence: 1}, nil
				case "uncertain":
					return decision.Verdict{Option: q.Options[0], Confidence: .699}, nil
				}
				return acceptCompaction(actual, q)
			}
			if scenario == "nil" {
				r.Judge = nil
			}
			if scenario == "record" {
				r.Session = &recordSink{err: errors.New("original recording cause")}
			}
			next, summary, ran, err := r.Compact(ctx, original)
			if scenario != "accept" {
				if err == nil || ran || !reflect.DeepEqual(next, original) {
					t.Fatalf("failed admission changed context: ran%t err%v", ran, err)
				}
				return
			}
			if err != nil || !ran || summary == "" || next.Compactions != original.Compactions+1 || next.Measured {
				t.Fatalf("valid summary refused: %v", err)
			}
			for _, e := range original.Entries[:7] {
				found := false
				for _, n := range next.Entries {
					found = found || reflect.DeepEqual(e, n)
				}
				if !found {
					t.Fatalf("protected entry changed: %+v", e)
				}
			}
			if !reflect.DeepEqual(next.Coding, original.Coding) || !reflect.DeepEqual(next.Assignment, original.Assignment) {
				t.Error("judge changed owning receipt/authority")
			}
		})
	}
}

type compactionJudgmentFailure struct{ recordSink }

func (s *compactionJudgmentFailure) Append(ctx context.Context, id, kind, text string) error {
	if kind == decision.KindJudgment {
		return errors.New("ACTUAL_JUDGMENT_RECORD_FAILURE")
	}
	return s.recordSink.Append(ctx, id, kind, text)
}
func TestFactualCompactionCompleteDecisionInputAndRecording(t *testing.T) {
	original := passedCompactionFixture()
	original.Add("note", "user", strings.Repeat("full selected Unicode note κ ", 2000)+"LAST_ANCHOR_SENTINEL", false)
	seen := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Context      string
			Instructions string
			Options      []string
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		var selected struct {
			Anchors []session.Entry
			Summary string
		}
		if err := json.Unmarshal([]byte(body.Context), &selected); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(selected.Anchors[len(selected.Anchors)-1], original.Entries[len(original.Entries)-1]) || !strings.Contains(body.Context, "LAST_ANCHOR_SENTINEL") {
			t.Error("decision input silently clipped")
		}
		seen = true
		// Controlled full-input sidecar refusal; this is not a native tokenizer claim.
		http.Error(w, "full request exceeds selected context; no verdict", http.StatusBadRequest)
	}))
	defer srv.Close()
	r := truthRunner(t, &scriptModel{replies: []string{"Recorded check passed; preserve failure and note."}}, 131072)
	r.JudgmentContextChars = 1
	r.Judge = decision.New(srv.URL, "fixture", decision.DeviceGPU, 0).Judge
	next, _, ran, err := r.Compact(context.Background(), original)
	if err == nil || ran || !seen || !reflect.DeepEqual(next, original) {
		t.Fatalf("sidecar full-input refusal changed context: ran%t seen%t err%v", ran, seen, err)
	}
	small := passedCompactionFixture()
	r = truthRunner(t, &scriptModel{replies: []string{"Actual check passed; fail.sh failed and note retained."}}, 32768)
	sink := &compactionJudgmentFailure{}
	r.Session = sink
	r.Judge = acceptCompaction
	next, _, ran, err = r.Compact(context.Background(), small)
	if err == nil || !strings.Contains(err.Error(), "ACTUAL_JUDGMENT_RECORD_FAILURE") || ran || !reflect.DeepEqual(next, small) {
		t.Fatalf("judgment write failure applied: ran%t err%v", ran, err)
	}
	if len(sink.kinds) < 2 || sink.kinds[0] != "reasoning" {
		t.Error("original proposal not archived before judgment failure")
	}
}
