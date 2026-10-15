package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContinuationNativeSnapshotRawTailAndBounds(t *testing.T) {
	dir := t.TempDir()
	id := "owned-context"
	c := Continuation{Version: 1, Measured: true, Prompt: 200, BasisTokens: 200, BasisChars: 900, ModelKey: "old-runtime"}
	c.Add("intent", "user", "ORIGINAL", false)
	c.Add("summary", "user", "Prior context summary:\nBOUNDED", false)
	c.Add("failure", "user", "actual check exit5; NO TESTS RAN", false)
	encoded, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Event{{Kind: "prompt", Text: "OLD_DISCARDED"}, {Kind: "continuation", Text: encoded}, {Kind: "continuation", Text: `{"version":99}`}, {Kind: "error", Text: "actual transport EOF"}} {
		if err = Append(dir, id, e); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events, err := Read(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	restored, notice, err := Restore(events, 10000, "old-runtime")
	if err != nil || !strings.Contains(notice, "invalid snapshot") || restored.Measured || restored.BasisChars != 900 {
		t.Fatalf("restore/unknown%+v %q %v", restored, notice, err)
	}
	text := ""
	for _, m := range restored.Messages() {
		text += m.Content
	}
	for _, a := range []string{"ORIGINAL", "BOUNDED", "NO TESTS RAN", "exit5", "actual transport EOF"} {
		if !strings.Contains(text, a) {
			t.Errorf("lost fact%q", a)
		}
	}
	if strings.Contains(text, "OLD_DISCARDED") {
		t.Fatal("old turns resurrected")
	}
	after, _ := os.ReadFile(filepath.Join(dir, id+".jsonl"))
	if string(after) != string(raw) {
		t.Fatal("restore changed raw events")
	}
	if changed, _, err := Restore(events, 10000, "changed-runtime"); err != nil || changed.BasisChars != 0 {
		t.Fatal("runtime change kept calibration")
	}
	if pressure, notice, err := Restore(events, 2); err != nil || pressure.Estimate() <= 2 || !strings.Contains(notice, "retained history") {
		t.Fatal("protected oversize context must remain addressable with pressure disclosure", err, notice)
	}
	huge := c.Clone()
	huge.Entries[0].Message.Content = strings.Repeat("\"\\", 180000)
	large, err := huge.Encode()
	if err != nil {
		t.Fatal("valid escaped continuation rejected", err)
	}
	reopened, _, err := Restore([]Event{{Kind: "continuation", Text: large}}, 0)
	if err != nil || reopened.Entries[0].Message.Content != huge.Entries[0].Message.Content {
		t.Fatal("escaped continuation changed", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, id+".jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("not JSON\n")
	f.Close()
	if _, err = Read(dir, id); err == nil {
		t.Fatal("corrupt raw evidence silently skipped")
	}
}

func TestContinuationReconstructedCompactReplacesOptionalMiddle(t *testing.T) {
	base := Continuation{Version: 1}
	base.Add("intent", "user", "ORIGINAL", false)
	base.Add("reply", "assistant", "DISCARDED_BEFORE_SNAPSHOT", false)
	encoded, err := base.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "snapshot-tail"}[snapshot], func(t *testing.T) {
			events := []Event{{Kind: "prompt", Text: "ORIGINAL"}, {Kind: "reply", Text: "DISCARDED_BEFORE_SNAPSHOT"}}
			if snapshot {
				events = []Event{{Kind: "continuation", Text: encoded}}
			}
			events = append(events, Event{Kind: "result", Text: "actual check exit5; NO TESTS RAN"}, Event{Kind: "btw", Text: "KEEP_NOTE"}, Event{Kind: "compact", Text: "SUMMARY_1"}, Event{Kind: "prompt", Text: "MID_TASK"}, Event{Kind: "reply", Text: "DISCARDED_MIDDLE"}, Event{Kind: "compact", Text: "SUMMARY_2"}, Event{Kind: "prompt", Text: "CURRENT_TASK"})
			c, notice, err := Restore(events, 10000)
			if err != nil {
				t.Fatal(err)
			}
			text, summaries := "", 0
			for _, e := range c.Entries {
				text += e.Message.Content
				if e.Kind == "summary" {
					summaries++
				}
			}
			for _, a := range []string{"ORIGINAL", "CURRENT_TASK", "KEEP_NOTE", "exit5", "NO TESTS RAN", "SUMMARY_2"} {
				if !strings.Contains(text, a) {
					t.Errorf("lost protected fact %q", a)
				}
			}
			if summaries != 1 || strings.Contains(text, "DISCARDED_") || strings.Contains(text, "SUMMARY_1") {
				t.Fatalf("old compacted turns resurrected: summaries %d %s", summaries, text)
			}
			if !strings.Contains(notice, "conservative compact reconstruction") {
				t.Fatalf("unlabelled historical boundary estimate %q", notice)
			}
		})
	}
}
