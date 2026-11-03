package agent

import (
	"strings"
	"testing"
)

func TestToolSurfaceDelta(t *testing.T) {
	if got := toolSurfaceDelta(nil, []string{"bash"}); got != "" {
		t.Fatalf("first request must stay silent, got %q", got)
	}
	if got := toolSurfaceDelta([]string{"a", "b"}, []string{"b", "a"}); got != "" {
		t.Fatalf("reordering is not a change, got %q", got)
	}
	got := toolSurfaceDelta([]string{"plan", "bash", "workflow_task"}, []string{"plan", "edit", "bash"})
	if !strings.Contains(got, "now available: edit") || !strings.Contains(got, "no longer available: workflow_task") {
		t.Fatalf("delta must name both sides: %q", got)
	}
}
