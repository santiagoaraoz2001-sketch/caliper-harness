package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"specific-harness/internal/config"
	appupdate "specific-harness/internal/update"
)

func TestPreparedUpdateKeepsNewerExternalSelection(t *testing.T) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	manager := appupdate.Manager{Home: t.TempDir(), Platform: platform}
	selected := appupdate.Selection{Version: "0.1.4", Platform: platform, Root: filepath.Join(manager.Home, "versions", "0.1.4", platform, "Caliper")}
	files := []string{"caliper-harness", "caliper.exe", "prompts.yaml", "MANIFEST.sha256", "runtime/decision/requirements.lock", "lib/libkuzu.dylib", "lib/libkuzu.so", "kuzu_shared.dll", "bin/rg", "bin/rg.exe", "runtime/decision/bin/python3", "runtime/decision/python.exe", "runtime/worker/bin/python3"}
	for _, name := range files {
		path := filepath.Join(selected.Root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest, _ := json.Marshal(map[string]any{"format": 1, "platform": platform})
	if err := os.WriteFile(filepath.Join(selected.Root, "runtime/manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Activate(&selected); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(manager.Home, "current.json"))
	m := New(config.Defaults(), "0.1.2")
	m.CfgPath = filepath.Join(t.TempDir(), "missing", "must-not-write.yaml")
	m.updatePrepared(updatePreparedMsg{manager: manager, selection: &appupdate.Selection{Version: "0.1.3"}})
	after, _ := os.ReadFile(filepath.Join(manager.Home, "current.json"))
	if string(before) != string(after) {
		t.Fatal("older download replaced newer selected version")
	}
	if _, err := os.Stat(m.CfgPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("obsolete update touched configuration")
	}
}

func TestUpdateStartupDeclineDoesNotDownload(t *testing.T) {
	m := New(config.Defaults(), "0.1.2")
	cmd := m.updateChecked(updateCheckedMsg{release: &appupdate.Release{Version: "0.1.3", ArchiveURL: "http://must-not-contact"}})
	if cmd == nil || m.Chat.Dialog == nil || !strings.Contains(m.Chat.Dialog.Prompt, "[y/N]") {
		t.Fatal("missing update consent")
	}
	m.answerDialog("n")
	result := cmd().(updateFinishedMsg)
	if !result.declined || result.err != nil {
		t.Fatalf("decline %+v", result)
	}
	m.updateFinished(result)
	if m.Updater.busy {
		t.Fatal("decline left updater busy")
	}
}
func TestUpdateStartupNeverReplacesWorkerDialog(t *testing.T) {
	m := New(config.Defaults(), "0.1.2")
	m.Chat.Running = true
	old := &Dialog{Kind: "auth"}
	m.Chat.Dialog = old
	if cmd := m.updateChecked(updateCheckedMsg{release: &appupdate.Release{Version: "0.1.3"}}); cmd != nil {
		t.Fatal("interrupted work with update")
	}
	if m.Chat.Dialog != old || !m.Chat.Running {
		t.Fatal("changed live worker")
	}
}
func TestExplicitUpdateCanStageDuringRun(t *testing.T) {
	m := New(config.Defaults(), "0.1.2")
	m.Chat.Running = true
	cmd := m.updateChecked(updateCheckedMsg{explicit: true, release: &appupdate.Release{Version: "0.1.3"}})
	if cmd == nil || !m.Updater.busy || !m.Chat.Running || m.Chat.Dialog != nil {
		t.Fatal("explicit update blocked active run")
	}
	m.Updater.cancel()
	result := cmd().(updateFinishedMsg)
	if result.err == nil {
		t.Fatal("cancelled update unexpectedly activated")
	}
}
func TestUpdateCheckFailureDoesNotStopWorker(t *testing.T) {
	m := New(config.Defaults(), "0.1.2")
	m.Chat.Running = true
	if cmd := m.updateChecked(updateCheckedMsg{explicit: true, err: errors.New("network unavailable")}); cmd != nil {
		t.Fatal("unexpected command")
	}
	if !m.Chat.Running || m.Updater.busy {
		t.Fatal("update check changed worker")
	}
}
