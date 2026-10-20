package launch

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

// The shipped D45 options are shared with other component fixtures in this package.
var servesGoal = prompts.Builtin().Text("decision.readiness.ready")
var offGoal = prompts.Builtin().Text("decision.readiness.revise")

var errTestDown = errors.New("sidecar down")

func stubVerdict(v decision.Verdict, err error) func(context.Context, decision.Request) (decision.Verdict, error) {
	return func(context.Context, decision.Request) (decision.Verdict, error) { return v, err }
}

func gateDeps(t *testing.T, n int) (*store.Store, string, Deps) {
	t.Helper()
	s, aid, _ := setup(t, n)
	d := depsFor(s, nil)
	d.Threshold = 0.7
	d.Out = &bytes.Buffer{}
	return s, aid, d
}

func TestReadyCheckCoherent(t *testing.T) {
	_, aid, d := gateDeps(t, 2)
	d.Judge = stubVerdict(decision.Verdict{Option: servesGoal, Confidence: 0.9}, nil)
	d.In = strings.NewReader("")
	if err := ReadyCheck(context.Background(), d, aid); err != nil {
		t.Fatalf("coherent: %s", err)
	}
}

func TestReadyCheckRejectsIncoherent(t *testing.T) {
	s, aid, d := gateDeps(t, 2)
	d.Judge = stubVerdict(decision.Verdict{Option: offGoal, Confidence: 0.7}, nil)
	d.In = strings.NewReader("")
	if err := ReadyCheck(context.Background(), d, aid); err == nil || !strings.Contains(err.Error(), "incoherent") {
		t.Fatalf("err=%v", err)
	}
	a, err := plan.GetApproach(s, aid)
	if err != nil {
		t.Fatal(err)
	}
	if a.State != plan.ApproachReady {
		t.Errorf("reject must leave the approach ready, state=%s", a.State)
	}
}

func TestReadyCheckUnsureAsks(t *testing.T) {
	for _, tc := range []struct {
		in      string
		wantErr bool
	}{
		{"y\n", false},
		{"n\n", true},
	} {
		_, aid, d := gateDeps(t, 2)
		d.Judge = stubVerdict(decision.Verdict{Option: servesGoal, Confidence: 0.5}, nil)
		d.In = strings.NewReader(tc.in)
		out := &bytes.Buffer{}
		d.Out = out
		err := ReadyCheck(context.Background(), d, aid)
		if tc.wantErr && (err == nil || !strings.Contains(err.Error(), "unsure")) {
			t.Errorf("in=%q err=%v", tc.in, err)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("in=%q err=%v", tc.in, err)
		}
		if !strings.Contains(out.String(), "leans "+servesGoal) {
			t.Errorf("in=%q prompt=%q", tc.in, out.String())
		}
	}
}

func TestReadyCheckUnsureWithoutPromptRefuses(t *testing.T) {
	_, aid, d := gateDeps(t, 2)
	d.Judge = stubVerdict(decision.Verdict{Option: servesGoal, Confidence: 0.5}, nil)
	d.In, d.Out = nil, nil
	if err := ReadyCheck(context.Background(), d, aid); err == nil {
		t.Fatal("unsure with no prompt must refuse")
	}
}

func TestReadyCheckNoJudgeRefuses(t *testing.T) {
	_, aid, d := gateDeps(t, 2)
	d.Judge = nil
	if err := ReadyCheck(context.Background(), d, aid); err == nil || !strings.Contains(err.Error(), "no decision judge") {
		t.Fatalf("err=%v", err)
	}
}

func TestReadyCheckJudgeErrorRefuses(t *testing.T) {
	_, aid, d := gateDeps(t, 2)
	d.Judge = stubVerdict(decision.Verdict{}, errTestDown)
	if err := ReadyCheck(context.Background(), d, aid); err == nil || !strings.Contains(err.Error(), "sidecar") {
		t.Fatalf("err=%v", err)
	}
}

