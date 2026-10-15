package tui

import (
	"errors"
	"strings"
	"testing"
)

func TestBehaviorWorkflowNoticeSettlement(t *testing.T) {
	for _, err := range []error{nil, errors.New("workflow refused")} {
		m := New(testCfg(), "test")
		v, _ := m.Update(wfDoneMsg{approach: "fixture", text: "counts", note: "note: behavior capped", err: err})
		m = v.(Model)
		if len(m.Alerts) != 1 || !strings.Contains(m.Alerts[0].Text, "note: behavior capped") {
			t.Fatal("notice discarded", m.Alerts)
		}
		if err != nil && !strings.Contains(m.Alerts[0].Text, err.Error()) {
			t.Fatal("failure hidden", m.Alerts)
		}
		if strings.Contains(m.Input.Value(), "behavior capped") {
			t.Fatal("notice became prompt")
		}
	}
}
