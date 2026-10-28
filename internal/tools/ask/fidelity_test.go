package ask

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"specific-harness/internal/agent"
	"specific-harness/internal/graph"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
)

func TestAskFidelityNonblankAndEmptyValues(t *testing.T) {
	raw := " \tR41_RAW\n\tsecond\n\nLAST  "
	answers := []Answer{{Picked: raw, Comment: " \tcomment\n  "}, {Picked: " \t\n ", Comment: " \t\n"}}
	d := New(&Prompter{Ask: func(context.Context, []Question) ([]Answer, error) { return answers, nil }})
	res, err := d.Execute(context.Background(), tool.Args{"question": "raw?", "question2": "blank?"})
	want := `[{"question":"raw?","picked":" \tR41_RAW\n\tsecond\n\nLAST  ","comment":" \tcomment\n  "},{"question":"blank?","picked":" \t\n ","comment":" \t\n"}]`
	if err != nil || res.Text != want {
		t.Fatalf("nonblank bytes/blank semantics got%q want%q error%v", res.Text, want, err)
	}
}

func TestAskFidelityStdinTerminatorsAndNumericRouting(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", ""} {
		t.Run(fmt.Sprintf("ending%q", ending), func(t *testing.T) {
			raw := " \tanswer  "
			res, err := New(&Prompter{In: strings.NewReader(raw + ending), Out: io.Discard}).Execute(context.Background(), tool.Args{"question": "raw?"})
			if err != nil || res.Text != `[{"question":"raw?","picked":" \tanswer  ","comment":""}]` {
				t.Fatalf("line content changed %q %v", res.Text, err)
			}
		})
	}
	p := &Prompter{In: strings.NewReader(" \t2  \r\n \tcomment  \r\n"), Out: io.Discard}
	res, err := New(p).Execute(context.Background(), tool.Args{"question": "which?", "options": "slow\n fast"})
	if err != nil || res.Text != `[{"question":"which?","picked":"fast","comment":" \tcomment  "}]` {
		t.Fatalf("numeric routing/comment bytes %q %v", res.Text, err)
	}
	res, err = New(&Prompter{In: strings.NewReader("answer\r"), Out: io.Discard}).Execute(context.Background(), tool.Args{"question": "raw?"})
	if err != nil || res.Text != `[{"question":"raw?","picked":"answer\r","comment":""}]` {
		t.Fatalf("lone CR content was removed %q %v", res.Text, err)
	}
}

func TestAskFidelityRunnerHTTPAndNativeSession(t *testing.T) {
	for _, display := range []bool{false, true} {
		t.Run(fmt.Sprintf("callback%t", display), func(t *testing.T) {
			dir := t.TempDir()
			if root := os.Getenv("R41_ASK_EVIDENCE_ROOT"); root != "" {
				var err error
				dir, err = os.MkdirTemp(root, "transport-")
				if err != nil {
					t.Fatal(err)
				}
			}
			raw := " \tR41_RAW\n\tsecond\n\nLAST  "
			comment := " \tcomment\n  "
			observation := "tool result:\n" + `[{"question":"raw?","picked":" \tR41_RAW\n\tsecond\n\nLAST  ","comment":" \tcomment\n  "}]`
			requests := make(chan []llm.Message, 2)
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				idx := calls.Add(1)
				body, err := io.ReadAll(req.Body)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				if err = os.WriteFile(filepath.Join(dir, fmt.Sprintf("request%d.json", idx)), body, 0600); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				var in struct {
					Messages []llm.Message
					Stream   bool
				}
				if err = json.Unmarshal(body, &in); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				requests <- in.Messages
				reply := "fixture completed"
				if idx == 1 {
					reply = "```tool\ntool: ask\nquestion: raw?\n```"
				}
				if !in.Stream {
					t.Error("tool exchange must stream with or without a display callback")
					http.Error(w, "expected tool stream", 400)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				b, _ := json.Marshal(map[string]any{"model": "fixture", "choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
			}))
			defer srv.Close()
			s, err := store.Open(filepath.Join(dir, "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			reg := tool.NewRegistry()
			reg.Register(New(&Prompter{Ask: func(context.Context, []Question) ([]Answer, error) {
				return []Answer{{Picked: raw, Comment: comment}}, nil
			}}))
			var out bytes.Buffer
			sessions := filepath.Join(dir, "sessions")
			r := &agent.Runner{Model: llm.New(srv.URL, "fixture").WithToolProtocol("text"), Registry: reg, MaxSteps: 2, Out: &out, SessionID: "ask-fidelity", Session: graph.NewSessionLog(graph.New(s, "fixture", sessions))}
			if display {
				r.OnToken = func(string) {}
			}
			final, transcript, err := r.Run(context.Background(), "ask raw")
			if err != nil || final != "fixture completed" || calls.Load() != 2 {
				t.Fatalf("Runner settlement %q %v calls%d", final, err, calls.Load())
			}
			<-requests
			messages := <-requests
			if messages[len(messages)-1].Content != observation {
				t.Errorf("actual next request got%q want%q", messages[len(messages)-1].Content, observation)
			}
			// Settlement can append factual evidence after the reply; assert the
			// byte-exact observation and its provenance without a tail index.
			retained, outputs := 0, 0
			for _, message := range transcript {
				if message.Role == "user" && message.Content == observation {
					retained++
				}
			}
			for _, entry := range r.Continuation.Entries {
				if entry.Kind == "output" || entry.Kind == "answer" {
					outputs++
					if entry.Message.Role != "user" || entry.Message.Content != observation || entry.Unread {
						t.Errorf("settled Ask observation provenance/fidelity changed %+v", entry)
					}
				}
			}
			if retained != 1 || outputs != 1 {
				t.Errorf("returned/continued Ask observation counts %d/%d, want1/1", retained, outputs)
			}
			events, err := session.Read(sessions, "ask-fidelity")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, event := range events {
				if event.Kind == "result" {
					found = event.Text == observation
					if !found {
						t.Errorf("native result got%q want%q", event.Text, observation)
					}
				}
			}
			if !found {
				t.Error("native session lacks exact public Ask tool result")
			}
			t.Logf("HTTP fixture/SSE; display callback=%t; actual public tool/Runner/native GraphQL-session artifacts %s; no inference", display, dir)
		})
	}
}
