package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"specific-harness/internal/agent"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"strings"
	"testing"
	"time"
)

func TestConsistencyWorkerDeliveryReachesIndependentReview(t *testing.T) {
	s, aid, ids := setup(t, 1)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, _ = filepath.EvalSymlinks(root)
	a, _ := plan.GetApproach(s, aid)
	p, _ := plan.ApproachPlan(s, a.ID)
	if _, err = plan.BindProject(context.Background(), s, p.ID, root); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionPlan(s, p.ID, plan.PlanActive); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionApproach(s, aid, plan.ApproachRunning); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.TransitionTask(s, ids[0], plan.TaskRunning); err != nil {
		t.Fatal(err)
	}
	if err = plan.SetTaskAttempts(s, ids[0], 1); err != nil {
		t.Fatal(err)
	}
	task, _ := plan.GetTask(s, ids[0])
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	next := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct{ Messages []llm.Message }
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		delta, finish := map[string]any{"content": "done"}, "stop"
		if calls == 0 {
			close(entered)
			select {
			case <-release:
			case <-req.Context().Done():
				return
			}
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "worker-bash", "type": "function", "function": map[string]string{"name": "bash", "arguments": `{"cmd":"true"}`}}}}
			finish = "tool_calls"
		} else {
			for _, m := range body.Messages {
				next += m.Content + "\n"
			}
		}
		calls++
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	defer srv.Close()
	d := depsFor(s, nil)
	d.Workspace = root
	d.NewModel = func() agent.Model {
		return llm.New(srv.URL, "fixture").WithRuntime("openai").WithToolProtocol("structured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finished := make(chan Result, 1)
	go func() { finished <- RunTask(ctx, d, task, 32768) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker never entered actual HTTP")
	}
	comment, err := plan.AppendTaskComment(plan.WithProject(ctx, root), s, task.ID, 1, "user", "actual-user-session", "preserve β🙂 advice")
	if err != nil {
		t.Fatal(err)
	}
	current, _ := plan.GetTask(s, task.ID)
	current.AttemptLog = strings.Replace(current.AttemptLog, `"Kind":"comment"`, `"future": [true],"Kind":"comment"`, 1)
	if err = plan.SetTaskOutcome(s, current); err != nil {
		t.Fatal(err)
	}
	close(release)
	result := <-finished
	srv.Close()
	if result.Err != nil || calls != 2 || strings.Count(next, comment.Text) != 1 {
		t.Fatalf("worker delivery calls%d next=%s err%v", calls, next, result.Err)
	}
	actual, _ := plan.GetTask(s, task.ID)
	if !strings.Contains(actual.AttemptLog, `"Kind":"comment_delivery"`) || !strings.Contains(actual.AttemptLog, `"future": [true]`) {
		t.Fatal(actual.AttemptLog)
	}
	// Actual reviewer event append, not a fabricated verdict/check.
	result.AttemptLog = actual.AttemptLog
	result.Attempts = 1
	result.State = plan.TaskRunning
	if err = (reviewSink{d: d, r: &result}).Append(ctx, task.ID, "reply", "UNVERIFIED fixture event"); err != nil {
		t.Fatal(err)
	}
	actual, _ = plan.GetTask(s, task.ID)
	if !strings.Contains(actual.AttemptLog, `"future": [true]`) || !strings.Contains(actual.AttemptLog, comment.ID) || actual.Checked() {
		t.Fatalf("review lost raw comment or minted check %s", actual.AttemptLog)
	}
	comments, err := outcome.TaskComments(actual.AttemptLog, 1)
	if err != nil || len(comments) != 1 {
		t.Fatalf("comment duplicate/loss %+v %v", comments, err)
	}
	// A second guidance-only comment arrives after the reviewer starts. The
	// existing callback must deliver it on the next request, without revising
	// the frozen brief or manufacturing a verification result.
	var later *outcome.TaskComment
	reviewer := &consistencyReviewCommentCapture{onFirst: func(ctx context.Context) error {
		var err error
		later, err = plan.AppendTaskComment(plan.WithProject(ctx, root), s, task.ID, 1, "user", "late-user-session", "later review guidance γ🙂")
		return err
	}}
	d.NewModel = func() agent.Model { return reviewer }
	results := []Result{result}
	if err = verifyPass(ctx, d, results, []int{0}, 32768); err != nil {
		t.Fatalf("review admission control failed: %v", err)
	}
	if len(reviewer.wires) != 2 {
		t.Fatalf("actual reviewer turn count %d, want two", len(reviewer.wires))
	}
	if strings.Count(reviewer.wires[0], comment.Text) != 1 || !strings.Contains(reviewer.wires[0], comment.ID) {
		t.Error("first reviewer request omitted the actual delivered worker comment or its ID")
	}
	if strings.Count(reviewer.wires[1], comment.Text) != 1 || strings.Count(reviewer.wires[1], later.Text) != 1 || !strings.Contains(reviewer.wires[1], later.ID) || !strings.Contains(reviewer.wires[1], `"Source":"user"`) {
		t.Error("next reviewer request did not retain both actual comments and their provenance exactly once")
	}
	actual, err = plan.GetTask(s, task.ID)
	if err != nil || !actual.SameBrief(task) || actual.Qualified() || results[0].Verification == outcome.Passed {
		t.Fatalf("comment guidance changed the brief or qualified work: %+v %v", actual, err)
	}
	var records []struct{ Kind, CommentID string }
	if err = json.Unmarshal([]byte(actual.AttemptLog), &records); err != nil {
		t.Fatal(err)
	}
	deliveries := 0
	for _, record := range records {
		if record.Kind == "comment_delivery" && record.CommentID == later.ID {
			deliveries++
		}
	}
	if deliveries != 1 {
		t.Fatalf("later guidance has %d owning delivery receipts, want one", deliveries)
	}
}

type consistencyReviewCommentCapture struct {
	wires   []string
	onFirst func(context.Context) error
}

func (m *consistencyReviewCommentCapture) Chat(ctx context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	var wire strings.Builder
	for _, msg := range msgs {
		wire.WriteString(msg.Content)
		wire.WriteByte('\n')
	}
	m.wires = append(m.wires, wire.String())
	if len(m.wires) == 1 && m.onFirst != nil {
		if err := m.onFirst(ctx); err != nil {
			return "", "fixture", llm.Usage{}, err
		}
		return "```tool\ntool: bash\ncmd: true\n```", "fixture", llm.Usage{}, nil
	}
	return "UNVERIFIED component probe", "fixture", llm.Usage{}, nil
}
