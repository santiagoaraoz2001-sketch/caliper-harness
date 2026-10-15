package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/decision"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// Reproduce the complete large Read shape; the actual enforce builder and
// assignment observer run, while owning facts and the judge are controlled.
func TestEnforcementDiscoverySameObservationRetainedOnce(t *testing.T) {
	var page strings.Builder
	page.WriteString("inspection/current/ledger.txt sha256=actual-owner-hash\n")
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&page, "entry_%04d: retained record\n", i)
	}
	page.WriteString("handoff_code: HELP_κ_é_🌱_731\n[shown_range=1-2000; EOF=false; truncated=true; next_offset=2001]")
	actual := tool.Result{Text: page.String(), Truncated: true}
	args := tool.Args{"path": "inspection/current/ledger.txt", "offset": int64(1), "limit": int64(2000)}
	// Explicit evidence-only invocation can substitute the frozen live observation.
	if name := os.Getenv("SPECIFIC_ENFORCER_OBSERVATION"); name != "" {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var fixture observed
		if err = json.Unmarshal(raw, &fixture); err != nil || fixture.Tool != "read" || fixture.Error != "" {
			t.Fatalf("invalid captured Read: %v", err)
		}
		actual, args = fixture.Result, fixture.Args
	}
	for _, mode := range []string{"same call", "older discovery", "distinct equal observation", "no discovery"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			r := &Runner{Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "controlled-store", Kind: "root"}}, Assigned: &AssignedOps{Facts: func(context.Context) (any, error) {
				return map[string]string{"state": "unclaimed", "source": "controlled owning facts"}, nil
			}}}
			exec := r.assignmentExec(func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
				return actual, nil
			})
			returned, err := exec(ctx, tool.Definition{Name: "read"}, args)
			if err != nil || !reflect.DeepEqual(returned, actual) {
				t.Fatalf("actual returned source changed: %v", err)
			}
			latest := r.enforcement.latest
			if latest != r.enforcement.discovery || latest != r.enforcement.current {
				t.Fatal("observer did not capture one actual Read observation")
			}
			exit := 7
			switch mode {
			case "older discovery":
				r.enforcement.discovery = &observed{Tool: "grep", Args: tool.Args{"pattern": "old"}, Result: tool.Result{Text: "older full source β\n", Failed: true, Blocked: true, NoMatches: true, ExitCode: &exit}, Error: "actual older error"}
			case "distinct equal observation":
				copy := *latest
				r.enforcement.discovery = &copy
			case "no discovery":
				r.enforcement.discovery = nil
			}
			discovery := r.enforcement.discovery
			before, _ := json.Marshal([]*observed{latest, discovery})
			calls := 0
			r.Judge = func(_ context.Context, req decision.Request) (decision.Verdict, error) {
				calls++
				if dir := os.Getenv("SPECIFIC_ENFORCER_CAPTURE"); dir != "" && mode == "same call" {
					raw, e := json.Marshal(req)
					if e != nil {
						t.Fatal(e)
					}
					if e = os.WriteFile(filepath.Join(dir, "REQUEST.json"), raw, 0600); e != nil {
						t.Fatal(e)
					}
				}
				var captured struct {
					Latest, LatestDiscovery     *loopObservation
					LatestDiscoverySameAsLatest bool
				}
				if e := json.Unmarshal([]byte(req.Context), &captured); e != nil {
					t.Fatal(e)
				}
				wantAlias := mode == "same call"
				if captured.LatestDiscoverySameAsLatest != wantAlias {
					t.Errorf("same actual observation alias=%t want=%t", captured.LatestDiscoverySameAsLatest, wantAlias)
				}
				if wantAlias {
					if captured.LatestDiscovery != nil {
						t.Error("same complete Read still serialized twice")
					}
					if !strings.Contains(req.Instructions, "LatestDiscoverySameAsLatest") {
						t.Error("actual alias has no reconstruction instruction")
					}
					captured.LatestDiscovery = captured.Latest
				}
				// Compare exact receipt metadata/args/flags; full source stays in the owning observation.
				got, _ := json.Marshal([]*loopObservation{captured.Latest, captured.LatestDiscovery})
				want, _ := json.Marshal([]*loopObservation{loopObserved(latest), loopObserved(discovery)})
				if string(got) != string(want) {
					t.Error("judgment alias cannot reconstruct exact receipt metadata")
				}
				occurrences := 0 // No full source enters the progress judgment.
				if n := strings.Count(req.Context, "entry_0001:"); n != occurrences {
					t.Errorf("actual source body copies=%d want=%d", n, occurrences)
				}
				return decision.Verdict{Option: coherent, Confidence: .9}, nil
			}
			if _, err = r.enforce(ctx, false, "", func(string, string) error { return nil }); err != nil || calls != 1 {
				t.Fatalf("actual judgment failed: calls=%d error=%v", calls, err)
			}
			after, _ := json.Marshal([]*observed{r.enforcement.latest, r.enforcement.discovery})
			if string(after) != string(before) || r.enforcement.latest != latest || r.enforcement.discovery != discovery || r.enforcement.current != latest || !reflect.DeepEqual(returned, actual) {
				t.Fatal("judgment view mutated owning observation/result")
			}
		})
	}
}

