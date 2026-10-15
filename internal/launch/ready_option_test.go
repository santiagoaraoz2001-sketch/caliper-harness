package launch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/decision"
	"specific-harness/internal/prompts"
	"strings"
	"testing"
)

func TestReadyUnknownVerdictRefusesBeforeAsk(t *testing.T) {
	for _, confidence := range []float64{1, .1} {
		t.Run(fmt.Sprint(confidence), func(t *testing.T) {
			_, aid, d := gateDeps(t, 1)
			asked := false
			d.Ask = func(context.Context, string) bool { asked = true; return true }
			d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
				return decision.Verdict{Option: "unrecognized", Confidence: confidence}, nil
			}
			if err := ReadyCheck(context.Background(), d, aid); err == nil || !strings.Contains(err.Error(), "unknown readiness option") || asked {
				t.Fatalf("invalid choice admitted/asked: %v asked=%v", err, asked)
			}
		})
	}
}

func TestReadyDuplicateOptionsRefuseBeforeJudge(t *testing.T) {
	_, aid, d := gateDeps(t, 1)
	raw := strings.Replace(prompts.Builtin().YAML(), offGoal, servesGoal, 1)
	file := filepath.Join(t.TempDir(), "prompts.yaml")
	if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	var err error
	d.Prompts, err = prompts.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	d.Judge = func(context.Context, decision.Request) (decision.Verdict, error) {
		called = true
		return decision.Verdict{Option: servesGoal, Confidence: 1}, nil
	}
	if err = ReadyCheck(context.Background(), d, aid); err == nil || called {
		t.Fatalf("duplicate options reached judge: %v called=%v", err, called)
	}
}
