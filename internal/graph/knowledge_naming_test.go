package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/store"
)

func TestKnowledgeAppendNameCompatibilityAndPartialEffects(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := projectAPI(t, s, t.TempDir())
	first := projectExec(t, a, `mutation { appendKnowledge(key:"product/decisions",body:"20x20",expectedPreviousId:"") {id body previousId} }`)
	if len(first.Effects) != 1 || first.Effects[0].Field != "appendKnowledge" || first.Effects[0].Status != "applied" {
		t.Fatalf("public append receipt: %+v", first)
	}
	firstID := first.Effects[0].ID
	second := projectExec(t, a, fmt.Sprintf(`mutation { putKnowledge(key:"product/decisions",body:"20x20 dark",expectedPreviousId:%q) {id body previousId} }`, firstID))
	secondID := second.Effects[0].ID
	if secondID == firstID || second.Effects[0].Field != "putKnowledge" || !strings.Contains(string(second.Data), firstID) {
		t.Fatalf("legacy alias changed version semantics: %+v", second)
	}
	q := fmt.Sprintf(`mutation { good:appendKnowledge(key:"product/decisions",body:"20x20 dark progressive",expectedPreviousId:%q) {id} stale:putKnowledge(key:"product/decisions",body:"wrong",expectedPreviousId:%q) {id} separate:appendKnowledge(key:"separate",body:"retained independent commit",expectedPreviousId:"") {id} }`, secondID, firstID)
	data, err := a.ExecBounded(context.Background(), q, "", nil, config.Defaults().Tools.GraphQL)
	var receipt Result
	if err == nil || json.Unmarshal(data, &receipt) != nil || len(receipt.Effects) != 3 || receipt.Effects[0].Status != "applied" || receipt.Effects[0].ID == "" || receipt.Effects[1].Status == "applied" || receipt.Effects[2].Status != "applied" || receipt.Effects[2].ID == "" {
		t.Fatalf("actual mixed-name partial receipts lost: %s %v", data, err)
	}
	head := projectExec(t, a, `{knowledge(key:"product/decisions",limit:1){id body previousId}}`)
	if !strings.Contains(string(head.Data), "progressive") || !strings.Contains(string(head.Data), secondID) {
		t.Fatal(string(head.Data))
	}
	history := projectExec(t, a, fmt.Sprintf(`{knowledge(id:%q){id body}}`, firstID))
	if !strings.Contains(string(history.Data), `"body":"20x20"`) {
		t.Fatal("alias overwrote immutable history")
	}
	separate := projectExec(t, a, `{knowledge(key:"separate",limit:1){id body}}`)
	if !strings.Contains(string(separate.Data), receipt.Effects[2].ID) || !strings.Contains(string(separate.Data), "retained independent commit") {
		t.Fatal("partial effect receipt lost its actual independent commit")
	}
	// Invalid selected fields and essential-receipt overflow must refuse before effects.
	_, err = a.ExecBounded(context.Background(), `mutation{appendKnowledge(key:"invalid",body:"x",expectedPreviousId:""){nonexistent}}`, "", nil, config.Defaults().Tools.GraphQL)
	if err == nil {
		t.Fatal("invalid selection admitted")
	}
	limits := config.Defaults().Tools.GraphQL
	limits.MaxResultBytes = 100
	_, err = a.ExecBounded(context.Background(), `mutation{appendKnowledge(key:"invalid",body:"x",expectedPreviousId:""){id}}`, "", nil, limits)
	if err == nil || string(projectExec(t, a, `{knowledge(key:"invalid",limit:1){id}}`).Data) != `{"knowledge":[]}` {
		t.Fatal("schema alias bypassed preflight budget/selection refusal")
	}
}
