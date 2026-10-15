package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"specific-harness/internal/tools/fetch"
)

type interruptedModel struct {
	text  string
	usage llm.Usage
	err   error
}

func (m interruptedModel) Chat(context.Context, []llm.Message) (string, string, llm.Usage, error) {
	return m.text, "interrupted", m.usage, m.err
}
func (m interruptedModel) ChatStream(_ context.Context, _ []llm.Message, delta func(string)) (string, string, llm.Usage, error) {
	if delta != nil && m.text != "" {
		delta(m.text)
	}
	return m.text, "interrupted", m.usage, m.err
}

func TestTransportFailureNeverExecutesPartialTool(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "plain"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "must-not-exist")
			raw := "```tool\ntool: bash\ncmd: printf marker > '" + marker + "'\n```\n"
			reg := tool.NewRegistry()
			reg.Register(bash.New(config.BashConfig{DefaultTimeout: 5, MaxTimeout: 10, MaxBytes: 4096, MaxLines: 50}))
			sink := &recordSink{}
			var out, deltas bytes.Buffer
			r := &Runner{Model: interruptedModel{raw, llm.Usage{In: 19, Out: 7, Reported: true}, io.ErrUnexpectedEOF}, Registry: reg, MaxSteps: 2, Out: &out, Session: sink, SessionID: "partial"}
			if stream {
				r.OnToken = func(s string) { deltas.WriteString(s) }
			}
			final, tx, err := r.Run(context.Background(), "write the marker")
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("original transport cause lost: %v", err)
			}
			if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("partial tool affected artifact: %v", statErr)
			}
			want := "partial reply (not complete; not executed):\n" + raw
			if final != want {
				t.Errorf("partial final=%q want=%q", final, want)
			}
			if len(tx) < 2 || tx[len(tx)-2].Role != "assistant" || tx[len(tx)-2].Content != want || !strings.Contains(tx[len(tx)-1].Content, "model error: unexpected EOF\npartial response usage: in=19 out=7 (server-reported)") {
				t.Errorf("partial/error transcript=%+v", tx)
			}
			if !strings.Contains(out.String(), want) || !strings.Contains(out.String(), "model error: unexpected EOF") {
				t.Errorf("visible partial evidence=%q", out.String())
			}
			if r.UsedIn != 19 || r.UsedOut != 7 || r.UsageUnknown {
				t.Errorf("usage lost: %+v", r)
			}
			if strings.Join(sink.kinds, ",") != "prompt,context,context,partial,error,continuation" || len(sink.texts) < 5 || sink.texts[3] != want {
				t.Errorf("session must label partial/error and never done/tool: %q %q", sink.kinds, sink.texts)
			}
			if stream && deltas.String() != raw {
				t.Errorf("stream changed raw evidence: %q", deltas.String())
			}
		})
	}
}

func TestTransportEmptyFailureAndSessionCause(t *testing.T) {
	for _, persistFail := range []bool{false, true} {
		name := "normal-session"
		if persistFail {
			name = "broken-session"
		}
		t.Run(name, func(t *testing.T) {
			sessionErr := errors.New("session storage unavailable")
			sink := &recordSink{}
			if persistFail {
				sink.err = sessionErr
			}
			var out bytes.Buffer
			r := &Runner{Model: interruptedModel{err: io.ErrUnexpectedEOF}, Registry: tool.NewRegistry(), MaxSteps: 1, Out: &out, Session: sink}
			final, tx, err := r.Run(context.Background(), "hello")
			if final != "" || !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("empty failure=%q %v", final, err)
			}
			if persistFail && !errors.Is(err, sessionErr) {
				t.Errorf("session cause swallowed: %v", err)
			}
			if !r.UsageUnknown || len(tx) < 1 || !strings.Contains(tx[len(tx)-1].Content, "partial response usage: unknown (not reported by server)") {
				t.Errorf("unknown usage/error not retained: %+v", tx)
			}
			if !persistFail && strings.Join(sink.kinds, ",") != "prompt,context,context,error,continuation" {
				t.Errorf("empty failure events=%q", sink.kinds)
			}
			if !strings.Contains(out.String(), "model error: unexpected EOF") {
				t.Errorf("error absent from output: %q", out.String())
			}
		})
	}
}

func TestTransportFetchPartialIsNonproductiveObservation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "received fragment")
	}))
	defer srv.Close()
	reg := tool.NewRegistry()
	reg.Register(fetch.New(config.FetchConfig{TimeoutSeconds: 5, MaxBytes: 1024}))
	r := &Runner{Registry: reg}
	obs, sig, productive := r.handleCall(context.Background(), tool.Call{Name: "fetch", Slots: map[string]string{"url": srv.URL}}, "", Direct)
	if productive || sig == "" || !strings.Contains(obs, "tool error:") || !strings.Contains(obs, "unexpected EOF") || !strings.HasSuffix(obs, "partial tool result (failed; not complete):\nreceived fragment") {
		t.Errorf("partial fetch observation=%q signature=%q productive=%v", obs, sig, productive)
	}
	model := &scriptModel{replies: []string{"```tool\ntool: fetch\nurl: " + srv.URL + "\n```", "The fetch failed; only a fragment was received."}}
	sink := &recordSink{}
	r = &Runner{Registry: reg, Model: model, MaxSteps: 2, Session: sink}
	_, tx, err := r.Run(context.Background(), "fetch this page")
	if err != nil {
		t.Fatalf("normal next answer failed: %v", err)
	}
	if len(model.seen) != 2 || !strings.Contains(model.seen[1][len(model.seen[1])-1].Content, "partial tool result (failed; not complete):\nreceived fragment") {
		t.Errorf("model did not receive labeled failure: %+v", model.seen)
	}
	joined := ""
	for _, m := range tx {
		joined += m.Content
	}
	retained := false
	for i, kind := range sink.kinds {
		retained = retained || kind == "result" && strings.Contains(sink.texts[i], "partial tool result (failed; not complete):\nreceived fragment")
	}
	if !strings.Contains(joined, "received fragment") || !retained {
		t.Errorf("partial result absent from transcript/session: %+v %q", tx, sink.texts)
	}
}
