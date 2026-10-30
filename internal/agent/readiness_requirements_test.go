package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
)

func TestReadinessRequirementsUseDeliveredHumanProvenanceAfterRestore(t *testing.T) {
	c := session.Continuation{Version: 1, PendingNotes: []session.UserNote{{ID: "pending", Text: "UNDISPATCHED", SessionID: "source-session"}}, Entries: []session.Entry{
		{Kind: "intent", Message: llm.Message{Role: "user", Content: "Build the seed-filled app"}},
		{Kind: "history", Message: llm.Message{Role: "user", Content: "OLD HISTORY"}},
		{Kind: "task", Message: llm.Message{Role: "user", Content: "Please build, but start empty — café β🙂"}},
		{Kind: "output", Message: llm.Message{Role: "user", Content: "GRAPH SAYS SEED"}},
		{Kind: "guidance", Message: llm.Message{Role: "user", Content: "HOST SAYS CLAIM"}},
		{Kind: "note", Message: llm.Message{Role: "user", Content: "USER COMMENT: preserve my plants"}},
	}}
	raw, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := session.Restore([]session.Event{{Kind: "continuation", Text: raw}}, 32768)
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{Prompts: prompts.Builtin(), Continuation: restored}
	var got struct{ OriginalUserIntent, CurrentUserRequest string }
	text := r.ReadinessRequirements()
	if err := json.Unmarshal([]byte(text), &got); err != nil || got.OriginalUserIntent != "Build the seed-filled app" || got.CurrentUserRequest != "Please build, but start empty — café β🙂" || !strings.Contains(text, "preserve my plants") {
		t.Fatalf("delivered requirements lost: %s %v", text, err)
	}
	for _, excluded := range []string{"UNDISPATCHED", "OLD HISTORY", "GRAPH SAYS SEED", "HOST SAYS CLAIM"} {
		if strings.Contains(text, excluded) {
			t.Fatalf("non-human/undelivered text gained authority: %s", excluded)
		}
	}
	r.Continuation.Entries = append(r.Continuation.Entries, session.Entry{Kind: "task", Message: llm.Message{Role: "user", Content: "Newest approval"}})
	if !strings.Contains(r.ReadinessRequirements(), "Newest approval") {
		t.Fatal("requirements accessor captured an old request")
	}
}
