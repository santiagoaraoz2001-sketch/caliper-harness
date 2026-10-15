package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

// Both transports must carry the same original annotated source, not template data.
func TestCompactionRoleSeparation(t *testing.T) {
	c := session.Continuation{Version: 1}
	c.Add("history", "user", "history é🙂\r\n\tunchanged  \r\n"+strings.Repeat("old reference ", 50), false)
	c.Entries = append(c.Entries, session.Entry{Kind: "reply", Message: llm.Message{
		Role: "assistant", Content: "proposal\r\n", ToolCalls: []llm.ToolCall{{ID: "call-α", Name: "read", Arguments: `{"path":"é.py"}`}},
	}}, session.Entry{Kind: "output", Message: llm.Message{Role: "tool", Content: "receipt\r\n", ToolCallID: "call-α", Name: "read"}})
	c.Add("intent", "user", "ORIGINAL TASK\r\n", false)
	c.Add("failure", "user", "CHECK FAILED: é\r\n", false)
	wantSource := "[kind=history role=user user text reference-only=false]\n" + c.Entries[0].Message.Content + "\n" +
		"[kind=reply role=assistant model text; unverified reference-only=false]\nproposal\r\n\n" +
		"[model tool proposal unverified id=call-α name=read]\n{\"path\":\"é.py\"}\n" +
		"[kind=output role=tool recorded observation reference-only=false]\nreceipt\r\n\n[tool receipt id=call-α name=read]\n" +
		"[kind=intent role=user user text reference-only=true]\nORIGINAL TASK\r\n\n" +
		"[kind=failure role=user recorded observation reference-only=true]\nCHECK FAILED: é\r\n\n"
	want := []llm.Message{{Role: "system", Content: "WORDS=1000; legacy-range="}, {Role: "user", Content: wantSource}}
	for _, transport := range []string{"plain", "stream"} {
		t.Run(transport, func(t *testing.T) {
			dir := t.TempDir()
			writeCompactionCatalog(t, dir, "WORDS={{.Words}}; legacy-range={{.Range}}")
			m := &scriptModel{replies: []string{"Pending."}}
			r := truthRunner(t, m, 10000)
			r.TemplatesDir = dir
			var wire []llm.Message
			if transport == "stream" {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					var body struct {
						Messages []llm.Message     `json:"messages"`
						Tools    []json.RawMessage `json:"tools"`
						Stream   bool              `json:"stream"`
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					wire = body.Messages
					if !body.Stream || len(body.Tools) != 0 {
						t.Errorf("compaction transport: stream=%v tools=%d", body.Stream, len(body.Tools))
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Pending.\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
				}))
				defer server.Close()
				r.Model = llm.New(server.URL, "fixture")
			}
			next, _, ran, err := r.Compact(context.Background(), c)
			if err != nil || !ran {
				t.Fatalf("compaction: ran=%v err=%v", ran, err)
			}
			if transport == "plain" {
				wire = m.seen[0]
			}
			if !reflect.DeepEqual(wire, want) {
				t.Errorf("role/source bytes differ: got=%+v want=%+v", wire, want)
			}
			for _, e := range c.Entries[3:] {
				found := false
				for _, retained := range next.Entries {
					found = found || reflect.DeepEqual(e, retained)
				}
				if !found {
					t.Errorf("protected entry changed: %+v", e)
				}
			}
		})
	}
	// A long instruction must count even when the annotated source alone fits.
	dir := t.TempDir()
	instruction := strings.Repeat("instructions ", 200)
	writeCompactionCatalog(t, dir, instruction)
	m := &scriptModel{replies: []string{"must not run"}}
	r := truthRunner(t, m, len(instruction)+len(wantSource)+40)
	r.TemplatesDir = dir
	r.UsedIn, r.UsedOut = 17, 23
	next, _, ran, err := r.Compact(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "estimated") || ran || len(m.seen) != 0 || !reflect.DeepEqual(next, c) || r.UsedIn != 17 || r.UsedOut != 23 {
		t.Fatalf("two-message admission must refuse intact before call: ran=%v err=%v calls=%d", ran, err, len(m.seen))
	}
}

// Compact also accepts legacy in-memory histories; every system role stays outside
// the summary even when it is not an injected leading prefix.
func TestCompactProtectsEverySystemRole(t *testing.T) {
	c := session.Continuation{Version: 1}
	c.Add("intent", "user", "ORIGINAL_TASK", false)
	c.Add("history", "user", "OLD_USER_TURN", false)
	c.Add("history", "system", "HOST_SYSTEM_κ\r\nremain byte-exact", false)
	c.Add("reply", "assistant", "OLD_MODEL_TURN"+strings.Repeat(" old draft", 100), false)
	system := c.Entries[2]
	m := &scriptModel{replies: []string{"Old turns summarized."}}
	next, _, ran, err := truthRunner(t, m, 10000).Compact(context.Background(), c)
	if err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if strings.Contains(m.seen[0][1].Content, system.Message.Content) {
		t.Error("system instructions entered the summary source")
	}
	retained := 0
	for _, e := range next.Entries {
		if reflect.DeepEqual(e, system) {
			retained++
		}
	}
	if retained != 1 {
		t.Errorf("exact system entry retained %d times", retained)
	}
}

// Scripted summary/judge, actual settled-round retirement and compaction input.
func TestRetiredGuidanceKeepsCompactionProvenance(t *testing.T) {
	c := session.Continuation{Version: 1}
	c.Add("intent", "user", "original requirement", false)
	a := &session.Assignment{Store: "/owned-store", Workspace: "/owned-workspace", PlanID: "plan", ApproachID: "approach", TaskID: "task", Attempt: 1}
	const advice = "Harness bookkeeping request: inspect PAT before final; unexecuted"
	c.AddGuidance(advice, session.RoundFor(a, ""))
	c.Add("note", "user", "USER_COMMENT_κ", false)
	c.Entries[2].Round = session.RoundFor(a, "actual-comment")
	c.Add("evidence", "user", "Assigned task settlement: actual qualified round", false)
	c.Add("reply", "assistant", strings.Repeat("Consumed model draft; unverified. ", 50), false)
	m := &scriptModel{replies: []string{"Old advice remains unexecuted; user comment retained."}}
	r := truthRunner(t, m, 32768)
	r.Continuation, r.Session, r.SessionID = c, &recordSink{}, "owned"
	if err := r.retireTaskGuidance(context.Background(), session.RoundFor(a, "")); err != nil {
		t.Fatal(err)
	}
	if r.Continuation.Entries[1].Kind != "history" {
		t.Fatal("control did not retire guidance")
	}
	_, _, ran, err := r.Compact(context.Background(), r.Continuation)
	if err != nil || !ran {
		t.Fatalf("compact=%v err=%v", ran, err)
	}
	input := m.seen[0][1].Content
	if !strings.Contains(input, "[kind=history role=user host advice; unexecuted; no authority reference-only=false]\n"+advice) || strings.Count(input, advice) != 1 {
		t.Fatalf("retirement lost original host provenance: %s", input)
	}
	if !strings.Contains(input, "[kind=note role=user user text reference-only=true]\nUSER_COMMENT_κ") {
		t.Fatalf("user comment was reclassified: %s", input)
	}
}
