package ask

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/tool"
)

func TestAskPairedContractAndReceipt(t *testing.T) {
	called := 0
	def := New(&Prompter{Ask: func(_ context.Context, questions []Question) ([]Answer, error) {
		called++
		if len(questions) != 3 || questions[0].Options[0] != "Rust" || questions[1].Options[0] != "Keyboard" || len(questions[2].Options) != 0 {
			t.Fatalf("changed pairs: %+v", questions)
		}
		return []Answer{{Picked: "Rust", Comment: "keep résumé🙂\nspacing"}, {Picked: "Keyboard"}, {Picked: "  Orbit α  "}}, nil
	}})
	for _, names := range [][2]string{{"options", "question"}, {"options2", "question2"}, {"options3", "question3"}} {
		for _, p := range def.Params {
			if p.Name == names[0] && !strings.Contains(p.Desc, "answers to "+names[1]+" only") {
				t.Fatalf("unpaired schema %s: %s", p.Name, p.Desc)
			}
		}
	}
	res, err := def.Execute(t.Context(), tool.Args{"question": "Language?", "options": "Rust\nGo", "question2": "Controls?", "options2": "Keyboard\nMouse", "question3": " Name α? "})
	if err != nil || called != 1 {
		t.Fatal(res, err)
	}
	var got []struct{ Question, Picked, Comment string }
	if err := json.Unmarshal([]byte(res.Text), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Question != "Language?" || got[0].Picked != "Rust" || got[0].Comment != "keep résumé🙂\nspacing" || got[1].Question != "Controls?" || got[1].Picked != "Keyboard" || got[2].Question != " Name α? " || got[2].Picked != "  Orbit α  " {
		t.Fatalf("correlation/fidelity lost: %+v", got)
	}
}

func TestAskOrphanOptionsAndTransportMismatchCommitNothing(t *testing.T) {
	called := false
	def := New(&Prompter{Ask: func(context.Context, []Question) ([]Answer, error) {
		called = true
		return []Answer{{Picked: "answer"}}, nil
	}})
	if _, repair := tool.ValidateJSON(def, `{"questions":[{"header":"camera","question":"Camera?","options":[{"__value__":"Top-down"}]}]}`); repair == "" || !strings.Contains(repair, "question") || called {
		t.Fatal("foreign Ask API admitted or lost actionable repair", repair)
	}
	for _, args := range []tool.Args{{"options": "orphan"}, {"question": "A?", "options2": "orphan"}, {"question": "A?", "question3": " ", "options3": "orphan"}} {
		res, err := def.Execute(t.Context(), args)
		if err == nil || called || res.Text != "" {
			t.Fatalf("orphan asked or published: %+v %v", res, err)
		}
	}
	res, err := def.Execute(t.Context(), tool.Args{"question": "A?", "question2": "B?"})
	if err == nil || res.Text != "" || !called {
		t.Fatal("partial transport result published", res, err)
	}
	// Semantic meaning is authored data: never silently move choices between questions.
	var displayed []Question
	def = New(&Prompter{Ask: func(_ context.Context, qs []Question) ([]Answer, error) { displayed = qs; return nil, nil }})
	res, err = def.Execute(t.Context(), tool.Args{"question": "Camera?", "options": "Continue planning\nBuild now", "question3": "Visual direction?", "options3": "Top-down\nSide-scroll"})
	if err != nil || res.Text != "unanswered" || len(displayed) != 2 || displayed[0].Text != "Camera?" || displayed[0].Options[0] != "Continue planning" || displayed[1].Text != "Visual direction?" || displayed[1].Options[0] != "Top-down" {
		t.Fatal("host inferred or discarded authored pairing", displayed, err)
	}
}

func TestAskCancellationAfterTransportDoesNotPublishDrafts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	def := New(&Prompter{Ask: func(context.Context, []Question) ([]Answer, error) {
		cancel()
		return []Answer{{Picked: "draft α", Comment: "not submitted"}}, nil
	}})
	res, err := def.Execute(ctx, tool.Args{"question": "A?"})
	if err == nil || res.Text != "" {
		t.Fatal("cancelled transport leaked answers", res, err)
	}
}
