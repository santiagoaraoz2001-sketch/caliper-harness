package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/session"
)

func capturedThinkingCommand(t *testing.T) decision.Request {
	t.Helper()
	b, err := os.ReadFile("testdata/thinking-command-request.json")
	if err != nil {
		t.Fatal(err)
	}
	var req decision.Request
	if err = json.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	req.MaxBytes = 8192
	return req
}

// The selected Client is real; health/verdict HTTP replies are explicit doubles.
func thinkingSelectedClient(t *testing.T) (*decision.Client, *atomic.Int32, <-chan []byte) {
	t.Helper()
	path := "/Volumes/Cold Storage 1/SpecificHarnessModels/d1-3B-eval/models--LiquidAI--d1-3B/snapshots/051bcc464b01b9f92942b364d9586b0ef5912432"
	revision := "051bcc464b01b9f92942b364d9586b0ef5912432"
	sel := decision.Selection{Model: "LiquidAI/d1-3B", Runtime: "system_one", Device: "gpu", Path: &path, Revision: &revision, TrustRemoteCode: true}
	calls, bodies := &atomic.Int32{}, make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/health" {
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "pid": 1, "loaded": true, "ready": true, "loading": false, "model": sel.Model, "backends": []string{"gpu"}, "active": map[string]any{"model": sel.Model, "runtime": sel.Runtime, "source": "HTTP fixture", "path": path, "revision": revision, "device": "gpu", "native_device": "mps:0", "architecture": "fixture", "parameters": 1, "selection": sel}})
			return
		}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies <- b
		json.NewEncoder(w).Encode(map[string]any{"pick": 0, "option": loopStuck, "confidence": .9, "model": sel.Model, "device": "gpu"})
	}))
	t.Cleanup(srv.Close)
	return decision.New(srv.URL, sel.Model, sel.Device, time.Second).WithSelection(sel), calls, bodies
}

func TestThinkingCapturedCommandSelectedBody(t *testing.T) {
	old := capturedThinkingCommand(t)
	encoded, err := json.Marshal(old)
	if err != nil || len(encoded) != 7895 {
		t.Fatalf("captured log: %d %v", len(encoded), err)
	}
	client, calls, bodies := thinkingSelectedClient(t)
	if _, err = client.Judge(context.Background(), old); err == nil || !strings.Contains(err.Error(), "8231 exceed limit 8192") || calls.Load() != 0 {
		t.Fatalf("original selected body admission: %v HTTP=%d", err, calls.Load())
	}
	metadata, _, _ := strings.Cut(old.Context, "\nUnit:\n")
	var facts struct {
		OriginalUserIntent string
		Coding             *session.CodingState
	}
	if err = json.Unmarshal([]byte(metadata), &facts); err != nil {
		t.Fatal(err)
	}
	r := thinkingRunner(client.Judge)
	r.Assigned = &AssignedOps{}
	// Exercise the capture's planning label in an explicitly selected component
	// phase. AssignedOps alone no longer implies planning; its negative control
	// is TestConsistencyThinkingPhaseUsesSelectedPlanningAndClaim.
	r.Continuation.Workflow = &session.WorkflowScope{Store: "component-owner", Kind: "root"}
	r.Continuation.Coding = facts.Coding
	r.Continuation.Entries = []session.Entry{{Kind: "intent", Message: llm.Message{Role: "user", Content: facts.OriginalUserIntent}}}
	before, _ := json.Marshal(r.Continuation)
	var actual decision.Request
	r.Judge = func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
		actual = req
		return client.Judge(ctx, req)
	}
	sample := readThinkingEvidence(t, old.Context).Sample
	_, err = thinkingFixture(t, r, [][2]string{{"reasoning", sample}}, true, nil)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, thinkingLoopStop) || calls.Load() != 2 {
		t.Fatalf("guard failed selected Client admission: %v HTTP=%d", err, calls.Load())
	}
	body := <-bodies
	if len(body) > 8192 {
		t.Fatalf("actual body %d", len(body))
	}
	after, _ := json.Marshal(r.Continuation)
	if string(before) != string(after) || r.Continuation.Coding != facts.Coding {
		t.Fatal("owning continuation mutated")
	}
	origMeta, origValues, _ := strings.Cut(old.Context, "\nUnit:\n")
	newMeta, newValues, _ := strings.Cut(actual.Context, "\nUnit:\n")
	var origMap, newMap map[string]any
	json.Unmarshal([]byte(origMeta), &origMap)
	json.Unmarshal([]byte(newMeta), &newMap)
	if newMap["ConfiguredCommandSameAsCommand"] != true {
		t.Fatal("missing explicit equality")
	}
	delete(newMap, "ConfiguredCommandSameAsCommand")
	coding := newMap["Coding"].(map[string]any)
	if _, present := coding["config_command"]; present {
		t.Fatal("duplicate command retained")
	}
	coding["config_command"] = coding["command"]
	if !reflect.DeepEqual(origMap, newMap) || origValues != newValues || !strings.Contains(actual.Instructions, "ConfiguredCommandSameAsCommand=true") {
		t.Fatal("lossy evidence reconstruction")
	}
	t.Logf("original logged7895/selected8231; current selected%d; all factual metadata and exact Unit/Sample reconstructed", len(body))
}

func TestThinkingCommandViewOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, command, configured string
		equal                     bool
	}{{"equal", "echo actual", "echo actual", true}, {"unequal", "echo actual", "echo configured", false}, {"noConfig", "echo actual", "", false}, {"blank", "   ", "   ", false}} {
		t.Run(tc.name, func(t *testing.T) {
			state := &session.CodingState{TaskID: "actual", Workspace: "/fixture", Command: tc.command, ConfigCommand: tc.configured, Revision: 2, Receipt: &session.CheckReceipt{Command: tc.command, Status: "failed"}}
			before, _ := json.Marshal(state)
			r := thinkingRunner(func(ctx context.Context, req decision.Request) (decision.Verdict, error) {
				meta, _, _ := strings.Cut(req.Context, "\nUnit:\n")
				var v struct {
					Coding                         *session.CodingState
					ConfiguredCommandSameAsCommand bool
				}
				json.Unmarshal([]byte(meta), &v)
				if v.ConfiguredCommandSameAsCommand != tc.equal {
					t.Fatalf("equality %+v", v)
				}
				if tc.equal {
					v.Coding.ConfigCommand = v.Coding.Command
				}
				b, _ := json.Marshal(v.Coding)
				if string(b) != string(before) {
					t.Fatalf("changed facts %s", b)
				}
				return decision.Verdict{Pick: 0, Option: loopStuck, Confidence: .9}, nil
			})
			r.Continuation.Coding = state
			_, err := thinkingFixture(t, r, [][2]string{{"reasoning", strings.Repeat("abcdefgh", 16)}}, true, nil)
			after, _ := json.Marshal(state)
			if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, thinkingLoopStop) || string(before) != string(after) || r.Continuation.Coding != state {
				t.Fatalf("owning state/stop %v", err)
			}
		})
	}
	r := thinkingRunner(decision.New("http://unused.invalid:1", "configured-model", decision.DeviceCPU, time.Second).Judge)
	command := strings.Repeat("actual immutable command", 1000)
	r.Continuation.Coding = &session.CodingState{Command: command, ConfigCommand: command}
	_, err := thinkingFixture(t, r, [][2]string{{"reasoning", strings.Repeat("abcdefgh", 16)}}, true, nil)
	if !errors.Is(err, thinkingLoopStop) || !strings.Contains(fmt.Sprint(err), "complete request bytes") {
		t.Fatalf("immutable overflow %v", err)
	}
}
