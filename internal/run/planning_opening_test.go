package run

import (
	"encoding/json"
	"testing"

	"specific-harness/internal/tool"
)

func TestGuidedPlanningOpeningToleranceDoesNotSaveOrAnswer(t *testing.T) {
	for _, query := range []string{"", "x", "test"} {
		t.Run("query="+query, func(t *testing.T) {
			f := newPlanningFixture(t)
			d := f.definition()
			if !d.Opening {
				t.Fatal("entry is not marked as a harmless opening")
			}
			args, ignored := tool.NormalizeOpening(d, tool.Args{"query": query})
			if len(args) != 0 || len(ignored) != 1 || ignored[0] != "query" {
				t.Fatalf("args %v ignored %v", args, ignored)
			}
			f.call(args)
			question := f.definition()
			want := []string{"title", "outcome", "rules", "articles", "proof", "guidance"}
			if question.Opening || len(question.Params) != len(want) {
				t.Fatalf("not the first authored question: %+v", question)
			}
			for i, name := range want {
				if question.Params[i].Name != name || question.Params[i].Required != (name != "guidance") {
					t.Fatalf("goal field %s lost required/optional semantics: %+v", name, question.Params[i])
				}
			}
			args, ignored = tool.NormalizeOpening(question, tool.Args{"query": query})
			if len(ignored) != 0 {
				t.Fatal("authored question discarded unsupported argument")
			}
			raw, _ := json.Marshal(args)
			if _, gap := tool.ValidateJSON(question, string(raw)); gap == "" {
				t.Fatal("opening payload answered an authored question")
			}
			st, err := planningRead(f.d, f.scope)
			if err != nil || st.Plan != nil || st.Approach != nil || st.Task != nil {
				t.Fatalf("opening created authored nodes: %+v %v", st, err)
			}
		})
	}
}
