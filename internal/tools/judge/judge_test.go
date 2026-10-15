package judge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/tool"
)

var errTestDown = errors.New("sidecar down")

func stubJudge(v decision.Verdict, err error) func(context.Context, decision.Request) (decision.Verdict, error) {
	return func(context.Context, decision.Request) (decision.Verdict, error) { return v, err }
}

func TestRoundTripLogsEvent(t *testing.T) {
	var kinds, texts []string
	d := New(Deps{
		Judge: stubJudge(decision.Verdict{Option: "deny", Confidence: 0.61}, nil),
		Log:   func(kind, text string) { kinds, texts = append(kinds, kind), append(texts, text) },
	})
	args, q := tool.Validate(d, map[string]string{"context": "rm -rf /", "options": "allow, deny"})
	if q != "" {
		t.Fatalf("repair: %s", q)
	}
	res, err := d.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Text, "deny") || !strings.Contains(res.Text, "0.61") {
		t.Errorf("text=%q", res.Text)
	}
	if len(kinds) != 1 || kinds[0] != decision.KindJudgment {
		t.Fatalf("kinds=%v", kinds)
	}
	j, ok := decision.ParseJudgment(texts[0])
	if !ok {
		t.Fatalf("unparseable event: %q", texts[0])
	}
	if j.Source != decision.SourceJudgeTool || j.Pick != "deny" || j.Resolution != "returned" {
		t.Errorf("parsed=%+v", j)
	}
}

func TestOptionsNeedTwoChoices(t *testing.T) {
	d := New(Deps{Judge: stubJudge(decision.Verdict{}, nil)})
	for _, opts := range []string{"only", " , ", ""} {
		args, q := tool.Validate(d, map[string]string{"context": "x", "options": opts})
		if q != "" {
			continue // missing/blank slot: repair path, not execute
		}
		if _, err := d.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "at least 2") {
			t.Errorf("options %q: err=%v", opts, err)
		}
	}
}

func TestBlankOptionsSplitAndTrim(t *testing.T) {
	var got decision.Request
	d := New(Deps{Judge: func(ctx context.Context, r decision.Request) (decision.Verdict, error) {
		got = r
		return decision.Verdict{Option: "a", Confidence: 1}, nil
	}})
	args, q := tool.Validate(d, map[string]string{"context": "x", "options": " a ,, b "})
	if q != "" {
		t.Fatalf("repair: %s", q)
	}
	if _, err := d.Execute(context.Background(), args); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.Options) != 2 || got.Options[0] != "a" || got.Options[1] != "b" {
		t.Errorf("options=%v", got.Options)
	}
}

func TestUnwiredJudgeRefuses(t *testing.T) {
	d := New(Deps{})
	args, q := tool.Validate(d, map[string]string{"context": "x", "options": "a, b"})
	if q != "" {
		t.Fatalf("repair: %s", q)
	}
	if _, err := d.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "not wired") {
		t.Errorf("err=%v", err)
	}
}

func TestJudgeErrorLogsNothing(t *testing.T) {
	logged := 0
	d := New(Deps{
		Judge: stubJudge(decision.Verdict{}, errTestDown),
		Log:   func(string, string) { logged++ },
	})
	args, q := tool.Validate(d, map[string]string{"context": "x", "options": "a, b"})
	if q != "" {
		t.Fatalf("repair: %s", q)
	}
	if _, err := d.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "sidecar") {
		t.Fatalf("err=%v", err)
	}
	if logged != 0 {
		t.Errorf("logged=%d, want 0 (no verdict, no event)", logged)
	}
}

func TestContextCapHonored(t *testing.T) {
	var texts []string
	d := New(Deps{
		Judge:           stubJudge(decision.Verdict{Option: "a", Confidence: 1}, nil),
		Log:             func(_, text string) { texts = append(texts, text) },
		MaxContextChars: 3,
	})
	args, q := tool.Validate(d, map[string]string{"context": "abcdef", "options": "a, b"})
	if q != "" {
		t.Fatalf("repair: %s", q)
	}
	if _, err := d.Execute(context.Background(), args); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasSuffix(texts[0], "context=abc…") {
		t.Errorf("text=%q", texts[0])
	}
}
