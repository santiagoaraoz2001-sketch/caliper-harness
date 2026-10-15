package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApprovedBehaviorLimitsYAML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg.yaml")
	os.WriteFile(p, []byte("{}\n"), 0600)
	defaults, _, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = Save(p, defaults); err != nil {
		t.Fatal(err)
	}
	initial, _ := os.ReadFile(p)
	for _, key := range []string{"max_file_entries: 500", "max_file_view_lines: 2000", "max_run_ids: 20", "max_decision_rows: 100", "max_alerts: 50", "max_legacy_messages: 100", "title_timeout_seconds: 20", "plain_request_timeout_seconds: 300", "probe_timeout_seconds: 3"} {
		if !strings.Contains(string(initial), key) {
			t.Error("omitted default: " + key)
		}
	}
	text := "ui:\n  max_file_entries: 2\n  max_file_view_lines: 3\n  max_run_ids: 2\n  max_decision_rows: 2\n  max_alerts: 2\n  max_legacy_messages: 2\ntabs:\n  title_timeout_seconds: 1\nmodel:\n  plain_request_timeout_seconds: 1\nserve:\n  probe_timeout_seconds: 1\n"
	os.WriteFile(p, []byte(text), 0600)
	c, _, e := Load(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = Save(p, c); e != nil {
		t.Fatal(e)
	}
	saved, _ := os.ReadFile(p)
	for _, key := range []string{"max_file_entries: 2", "max_file_view_lines: 3", "max_run_ids: 2", "max_decision_rows: 2", "max_alerts: 2", "max_legacy_messages: 2", "title_timeout_seconds: 1", "plain_request_timeout_seconds: 1", "probe_timeout_seconds: 1"} {
		if !strings.Contains(string(saved), key) {
			t.Error(key)
		}
	}
	for _, key := range []string{"max_file_entries", "max_file_view_lines", "max_run_ids", "max_decision_rows", "max_alerts", "max_legacy_messages", "title_timeout_seconds", "plain_request_timeout_seconds", "probe_timeout_seconds"} {
		bad := strings.Replace(text, key+": "+map[string]string{"max_file_entries": "2", "max_file_view_lines": "3", "max_run_ids": "2", "max_decision_rows": "2", "max_alerts": "2", "max_legacy_messages": "2", "title_timeout_seconds": "1", "plain_request_timeout_seconds": "1", "probe_timeout_seconds": "1"}[key], key+": 0", 1)
		os.WriteFile(p, []byte(bad), 0600)
		if _, _, e = Load(p); e == nil || !strings.Contains(e.Error(), key) {
			t.Errorf("%s error=%v", key, e)
		}
	}
	for _, value := range []string{"-1", "9223372037"} {
		os.WriteFile(p, []byte("model:\n  plain_request_timeout_seconds: "+value+"\n"), 0600)
		if _, _, e = Load(p); e == nil || !strings.Contains(e.Error(), "plain_request_timeout_seconds") {
			t.Fatalf("invalid duration %s: %v", value, e)
		}
	}
}
