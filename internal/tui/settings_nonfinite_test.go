package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/llm"
)

func finiteSettingsSave(t *testing.T, m Model) []byte {
	t.Helper()
	if err := config.Save(m.CfgPath, m.Cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(m.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func selectSettingsFloat(t *testing.T, m Model, sel int) Model {
	t.Helper()
	m = submit(m, "/settings")
	for i := 0; i < sel; i++ {
		m = key(m, "down")
	}
	if m.Settings.Sel != sel {
		t.Fatal("selection did not advance via keys")
	}
	m = key(m, "enter")
	if m.Settings.Editing == nil {
		t.Fatal("Enter did not arm float edit")
	}
	return m
}

func TestSettingsNonFiniteArmedRefusal(t *testing.T) {
	for sel, f := range settingFields() {
		if f.Kind != "float" {
			continue
		}
		for _, bad := range []string{"NaN", "+Inf", "-Inf"} {
			t.Run(f.Section+"."+f.Key+"/"+bad, func(t *testing.T) {
				m := settingsModel(t)
				fileBefore := finiteSettingsSave(t, m)
				cfgBefore := m.Cfg
				serverBefore, modelBefore, windowBefore := m.Server, m.ModelName, m.St.CtxWindow
				m = selectSettingsFloat(t, m, sel)
				m = submit(m, bad)
				if m.Settings.Editing != nil {
					t.Error("refused edit remained armed")
				}
				if !reflect.DeepEqual(m.Cfg, cfgBefore) || m.Server != serverBefore || m.ModelName != modelBefore || m.St.CtxWindow != windowBefore {
					t.Error("refusal changed complete config or derived run state")
				}
				alert := lastAlert(m)
				if alert.Kind != "err" || !strings.Contains(alert.Text, f.Section+"."+f.Key) || !strings.Contains(alert.Text, "finite") {
					t.Errorf("refusal is not field-specific: %+v", alert)
				}
				raw, err := os.ReadFile(m.CfgPath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(raw, fileBefore) {
					t.Error("bad edit changed existing config file")
				}
				m = key(m, "s")
				raw, err = os.ReadFile(m.CfgPath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(raw, fileBefore) || !reflect.DeepEqual(m.Cfg, cfgBefore) {
					t.Error("save after refusal changed config/file bytes")
				}
				assertSettingsRequestEncodes(t, m.Cfg)
			})
		}
	}
}

func assertSettingsRequestEncodes(t *testing.T, cfg config.Config) {
	t.Helper()
	received := make(chan map[string]any, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Live Caliper discovery may GET any loopback listener; only inference belongs in this fixture.
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		var got map[string]any
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("configured request is not JSON: %v", err)
		}
		received <- got
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"fixture","choices":[{"message":{"content":"encoded"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	// This retention control selects the documented MLX sampler contract.
	cfg.Model.Runtime = llm.RuntimeMLX
	c := llm.New(srv.URL+"/v1", cfg.Model.Name).WithRuntime(cfg.Model.Runtime).WithParams(llm.Params{Temperature: cfg.Params.Temperature, TopK: cfg.Params.TopK, RepetitionPenalty: cfg.Params.RepetitionPenalty, Thinking: cfg.Params.Thinking})
	reply, _, _, err := c.Chat(context.Background(), []llm.Message{{Role: "user", Content: "encoding control"}})
	if err != nil || reply != "encoded" {
		t.Fatalf("retained config cannot encode subsequent request: reply=%q requests=%d err=%v", reply, len(received), err)
	}
	if len(received) != 1 {
		t.Fatalf("expected one configured request, got %d", len(received))
	}
	got := <-received
	if got["temperature"] != cfg.Params.Temperature || got["repetition_penalty"] != cfg.Params.RepetitionPenalty || got["chat_template_kwargs"].(map[string]any)["enable_thinking"] != cfg.Params.Thinking || got["top_k"] != float64(cfg.Params.TopK) {
		t.Errorf("request does not carry retained configured parameters: %+v", got)
	}
}

func TestSettingsNonFiniteSavePreservesFile(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run("temperature="+formatNonFinite(bad), func(t *testing.T) {
			m := settingsModel(t)
			before := finiteSettingsSave(t, m)
			m.Cfg.Params.Temperature = bad
			m = submit(m, "/settings")
			m = key(m, "s")
			alert := lastAlert(m)
			if alert.Kind != "err" || !strings.Contains(alert.Text, "params.temperature") || !strings.Contains(alert.Text, "finite") {
				t.Errorf("bad in-memory config save was not refused: %+v", alert)
			}
			after, err := os.ReadFile(m.CfgPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Error("bad save clobbered good file")
			}
		})
	}
}
func formatNonFinite(x float64) string {
	if math.IsNaN(x) {
		return "NaN"
	}
	if x > 0 {
		return "+Inf"
	}
	return "-Inf"
}

func TestSettingsFiniteBoundariesArmed(t *testing.T) {
	for sel, f := range settingFields() {
		if f.Kind != "float" {
			continue
		}
		for _, value := range []string{"0", "1.7976931348623157e+308"} {
			t.Run(f.Key+"="+value, func(t *testing.T) {
				m := settingsModel(t)
				m = selectSettingsFloat(t, m, sel)
				m = submit(m, value)
				if alert := lastAlert(m); alert.Kind != "info" {
					t.Fatalf("valid finite boundary refused: %+v", alert)
				}
				m = key(m, "s")
				back, used, err := config.Load(m.CfgPath)
				if err != nil || !used || !reflect.DeepEqual(back, m.Cfg) {
					t.Fatalf("finite setting did not stick: used=%v err=%v", used, err)
				}
				assertSettingsRequestEncodes(t, m.Cfg)
			})
		}
	}
}
