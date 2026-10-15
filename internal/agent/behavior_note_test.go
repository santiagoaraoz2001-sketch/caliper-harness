package agent

import (
	"context"
	"errors"
	"specific-harness/internal/tool"
	"strings"
	"testing"
)

type behaviorNoteFailure struct{}

func (behaviorNoteFailure) Append(_ context.Context, _, kind, _ string) error {
	if kind == "note" {
		return errors.New("behavior note recording refused")
	}
	return nil
}
func TestBehaviorNoteRecordingFailureStaysLoud(t *testing.T) {
	r := &Runner{Model: &scriptModel{replies: []string{"done"}}, Registry: tool.NewRegistry(), MaxSteps: 1, Behavior: "RULE", BehaviorNote: "note: capped", Session: behaviorNoteFailure{}}
	if _, _, err := r.Run(context.Background(), "task"); err == nil || !strings.Contains(err.Error(), "behavior note recording refused") {
		t.Fatalf("session failure lost %v", err)
	}
}