func TestReadyCheckDraftRefuses(t *testing.T) {
	s, aid, d := gateDeps(t, 2)
	p, err := plan.ApproachPlan(s, aid)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := plan.CreateApproach(s, p.ID, "draft")
	if err != nil {
		t.Fatal(err)
	}
	d.Judge = stubVerdict(decision.Verdict{Option: servesGoal, Confidence: 1}, nil)
	if err := ReadyCheck(context.Background(), d, draft.ID); err == nil || !strings.Contains(err.Error(), "launch needs ready") {
		t.Fatalf("err=%v", err)
	}
}

func TestReadyCheckQuestionShape(t *testing.T) {
	_, aid, d := gateDeps(t, 2)
	var got decision.Request
	d.Judge = func(ctx context.Context, r decision.Request) (decision.Verdict, error) {
		got = r
		return decision.Verdict{Option: servesGoal, Confidence: 1}, nil
	}
	if err := ReadyCheck(context.Background(), d, aid); err != nil {
		t.Fatal(err)
	}
	if len(got.Options) != 2 || got.Options[0] != servesGoal || got.Options[1] != offGoal {
		t.Errorf("options=%v", got.Options)
	}
	for _, want := range []string{"Plan goal: g", "t0", "prompt-t0"} {
		if !strings.Contains(got.Context, want) {
			t.Errorf("context missing %q: %q", want, got.Context)
		}
	}
	if !strings.Contains(got.Instructions, "Pick the true statement") {
		t.Errorf("instructions=%q", got.Instructions)
	}
}

func TestCoherenceContextKeepsCompleteBrief(t *testing.T) {
	p := &plan.Plan{Goal: "g"}
	tasks := []*plan.Task{{Title: "t", Prompt: strings.Repeat("p", 4000), Verify: "late verification"}}
	got := coherenceContext(p, tasks)
	if !strings.Contains(got, tasks[0].Prompt) || !strings.Contains(got, "late verification") {
		t.Error("complete readiness facts lost")
	}
}

func TestExecuteRefusesIncoherent(t *testing.T) {
	s, aid, d := gateDeps(t, 2)
	d.Judge = stubVerdict(decision.Verdict{Option: offGoal, Confidence: 0.9}, nil)
	d.In = strings.NewReader("")
	if _, err := New(d).Execute(context.Background(), tool.Args{"approach": aid}); err == nil || !strings.Contains(err.Error(), "incoherent") {
		t.Fatalf("err=%v", err)
	}
	a, err := plan.GetApproach(s, aid)
	if err != nil {
		t.Fatal(err)
	}
	if a.State != plan.ApproachReady {
		t.Errorf("refused launch must leave the approach ready, state=%s", a.State)
	}
}

func TestReadyCheckLogsJudgmentWithResolution(t *testing.T) {
	cases := []struct {
		name       string
		verdict    decision.Verdict
		answer     string
		resolution string
		wantErr    bool
	}{
		{"admitted", decision.Verdict{Option: servesGoal, Confidence: 0.9}, "", "admitted", false},
		{"refused", decision.Verdict{Option: offGoal, Confidence: 0.9}, "", "refused", true},
		{"asked-admitted", decision.Verdict{Option: servesGoal, Confidence: 0.5}, "y\n", "asked-admitted", false},
		{"asked-refused", decision.Verdict{Option: offGoal, Confidence: 0.5}, "n\n", "asked-refused", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, aid, d := gateDeps(t, 2)
			d.Judge = stubVerdict(tc.verdict, nil)
			d.In = strings.NewReader(tc.answer)
			var kinds, texts []string
			d.Log = func(kind, text string) { kinds, texts = append(kinds, kind), append(texts, text) }
			err := ReadyCheck(context.Background(), d, aid)
			if tc.wantErr && err == nil {
				t.Fatal("expected refusal, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if len(kinds) != 1 || kinds[0] != decision.KindJudgment {
				t.Fatalf("kinds=%v", kinds)
			}
			j, ok := decision.ParseJudgment(texts[0])
			if !ok {
				t.Fatalf("unparseable event: %q", texts[0])
			}
			if j.Source != decision.SourceCoherence || j.Pick != tc.verdict.Option || j.Resolution != tc.resolution {
				t.Errorf("parsed=%+v", j)
			}
		})
	}
}