// The real observer/builder and refused-call preflight run; facts and Judge are
// controlled. Host guidance alone must not rejudge the same complete Read.
func TestEnforcementOwnAdviceDoesNotRejudgeUnchangedEvidence(t *testing.T) {
	ctx := context.Background()
	facts := map[string]string{"state": "running", "task": "actual controlled task"}
	actual := tool.Result{Text: strings.Repeat("retained source κ\n", 2000), Truncated: true}
	r := &Runner{Continuation: session.Continuation{Workflow: &session.WorkflowScope{Store: "controlled-store", Kind: "root"}}, Threshold: .7, Registry: tool.NewRegistry(), Assigned: &AssignedOps{Facts: func(context.Context) (any, error) { return facts, nil }}}
	r.Continuation.Add("intent", "user", "original coding requirement", false)
	r.Continuation.Coding = &session.CodingState{TaskID: "actual controlled task", Workspace: "actual workspace", Command: "actual check"}
	effects := 0
	exec := r.assignmentExec(func(context.Context, tool.Definition, tool.Args) (tool.Result, error) { effects++; return actual, nil })
	if _, err := exec(ctx, tool.Definition{Name: "read"}, tool.Args{"path": "actual/source.go", "limit": int64(2000)}); err != nil {
		t.Fatal(err)
	}
	r.Registry.Register(tool.Definition{Name: "edit", Params: []tool.Param{{Name: "path", Type: tool.TypeString, Required: true}, {Name: "expected_hash", Type: tool.TypeString, Required: true}}})
	latest := r.enforcement.latest
	before, _ := json.Marshal(latest)
	calls, logs := 0, 0
	var inputs []decision.Request
	r.Judge = func(_ context.Context, q decision.Request) (decision.Verdict, error) {
		calls++
		inputs = append(inputs, q)
		var view struct {
			Latest        *loopObservation
			PendingRepair string
		}
		if err := json.Unmarshal([]byte(q.Context), &view); err != nil {
			t.Fatal(err)
		}
		got, _ := json.Marshal(view.Latest)
		want, _ := json.Marshal(loopObserved(r.enforcement.latest))
		if string(got) != string(want) || view.PendingRepair != r.enforcement.repair {
			t.Fatal("actual Judge lost receipt metadata or current advice")
		}
		return decision.Verdict{Option: enforceOptions[1], Confidence: .3}, nil
	}
	log := func(kind, text string) error {
		if kind == "judgment" {
			logs++
			if !strings.HasSuffix(text, "context="+inputs[len(inputs)-1].Context) {
				t.Fatal("recorded judgment lost complete actual input")
			}
		}
		return nil
	}
	advice, err := r.enforce(ctx, false, "", log)
	if err != nil || advice != "" || calls != 1 {
		t.Fatalf("initial judgment: %q calls=%d err=%v", advice, calls, err)
	}
	refused, sig, productive := r.handleCall(ctx, tool.Call{Name: "edit", Slots: map[string]string{"path": "actual/source.go"}}, "", exec)
	if !strings.Contains(refused, "expected_hash") || sig != "" || productive || effects != 1 || r.enforcement.latest != latest {
		t.Fatalf("preflight refusal became an effect/observation: %q", refused)
	}
	again, err := r.enforce(ctx, false, "", log)
	if err != nil || again != advice || calls != 1 || logs != 1 {
		t.Fatalf("own advice alone rejudged unchanged evidence: calls=%d logs=%d err=%v", calls, logs, err)
	}
	after, _ := json.Marshal(latest)
	if string(before) != string(after) {
		t.Fatal("cache mutated complete source observation")
	}
	final, answer := false, ""
	changes := []struct {
		name  string
		apply func()
	}{
		{"owning facts", func() { facts["state"] = "failed" }},
		{"observation", func() {
			r.enforcement.latest = &observed{Tool: "read", Args: tool.Args{"path": "other/source.go"}, Result: actual, Error: "actual changed error"}
		}},
		{"discovery", func() {
			r.enforcement.discovery = &observed{Tool: "grep", Result: tool.Result{Text: "new actual discovery"}}
		}},
		{"coding", func() { r.Continuation.Coding.Revision++ }},
		{"original intent", func() { r.Continuation.Add("intent", "user", "changed original requirement", false) }},
		{"current request", func() { r.Continuation.Add("task", "user", "actual distinct request", false) }},
		{"normal final", func() { final = true }},
		{"provisional answer", func() { answer = "unverified actual final claim" }},
	}
	for _, change := range changes {
		prior := calls
		change.apply()
		if _, err := r.enforce(ctx, final, answer, log); err != nil || calls != prior+1 {
			t.Fatalf("%s did not invalidate: calls=%d err=%v", change.name, calls, err)
		}
		if _, err := r.enforce(ctx, final, answer, log); err != nil || calls != prior+1 {
			t.Fatalf("%s advice rejudged again: calls=%d err=%v", change.name, calls, err)
		}
	}
	if len(inputs) != 9 || logs != 9 || effects != 1 {
		t.Fatalf("actual decision/log/effect counts: %d/%d/%d", len(inputs), logs, effects)
	}
	var newInput struct{ PendingRepair string }
	if err := json.Unmarshal([]byte(inputs[1].Context), &newInput); err != nil || newInput.PendingRepair != advice {
		t.Fatal("new evidence Judge omitted complete pending advice")
	}
}
