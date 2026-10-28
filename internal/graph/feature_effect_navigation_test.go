package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// A source-current implementation can still have a disconnected consumer. The
// authored map locates both; only the actual effect check supplies its outcome.
func TestFeatureOwnerCallerNavigationDoesNotTurnFreshSourceIntoEffectProof(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("owner.sh", "sum() { printf '%s' \"$(($1 + $2))\"; }\n")
	write("consumer.sh", ". ./owner.sh\nprintf 0\n")
	a := evidenceAPI(t, s, dir, root)
	projectExec(t, a, `mutation{appendKnowledge(key:"project/code/sum",body:"## owner\nowner.sh defines sum(a,b).\n## callers\nconsumer.sh must call sum; defining the function alone does not change the visible total.",filePath:"owner.sh",expectedPreviousId:""){id}}`)
	projectExec(t, a, `mutation{appendKnowledge(key:"project/features/total",body:"## behavior\nInputs 2 and 3 must display 5.\n## evidence\nRead sessionEvidence(sessionId: total-effect, offset:0, limit:1, callId: effect-before); this is historical execution evidence, not a current qualification.",filePath:"consumer.sh",expectedPreviousId:"",references:[{key:"project/code/sum",relation:"implements",sourceSection:"behavior",section:"callers",label:"Trace the actual consumer"}]){id}}`)
	feature := projectExec(t, a, `{knowledge(key:"project/features/total",section:"behavior"){filePath freshness sections{body references{key section label status}}}}`)
	for _, want := range []string{"consumer.sh", "unchanged", "project/code/sum", "callers", "resolved"} {
		if !strings.Contains(string(feature.Data), want) {
			t.Fatalf("feature route omitted %s: %s", want, feature.Data)
		}
	}
	owner := projectExec(t, a, `{knowledge(key:"project/code/sum",section:"callers"){filePath fileSHA256 freshness sections{body}}}`)
	if !strings.Contains(string(owner.Data), "consumer.sh must call sum") || !strings.Contains(string(owner.Data), "unchanged") {
		t.Fatal(string(owner.Data))
	}
	evidenceAppend(t, dir, "total-effect", "workspace", root)
	check := func(call string, expectedExit int) {
		t.Helper()
		command := `actual=$(. ./consumer.sh); printf 'actual=%s expected=5\n' "$actual"; test "$actual" = 5`
		result, err := bash.NewAt(config.Defaults().Tools.Bash, root).Execute(t.Context(), tool.Args{"cmd": command})
		if err != nil || result.ExitCode == nil {
			t.Fatalf("real Bash execution unavailable: %+v %v", result, err)
		}
		exit, out := *result.ExitCode, result.Text
		if exit != expectedExit {
			t.Fatalf("real consumer check exit=%d want=%d: %s", exit, expectedExit, out)
		}
		args, _ := json.Marshal(map[string]string{"cmd": command})
		evidenceNative(t, dir, "total-effect", "native_reply", llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: call, Name: "bash", Arguments: string(args)}}})
		evidenceNative(t, dir, "total-effect", "native_result", llm.Message{Role: "tool", Name: "bash", ToolCallID: call, Content: fmt.Sprintf("exit %d\n%s", exit, out)})
	}
	check("effect-before", 1)
	failed := evidenceData(t, a, `{sessionEvidence(sessionId:"total-effect",offset:0,limit:1,callId:"effect-before"){historical groups{records{source message{content}}}}}`)
	if !strings.Contains(failed, "actual=0 expected=5") || !strings.Contains(failed, "exit 1") || !strings.Contains(failed, `"historical":true`) {
		t.Fatal(failed)
	}
	// Correct the consumer, not the expected answer. The owner's unchanged hash
	// cannot conceal the consumer change or rewrite prior failure evidence.
	write("consumer.sh", ". ./owner.sh\nsum 2 3\n")
	check("effect-after", 0)
	changed := projectExec(t, a, `{knowledge(key:"project/features/total"){freshness} owner:knowledge(key:"project/code/sum"){freshness}}`)
	var anchors struct {
		Knowledge []struct{ Freshness string }
		Owner     []struct{ Freshness string }
	}
	if err := json.Unmarshal(changed.Data, &anchors); err != nil {
		t.Fatal(err)
	}
	if len(anchors.Knowledge) != 1 || len(anchors.Owner) != 1 || anchors.Knowledge[0].Freshness != "changed" || anchors.Owner[0].Freshness != "unchanged" {
		t.Fatal(string(changed.Data))
	}
	passed := evidenceData(t, a, `{sessionEvidence(sessionId:"total-effect",offset:0,limit:1,callId:"effect-after"){historical groups{records{message{content}}}}}`)
	if !strings.Contains(passed, "actual=5 expected=5") || !strings.Contains(passed, "exit 0") {
		t.Fatal(passed)
	}
	if again := evidenceData(t, a, `{sessionEvidence(sessionId:"total-effect",offset:0,limit:1,callId:"effect-before"){historical groups{records{source message{content}}}}}`); again != failed {
		t.Fatal("later success rewrote prior failure")
	}
}
