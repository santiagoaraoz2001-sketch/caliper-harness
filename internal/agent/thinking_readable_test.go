package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"specific-harness/internal/decision"
)

func TestThinkingLoopReadableJudgeTransport(t *testing.T) {
	unit := "\n<|channel>thought"
	thought := strings.Repeat(unit, 16)
	bodies := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		bodies <- body
		fmt.Fprintf(w, `{"pick":0,"option":%q,"confidence":0.9,"model":"fixture","device":"cpu"}`, loopStuck)
	}))
	defer srv.Close()
	r := thinkingRunner(decision.New(srv.URL, "fixture", decision.DeviceCPU, time.Second).Judge)
	var recorded decision.Request
	reply, err := thinkingFixture(t, r, [][2]string{{"reasoning", thought[:11]}, {"reasoning", thought[11:]}}, true, func(kind, text string) error {
		if kind == "thinking_loop" {
			return json.Unmarshal([]byte(text), &recorded)
		}
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, thinkingLoopStop) || reply.Message.Reasoning != thought || reply.ThinkingOnly {
		t.Fatalf("partial transport: %+v %v", reply, err)
	}
	var body struct {
		Context, Instructions string
		Options               []string
	}
	raw := <-bodies
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	// This is what the sidecar receives after decoding its HTTP envelope once.
	if len(raw) > 8192 || !strings.Contains(body.Context, thought) || strings.Count(body.Context, thought) != 1 || strings.Contains(body.Context, `\u003c|channel\u003ethought`) || !strings.Contains(body.Context, fmt.Sprintf("%x", sha256.Sum256([]byte(thought)))) {
		t.Fatalf("judge did not receive single-copy readable exact sample: %s", body.Context)
	}
	if len(body.Options) != 2 || body.Options[0] != loopStuck || body.Options[1] != loopProgress {
		t.Fatalf("decision choices changed: %v", body.Options)
	}
	evidence := readThinkingEvidence(t, body.Context)
	if recorded.Context != body.Context || evidence.Unit != unit || evidence.Sample != thought || evidence.UnitEncoding != "utf8" || evidence.SampleEncoding != "utf8" || evidence.ObservedBytes != len(thought) || evidence.OmittedBytes != 0 || evidence.Repeat != 16 {
		t.Fatalf("durable/native evidence mismatch: %+v", evidence)
	}
}
