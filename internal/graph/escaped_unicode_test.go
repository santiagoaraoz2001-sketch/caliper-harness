package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/store"
)

func TestGraphEscapedUnicodeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db")
	var s *store.Store
	var api *API
	open := func() {
		var err error
		s, err = store.Open(db)
		if err != nil {
			t.Fatal(err)
		}
		api = New(s, "unicode-test", filepath.Join(dir, "sessions"))
	}
	open()
	defer func() { s.Close() }()
	exec := func(query string, vars map[string]any) map[string]json.RawMessage {
		t.Helper()
		raw, err := api.Exec(context.Background(), query, "", vars)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	id := func(raw json.RawMessage) string {
		t.Helper()
		var node struct{ ID string }
		if err := json.Unmarshal(raw, &node); err != nil || node.ID == "" {
			t.Fatalf("missing node id: %s (%v)", raw, err)
		}
		return node.ID
	}
	pid := id(exec(`mutation{createPlan(title:"unicode",goal:"round trip"){id}}`, nil)["createPlan"])
	exec(`mutation($id:ID!){transitionPlan(id:$id,to:"active"){id}}`, map[string]any{"id": pid})
	aid := id(exec(`mutation($id:ID!){createApproach(planId:$id,title:"draft"){id}}`, map[string]any{"id": pid})["createApproach"])
	tid := id(exec(`mutation($id:ID!){createTask(approachId:$id,title:"fixture",prompt:"no execution"){id}}`, map[string]any{"id": aid})["createTask"])
	block := func(value string) string {
		return fmt.Sprintf(`setTaskBlock(id:%q,current:%s,target:"t",sketch:"s",acceptance:"a",tests:"tests",verify:"v"){id current attempts}`, tid, value)
	}
	assertCurrent := func(t *testing.T, raw json.RawMessage, want string) {
		t.Helper()
		var task struct {
			Current  string
			Attempts int
		}
		if err := json.Unmarshal(raw, &task); err != nil || task.Current != want || task.Attempts != 0 {
			t.Errorf("exact current/attempts: got %q / %d, want %q / 0 (%v)", task.Current, task.Attempts, want, err)
		}
	}
	// Exact first six logical lines of the retained R23/R32 native source fixture.
	mixed := "R23_PREFIX TOKEN" + strings.Repeat("X", 275) + " END_FIRST\n\tR23_TAB\talpha  \n    R23_INDENT beta   \r\n\nR23_UNICODE café Ελληνικά 東京 👩‍💻  \nR23_CRLF retained\r\n"
	quoted, _ := json.Marshal(mixed)
	escaped := strings.ReplaceAll(strings.ReplaceAll(string(quoted), "👩‍💻", `\ud83d\udc69\u200d\ud83d\udcbb`), "é", `\u00e9`)
	backslashes := `literal \ud83d\udc69 "quoted" \tail`
	backquoted, _ := json.Marshal(backslashes)
	cases := []struct {
		name, query, want string
		vars              map[string]any
	}{
		{"escaped_pairs", `mutation{` + block(escaped) + `}`, mixed, nil},
		{"literal_utf8", `mutation{` + block(string(quoted)) + `}`, mixed, nil},
		{"variables", `mutation($value:String!){` + block("$value") + `}`, mixed, map[string]any{"value": mixed}},
		{"literal_backslashes", `mutation{` + block(string(backquoted)) + `}`, backslashes, nil},
		{"block_string", `mutation{` + block(`"""literal \ud83d\udc69"""`) + `}`, `literal \ud83d\udc69`, nil},
		{"comment", "# \\ud83d\\udc69 \"not a value\"\nmutation{" + block(string(quoted)) + `}`, mixed, nil},
		{"default_literal", `mutation($value:String! = ` + escaped + `){` + block("$value") + `}`, mixed, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertCurrent(t, exec(c.query, c.vars)["setTaskBlock"], c.want)
			s.Close()
			open()
			assertCurrent(t, exec(`query($id:ID!){task(id:$id){current attempts}}`, map[string]any{"id": tid})["task"], c.want)
		})
	}
	exec(`mutation($value:String!){`+block("$value")+`}`, map[string]any{"value": mixed})
	for _, bad := range []string{`"\u12G4"`, `"\u123"`, `"unclosed`} {
		t.Run("malformed_"+bad, func(t *testing.T) {
			query := `mutation{before:` + block(`"must not change"`) + ` bad:` + block(bad) + `}`
			if _, err := api.Exec(context.Background(), query, "", nil); err == nil {
				t.Fatal("malformed inline value accepted")
			}
			raw := exec(`query($id:ID!){approach(id:$id){tasks{id current attempts}}}`, map[string]any{"id": aid})["approach"]
			var a struct{ Tasks []json.RawMessage }
			if err := json.Unmarshal(raw, &a); err != nil || len(a.Tasks) != 1 {
				t.Fatalf("mutated task count: %s (%v)", raw, err)
			}
			assertCurrent(t, a.Tasks[0], mixed)
		})
	}
}