func TestReadyUncertaintyShowsExactJudgedContext(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	p, err := plan.CreatePlan(s, "p", "g")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	x, err := plan.CreateTask(s, a.ID, "context task", "complete task")
	if err != nil {
		t.Fatal(err)
	}
	aid, d := a.ID, depsFor(s, nil)
	d.Threshold = .7
	if _, err := plan.SetTaskBlock(s, x.ID, "current\x1b[31m\x7f\u0085 雪\nsecond row", "target", "sketch", "acceptance 0", "tests", "verify", "true", "printf FIXTURE_ACCEPTANCE"); err != nil {
		t.Fatal(err)
	}
	var judged decision.Request
	var prompt string
	judges, asks := 0, 0
	d.Judge = func(_ context.Context, r decision.Request) (decision.Verdict, error) {
		judges++
		judged = r
		return decision.Verdict{Option: servesGoal, Confidence: .622}, nil
	}
	d.Ask = func(_ context.Context, p string) bool { asks++; prompt = p; return false }
	if _, err := Ready(context.Background(), d, aid); err == nil {
		t.Fatal("decline admitted")
	}
	if judges != 1 || asks != 1 {
		t.Fatalf("judge/ask=%d/%d", judges, asks)
	}
	_, rows, ok := strings.Cut(prompt, "Judged context (quoted rows; escapes are literal characters):\n")
	if !ok {
		t.Fatalf("judged context absent: %q", prompt)
	}
	var decoded []string
	for _, row := range strings.Split(rows, "\n") {
		value, err := strconv.Unquote(row)
		if err != nil {
			t.Fatal(err)
		}
		decoded = append(decoded, value)
	}
	if strings.Join(decoded, "\n") != judged.Context {
		t.Fatalf("not exact judged context: %q", prompt)
	}
	for _, field := range []string{"Plan goal: g", x.Title, x.Prompt, "current: current\x1b[31m\x7f\u0085 雪\nsecond row", "target: target", "sketch: sketch", "acceptance: acceptance 0", "tests: tests", "verify: verify", "check_command: true"} {
		if !strings.Contains(judged.Context, field) {
			t.Fatalf("missing %q", field)
		}
	}
	for _, unsafe := range []rune{0x1b, 0x7f, 0x85} {
		if strings.ContainsRune(prompt, unsafe) {
			t.Fatalf("unsafe display %U", unsafe)
		}
	}
	after, err := plan.GetTask(s, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != plan.TaskQueued || after.Attempts != 0 {
		t.Fatalf("decline changed queued task: %+v", after)
	}
	d.CoherenceMaxChars = 1
	judges, asks = 0, 0
	if _, err := Ready(context.Background(), d, aid); err == nil || !strings.Contains(err.Error(), "coherence_max_chars") {
		t.Fatalf("cap refusal=%v", err)
	}
	if judges != 0 || asks != 0 {
		t.Fatalf("overcap judge/ask=%d/%d", judges, asks)
	}
}

func TestReadinessFailureUsesSelectedServiceRecovery(t *testing.T) {
	_, aid, d := gateDeps(t, 1)
	cause := errors.New("http://127.0.0.1:18103/judge: connection refused")
	d.Judge = stubVerdict(decision.Verdict{}, cause)
	err := ReadyCheck(context.Background(), d, aid)
	if err == nil {
		t.Fatal("outage admitted readiness")
	}
	for _, want := range []string{"18103", "/manage", "external"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	for _, bad := range []string{"8090", "python/decision/server.py"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("guessed deployment in %v", err)
		}
	}
}
