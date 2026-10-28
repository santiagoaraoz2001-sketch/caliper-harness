package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func ownedPlanningRunner(t *testing.T) *Runner {
	t.Helper()
	r := planningTransportRunner()
	r.Workspace, r.SessionID, r.Session = t.TempDir(), "owned-planning", &recordSink{}
	r.Assigned = &AssignedOps{Store: "store"}
	r.Continuation.Add("intent", "user", "preserve actual requested behavior", false)
	return r
}

func TestPlanningPublicationReplacesOnlyOwnedCurrentFrame(t *testing.T) {
	r := ownedPlanningRunner(t)
	r.workflowHeader = "DRAFT PLAN root children=0"
	r.ensurePlanningPublication()
	first := r.Continuation.Clone()
	if r.ensurePlanningPublication() || !reflect.DeepEqual(first, r.Continuation) {
		t.Fatal("unchanged current publication moved or invalidated")
	}
	legacy := session.Entry{Kind: "schema", Message: llm.Message{Role: "user", Content: "historical unknown schema"}}
	foreign := session.Entry{Kind: "schema", PreparationScope: "other-store\nother-project", Message: llm.Message{Role: "user", Content: "foreign schema"}}
	unread := session.Entry{Kind: "schema", Unread: true, Message: llm.Message{Role: "user", Content: "unread schema"}}
	r.Continuation.Entries = append(r.Continuation.Entries, legacy, foreign, unread)
	r.workflowHeader = "DRAFT PLAN root children=1"
	if !r.ensurePlanningPublication() {
		t.Fatal("changed owner header was not published")
	}
	owned := 0
	for _, entry := range r.Continuation.Entries {
		if entry.Kind == "schema" && r.ownsPreparationPublication(entry) {
			owned++
			if entry.Message.Content != r.workflowHeader+"\n"+r.currentPlanningQuestion() {
				t.Fatal("stale owned header survived")
			}
		}
	}
	if owned != 1 {
		t.Fatalf("same question accumulated %d owned schema frames", owned)
	}
	for i, want := range []session.Entry{legacy, foreign, unread} {
		if !reflect.DeepEqual(r.Continuation.Entries[i+1], want) {
			t.Fatal("unknown/foreign/unread schema changed")
		}
	}
	before := r.Continuation.Clone()
	if r.ensurePlanningPublication() || !reflect.DeepEqual(before, r.Continuation) {
		t.Fatal("unchanged replacement moved")
	}
	if err := r.Continuation.Snapshot().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPlanningReceiptRetiresOnlyRedundantOwnedSchema(t *testing.T) {
	r := ownedPlanningRunner(t)
	r.ensurePlanningPublication()
	question := r.currentPlanningQuestion()
	def, _ := r.Registry.Lookup("plan")
	def.Execute = func(context.Context, tool.Args) (tool.Result, error) {
		body, _ := json.Marshal(map[string]string{"next_question": question})
		return tool.Result{Text: string(body)}, nil
	}
	r.Registry = tool.NewRegistry(r.Prompts)
	r.Registry.Register(def)
	log := func(kind, text string) error { return r.Session.Append(t.Context(), r.SessionID, kind, text) }
	_, _, err := r.nativeStep(t.Context(), llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "actual-plan", Name: "plan", Arguments: `{"observations":"actual source"}`}}}, Direct, log, nil, func(string, bool, string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	group := append([]session.Entry(nil), r.Continuation.Entries[len(r.Continuation.Entries)-2:]...)
	if !r.ensurePlanningPublication() || !r.planningPublished(question) {
		t.Fatal("actual current receipt did not retire its redundant schema")
	}
	if len(r.Continuation.Entries) != 3 || !reflect.DeepEqual(r.Continuation.Entries[1:], group) {
		t.Fatal("actual native group or intent changed")
	}
	if r.ensurePlanningPublication() {
		t.Fatal("receipt caused another publication")
	}
}

