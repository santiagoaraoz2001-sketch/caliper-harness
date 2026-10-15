package config

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Reflection is confined to tests: any new float config field joins the
// refusal matrix automatically, including each confidence-bucket position.
type configFloatSlot struct {
	name    string
	fields  []int
	element int
}

func configFloatSlots() []configFloatSlot {
	var slots []configFloatSlot
	var visit func(reflect.Value, string, []int)
	visit = func(v reflect.Value, prefix string, path []int) {
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			name := strings.Split(f.Tag.Get("yaml"), ",")[0]
			if prefix != "" {
				name = prefix + "." + name
			}
			next := append(append([]int(nil), path...), i)
			fv := v.Field(i)
			switch fv.Kind() {
			case reflect.Struct:
				visit(fv, name, next)
			case reflect.Float32, reflect.Float64:
				slots = append(slots, configFloatSlot{name, next, -1})
			case reflect.Slice:
				if fv.Type().Elem().Kind() == reflect.Float32 || fv.Type().Elem().Kind() == reflect.Float64 {
					for j := 0; j < max(1, fv.Len()); j++ {
						slots = append(slots, configFloatSlot{fmt.Sprintf("%s[%d]", name, j), next, j})
					}
				}
			}
		}
	}
	visit(reflect.ValueOf(Defaults()), "", nil)
	return slots
}
func (s configFloatSlot) set(c *Config, x float64) {
	v := reflect.ValueOf(c).Elem()
	for _, i := range s.fields {
		v = v.Field(i)
	}
	if s.element >= 0 {
		if v.Len() == 0 {
			v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		}
		v = v.Index(s.element)
	}
	v.SetFloat(x)
}
func configYAML(t *testing.T, c Config) []byte {
	t.Helper()
	raw, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func requireFiniteRefusal(t *testing.T, err error, name string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "finite") {
		t.Errorf("want finite refusal naming %s, got %v", name, err)
	}
}

func TestNonFiniteEveryConfigFloat(t *testing.T) {
	slots := configFloatSlots()
	if len(slots) == 0 {
		t.Fatal("probe discovered no float fields")
	}
	for _, s := range slots {
		for _, bad := range []struct {
			name  string
			value float64
		}{{"NaN", math.NaN()}, {"+Inf", math.Inf(1)}, {"-Inf", math.Inf(-1)}} {
			t.Run(s.name+"/"+bad.name, func(t *testing.T) {
				cfg := Defaults()
				s.set(&cfg, bad.value)
				before := configYAML(t, cfg)
				requireFiniteRefusal(t, cfg.Validate(), s.name)
				if !bytes.Equal(before, configYAML(t, cfg)) {
					t.Error("Validate changed its complete input config")
				}
				dir := t.TempDir()
				existing := filepath.Join(dir, "existing.yaml")
				sentinel := []byte("prior file bytes\n")
				if err := os.WriteFile(existing, sentinel, 0600); err != nil {
					t.Fatal(err)
				}
				requireFiniteRefusal(t, Save(existing, cfg), s.name)
				got, err := os.ReadFile(existing)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, sentinel) || !bytes.Equal(before, configYAML(t, cfg)) {
					t.Error("Save changed existing file or complete input config")
				}
				missing := filepath.Join(dir, "missing.yaml")
				requireFiniteRefusal(t, Save(missing, cfg), s.name)
				if _, err := os.Stat(missing); !os.IsNotExist(err) {
					t.Errorf("bad Save created a file: %v", err)
				}
				input := filepath.Join(dir, "input.yaml")
				if err := os.WriteFile(input, before, 0600); err != nil {
					t.Fatal(err)
				}
				loaded, used, err := Load(input)
				requireFiniteRefusal(t, err, s.name)
				if used || !reflect.DeepEqual(loaded, Config{}) {
					t.Error("failed Load returned an admitted config")
				}
				got, err = os.ReadFile(input)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, before) {
					t.Error("Load changed bad input file")
				}
			})
		}
	}
}

func TestFiniteDomainBoundariesRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		set  func(*Config)
	}{
		{"zero percentages", func(c *Config) {
			c.Context.Trigger = 0
			c.Context.Keep = 0
			c.Context.SummaryStep = 0
			c.Context.InflightOutputs = 0
			c.Schemas.InflightCap = 0
		}},
		{"hundred percentages", func(c *Config) {
			c.Context.Trigger = 100
			c.Context.Keep = 100
			c.Context.Summary = 100
			c.Context.SummaryStep = 100
			c.Context.SummaryMax = 100
			c.Context.InflightOutputs = 100
			c.Schemas.InflightCap = 100
		}},
		{"small positive fields", func(c *Config) {
			c.Context.Summary = math.SmallestNonzeroFloat64
			c.Context.SummaryMax = c.Context.Summary
			c.Context.SummaryWordsPerToken = math.SmallestNonzeroFloat64
			c.Behavior.CharsPerToken = math.SmallestNonzeroFloat64
		}},
		{"zero generation and threshold", func(c *Config) {
			c.Params.Temperature = 0
			c.Params.RepetitionPenalty = 0
			c.Decision.RiskThreshold = 0
		}},
		{"large finite unbounded fields", func(c *Config) {
			c.Context.SummaryWordsPerToken = math.MaxFloat64
			c.Behavior.CharsPerToken = math.MaxFloat64
			c.Params.Temperature = math.MaxFloat64
			c.Params.RepetitionPenalty = math.MaxFloat64
			c.Decision.RiskThreshold = 1
		}},
		{"exclusive bucket edges", func(c *Config) {
			c.Decision.ConfidenceBuckets = []float64{math.SmallestNonzeroFloat64, math.Nextafter(1, 0)}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			tc.set(&cfg)
			if err := cfg.Validate(); err != nil {
				t.Fatalf("finite valid boundary refused: %v", err)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := Save(path, cfg); err != nil {
				t.Fatal(err)
			}
			loaded, used, err := Load(path)
			if err != nil || !used || !reflect.DeepEqual(cfg, loaded) {
				t.Fatalf("finite boundary did not survive roundtrip: used=%v err=%v", used, err)
			}
		})
	}
}
