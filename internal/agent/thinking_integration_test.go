package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/tool"
)

func TestThinkingLoopRunnerArchiveAndSummary(t *testing.T) {
	for _, mode := range []string{"main", "uncertain", "cancel", "summary"} {
		t.Run(mode, func(t *testing.T) {
			thought := strings.Repeat("<|channel>thought", 16)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				delta := map[string]any{"reasoning": thought, "tool_calls": []any{map[string]any{"index": 0, "id": "partial", "type": "function", "function": map[string]string{"name": "echo", "arguments": "{\"msg\":\""}}}}
				if mode == "summary" {
					delete(delta, "tool_calls")
				}
				b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
				fmt.Fprintf(w, "data: %s\n\n", b)
				w.(http.Flusher).Flush()
				<-req.Context().Done()
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			judged := 0
			r := thinkingRunner(func(context.Context, decision.Request) (decision.Verdict, error) {
				judged++
				if mode == "cancel" {
					cancel()
				}
				if mode == "uncertain" {
					return decision.Verdict{Pick: 1, Option: loopProgress, Confidence: .3}, nil
				}
				return decision.Verdict{Pick: 0, Option: loopStuck, Confidence: .9}, nil
			})
			sink := &recordSink{}
			var calls []string
			r.Registry = tool.NewRegistry()
			r.Registry.Register(echoTool(&calls))
			r.Model, r.Session, r.SessionID = llm.New(srv.URL, "fixture"), sink, "loop"
			r.MaxSteps, r.MaxTokens = 2, 32768
			if mode == "summary" {
				original := truthFixture()
				r.Compaction, r.TemplatesDir = testCompaction(), testTemplateDir(t)
				r.Compaction.Keep = 0
				next, _, ran, err := r.Compact(ctx, original)
				if err == nil || ran || !reflect.DeepEqual(next, original) {
					t.Fatalf("looping summary replaced context: ran%t err%v", ran, err)
				}
			} else {
				_, _, err := r.Run(ctx, "repair the actual project")
				wantErr := context.DeadlineExceeded
				if mode == "cancel" {
					wantErr = context.Canceled
				}
				if r.Outcome.Status != outcome.Cancelled || !errors.Is(err, wantErr) || errors.Is(err, thinkingLoopStop) {
					t.Fatalf("advisory verdict overrode actual caller cancellation: outcome%+v err%v", r.Outcome, err)
				}
			}
			raw := ""
			for i, kind := range sink.kinds {
				if kind == "reasoning" && strings.HasPrefix(sink.texts[i], "offset=") {
					_, fragment, _ := strings.Cut(sink.texts[i], "\n")
					raw += fragment
				}
				if kind == "tool" || kind == "native_result" || kind == "native_reply" || kind == "done" || kind == "compact" {
					t.Fatalf("partial generation became completed/effectful: %s", kind)
				}
			}
			var archived llm.Reply
			if mode == "summary" {
				if err := json.Unmarshal([]byte(raw), &archived); err != nil {
					t.Fatal(err)
				}
			} else {
				var envelope struct{ Reply llm.Reply }
				if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
					t.Fatal(err)
				}
				archived = envelope.Reply
			}
			callsRetained := len(archived.Message.ToolCalls) == 1 && archived.Message.ToolCalls[0].ID == "partial" && archived.Message.ToolCalls[0].Arguments == "{\"msg\":\""
			if mode == "summary" {
				callsRetained = len(archived.Message.ToolCalls) == 0
			}
			if judged != 1 || len(calls) != 0 || archived.Message.Reasoning != thought || !callsRetained {
				t.Fatalf("guard/partial archive missing: judged%d effects%d raw%q", judged, len(calls), raw)
			}
		})
	}
}
