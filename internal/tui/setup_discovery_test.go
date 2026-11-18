package tui

import (
	"context"
	"net/http"
	"os"
	"specific-harness/internal/serve"
	"strings"
	"testing"
)

// First use in Setup finds model servers already running on this
// computer. Detection stages a choice only; nothing is saved or
// assigned until the user reviews and saves.
func TestSetupDiscoveryStagesSingleDetectedWorkerWithoutWriting(t *testing.T) {
	m := setupFixture(t, 100, 30)
	m.Serve.Rows = nil
	m.Cfg.Model.Name = ""
	m.Cfg.Serve.Models = nil
	m.Cfg.Serve.Recipes = nil
	m.Setup.Pick = ""
	m.Setup.Model.Name = ""
	discover := func(context.Context, *http.Client) ([]serve.DiscoveredServer, error) {
		return []serve.DiscoveredServer{
			{URL: "http://127.0.0.1:11434", Kind: serve.KindOllama, Health: serve.Health{Up: true, Kind: serve.KindOllama, APIBase: "http://127.0.0.1:11434/v1", Available: []string{"detected-model"}}},
			{URL: "http://127.0.0.1:8090", Kind: serve.KindSidecar, Health: serve.Health{Up: true, Kind: serve.KindSidecar, Runtime: "system_one", Available: []string{"detected-judge"}}},
		}, nil
	}
	msg := m.probeServeWithDiscovery(&http.Client{Transport: offlineTransport{}}, discover)().(serveProbedMsg)
	m.onServeProbed(msg)

	var detected []setupChoice
	for _, c := range m.setupChoices() {
		if c.detected {
			detected = append(detected, c)
		}
	}
	if len(detected) != 1 || detected[0].label != "detected-model" || !strings.Contains(detected[0].state, "detected on this computer") {
		t.Fatalf("detected worker choices: %+v", detected)
	}
	if m.Setup.Pick != detected[0].key || m.Setup.Model.Name != "detected-model" || m.Setup.Model.Runtime != "ollama" || m.Setup.Model.Server != "http://127.0.0.1:11434/v1" {
		t.Fatalf("single detected worker not staged: pick=%q model=%+v", m.Setup.Pick, m.Setup.Model)
	}
	if m.Setup.Judge.Model != "detected-judge" || m.Setup.Judge.Server != "http://127.0.0.1:8090" || m.Setup.Judge.Runtime != "system_one" || m.Setup.JudgePick != "native:http://127.0.0.1:8090:detected-judge" {
		t.Fatalf("single detected judge not staged: pick=%q judge=%+v", m.Setup.JudgePick, m.Setup.Judge)
	}
	if m.Cfg.Model.Name != "" {
		t.Fatalf("detection assigned the live worker: %+v", m.Cfg.Model)
	}
	if m.Cfg.Decision.Model == "detected-judge" {
		t.Fatalf("detection assigned the live judge: %+v", m.Cfg.Decision)
	}
	if _, err := os.Stat(m.CfgPath); !os.IsNotExist(err) {
		t.Fatal("detection wrote configuration")
	}

	m.Setup.Stage = 1
	foundJudge := false
	for _, c := range m.currentSetupChoices() {
		if c.label == "detected-judge" && c.judge != nil && c.judge.Server == "http://127.0.0.1:8090" && c.judge.Runtime == "system_one" {
			foundJudge = true
		}
	}
	if !foundJudge {
		t.Fatal("detected decision service missing from judge choices")
	}
}
