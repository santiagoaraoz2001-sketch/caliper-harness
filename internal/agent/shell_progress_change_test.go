package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"specific-harness/internal/prompts"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

// The executor is scripted; workspace fingerprints and progress packets use
// their production owners. An observed persistent effect is independent of exit.
func TestShellObservedChangesReachProgress(t *testing.T) {
	commandErr := errors.New("command failed after writing")
	for _, tc := range []struct {
		name                        string
		beforeMissing, afterMissing bool
		write, inputChanged, failed bool
		err                         error
		wantChanged, wantStale      bool
	}{
		{name: "changed", write: true, wantChanged: true, wantStale: true},
		{name: "unchanged"},
		{name: "already observed", inputChanged: true, wantChanged: true},
		{name: "failed exit with effects", write: true, failed: true, wantChanged: true, wantStale: true},
		{name: "error with effects", write: true, err: commandErr, wantChanged: true, wantStale: true},
		{name: "before unavailable", beforeMissing: true, write: true, wantStale: true},
		{name: "after unavailable", afterMissing: true, wantStale: true},
		{name: "both unavailable", beforeMissing: true, afterMissing: true, wantStale: true},
		{name: "unavailable retains observed", beforeMissing: true, afterMissing: true, inputChanged: true, wantChanged: true, wantStale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project")
			if !tc.beforeMissing {
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			c := &session.CodingState{TaskID: "ordinary", Workspace: root, Revision: 1, Receipt: &session.CheckReceipt{Status: "passed"}}
			r := &Runner{Workspace: root, Prompts: prompts.Builtin(), Session: &recordSink{}, SessionID: "observed-effects", Continuation: session.Continuation{Coding: c}}
			code := 0
			if tc.failed {
				code = 7
			}
			exec := r.assignmentExec(r.CodingExec(func(context.Context, tool.Definition, tool.Args) (tool.Result, error) {
				if tc.write {
					if err := os.MkdirAll(root, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(root, "actual.txt"), []byte("persistent effect"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if tc.afterMissing {
					if err := os.RemoveAll(root); err != nil {
						t.Fatal(err)
					}
				}
				return tool.Result{Text: "actual command receipt", Changed: tc.inputChanged, Failed: tc.failed, ExitCode: &code}, tc.err
			}))
			res, err := exec(t.Context(), tool.Definition{Name: "bash"}, tool.Args{"cmd": "scripted command"})
			if !errors.Is(err, tc.err) || res.Changed != tc.wantChanged || res.Failed != tc.failed || res.ExitCode == nil || *res.ExitCode != code {
				t.Fatalf("observed result lost: %+v err=%v", res, err)
			}
			wantRevision := 1
			if tc.wantStale {
				wantRevision++
			}
			if c.Revision != wantRevision || (c.Receipt.Status == "stale") != tc.wantStale {
				t.Fatalf("existing invalidation changed: %+v", c)
			}
			r.enforcement.current.CallID = "actual-shell-call"
			w := &watcher{}
			w.discoveryReceipt(r.enforcement.current, 5)
			packet, err := r.progressEvidence(t.Context(), w)
			if err != nil || packet.Latest == nil || packet.LatestSettled == nil || len(packet.RecentReceipts) != 1 {
				t.Fatalf("missing actual progress evidence: %+v err=%v", packet, err)
			}
			for _, receipt := range []*loopObservation{packet.Latest, packet.LatestSettled, packet.RecentReceipts[0]} {
				if receipt.Changed != tc.wantChanged || receipt.CallID != "actual-shell-call" || receipt.Failed != tc.failed || receipt.ExitCode == nil || *receipt.ExitCode != code {
					t.Fatalf("progress receipt differs from execution: %+v", receipt)
				}
			}
		})
	}
}
