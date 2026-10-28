package agent

import (
	"context"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
	"testing"
)

func TestCheckUsesVerificationCapBothStagesAndHonorsShorterRequest(t *testing.T) {
	for _, raw := range []string{`{}`, `{"timeout":7}`} {
		r := &Runner{Prompts: prompts.Builtin(), CheckCommand: "suite", CheckTimeoutSeconds: 600, Session: &recordSink{}, SessionID: "timeout", Continuation: session.Continuation{Coding: &session.CodingState{TaskID: "task", Attempt: 1, Workspace: t.TempDir(), Command: "suite", ConfigCommand: "suite", AcceptanceCommand: "accept"}}}
		var got []int64
		bash := tool.Definition{Name: "bash", Params: []tool.Param{{Name: "cmd", Type: tool.TypeString, Required: true}, {Name: "timeout", Type: tool.TypeInt, Default: "30"}}, Execute: func(_ context.Context, a tool.Args) (tool.Result, error) {
			got = append(got, a["timeout"].(int64))
			zero := 0
			return tool.Result{ExitCode: &zero}, nil
		}}
		def := r.CheckDefinition(bash)
		args, repair := tool.ValidateJSON(def, raw)
		if repair != "" {
			t.Fatal(repair)
		}
		_, err := Direct(t.Context(), def, args)
		want := int64(600)
		if raw != `{}` {
			want = 7
		}
		if err != nil || len(got) != 2 || got[0] != want || got[1] != want || !r.Continuation.Coding.Passed() {
			t.Fatalf("stages=%v err=%v receipt=%+v", got, err, r.Continuation.Coding.Receipt)
		}
		if bash.Params[1].Default != "30" {
			t.Fatal("ordinary shell default changed")
		}
	}
}