func TestActualClaimRetiresOwnedPreparationOnlyAfterReadmission(t *testing.T) {
	for _, mode := range []string{"healthy", "admit-refused", "sink-error", "cancel-after-append"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r := ownedPlanningRunner(t)
			sink := r.Session.(*recordSink)
			r.Assigned.Facts = func(context.Context) (any, error) {
				return BookkeepingFacts{Preparation: "current planning question"}, nil
			}
			r.ensurePlanningPublication()
			if ok, err := r.publishPreparationGuidance(ctx, "answer the current question", true, ""); err != nil || !ok {
				t.Fatal(ok, err)
			}
			protected := []session.Entry{
				{Kind: "guidance", Message: llm.Message{Role: "user", Content: "legacy unknown advice"}},
				{Kind: "guidance", PreparationScope: "foreign", Message: llm.Message{Role: "user", Content: "foreign advice"}},
				{Kind: "schema", Unread: true, Message: llm.Message{Role: "user", Content: "unread schema"}},
				{Kind: "note", Message: llm.Message{Role: "user", Content: "actual user constraint"}},
				{Kind: "guidance", PreparationScope: r.preparationScope(), Round: &session.TaskRound{Store: "store", Workspace: r.Workspace, TaskID: "task-a", Attempt: 1}, Message: llm.Message{Role: "user", Content: "owned round evidence"}},
			}
			r.Continuation.Entries = append(r.Continuation.Entries, protected...)
			r.Assigned.Start = func(context.Context, string) (*session.Assignment, string, error) {
				return &session.Assignment{Store: "store", Workspace: r.Workspace, PlanID: "plan", ApproachID: "approach", TaskID: "task-a", Attempt: 1}, "true", nil
			}
			r.Assigned.Admit = func(context.Context) error {
				if mode == "admit-refused" {
					return errors.New("current claim refused")
				}
				return nil
			}
			r.Registry.Register(r.TaskStartDefinition())
			log := func(kind, text string) error { return r.Session.Append(ctx, r.SessionID, kind, text) }
			_, _, err := r.nativeStep(ctx, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "actual-claim", Name: "task_start", Arguments: `{"task":"task-a"}`}}}, Direct, log, nil, func(string, bool, string) string { return "" })
			if err != nil || !r.assignedClaim || r.claimPreparationEnd <= 0 {
				t.Fatalf("actual claim missing: %v", err)
			}
			before := r.Continuation.Clone()
			recorded := len(sink.texts)
			if mode == "sink-error" {
				sink.err = errors.New("sink refused")
			}
			if mode == "cancel-after-append" {
				r.Session = &preparationCancellingSink{sink: sink, cancel: cancel}
			}
			err = r.archiveClaimPreparation(ctx)
			if mode != "healthy" {
				if err == nil || !reflect.DeepEqual(before, r.Continuation) || r.claimPreparationEnd <= 0 {
					t.Fatal("failed retirement changed active continuation", err)
				}
				return
			}
			// Independent preservation oracle: the actual intent, the explicitly
			// protected entries above, and the real native claim group survive.
			// Do not derive this expectation from the production deletion predicate.
			group := before.Entries[len(before.Entries)-2:]
			if group[0].Message.Role != "assistant" || group[1].Message.Role != "tool" || group[1].Message.Name != "task_start" || group[1].Message.ToolCallID != "actual-claim" {
				t.Fatal("fixture did not capture the actual native claim group")
			}
			want := append([]session.Entry{before.Entries[0]}, protected...)
			want = append(want, group...)
			if err != nil || !reflect.DeepEqual(want, r.Continuation.Entries) || r.claimPreparationEnd != 0 || len(sink.texts) != recorded+1 {
				t.Fatal("retirement changed exact evidence or lacked durable snapshot", err)
			}
			var durable session.Continuation
			if json.Unmarshal([]byte(sink.texts[len(sink.texts)-1]), &durable) != nil || !reflect.DeepEqual(durable.Entries, r.Continuation.Snapshot().Entries) {
				t.Fatal("durable retirement differs")
			}
			if err := r.Continuation.Snapshot().Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
