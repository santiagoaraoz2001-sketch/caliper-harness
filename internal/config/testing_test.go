package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleTestSuite() TestSuite {
	return TestSuite{ID: "save", Name: "Save reload", Purpose: "Purchased levels survive reload", Expected: "Purchased level and derived stats are unchanged", Command: "go test ./save", Enabled: true}
}

func TestTestingProjectPersistenceAndValidation(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	c := Defaults()
	if !c.Testing.Guidance {
		t.Fatal("testing guidance must default on")
	}
	c.Testing.Suites = []TestSuite{sampleTestSuite()}
	p := filepath.Join(t.TempDir(), "caliper.yaml")
	original := "# authored project\ncoding:\n  check_command: original-required\n"
	if err := os.WriteFile(p, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SaveTesting(p, c); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := LoadProject(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Coding.CheckCommand != "original-required" || len(loaded.Testing.Suites) != 1 || loaded.Testing.Suites[0].Expected != c.Testing.Suites[0].Expected {
		t.Fatalf("wrong owner saved: %+v", loaded)
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "model:") || !strings.Contains(string(raw), "# authored project") {
		t.Fatal("save froze inherited connections or dropped project comment")
	}
	for _, pathFn := range []func() (string, error){PreferencesPath, ConnectionsPath} {
		path, e := pathFn()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = os.Stat(path); !os.IsNotExist(e) {
			t.Fatalf("unexpected personal write %s: %v", path, e)
		}
	}
	c.Testing.Guidance = false
	c.Testing.Suites[0].Enabled = false
	c.Testing.Suites[0].DisabledReason = "Browser unavailable; scope remains unverified"
	if err := SaveTesting(p, c); err != nil {
		t.Fatal(err)
	}
	loaded, _, err = LoadProject(p)
	if err != nil || loaded.Testing.Guidance || loaded.Testing.Suites[0].Enabled || loaded.Testing.Suites[0].DisabledReason == "" {
		t.Fatalf("selection persistence failed: %+v %v", loaded.Testing, err)
	}
	before, _ := os.ReadFile(p)
	c.Testing.Suites = append(c.Testing.Suites, c.Testing.Suites[0])
	if SaveTesting(p, c) == nil {
		t.Fatal("duplicate id accepted")
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("invalid config replaced saved selection")
	}
}

func TestTestingRejectsUnownedRequiredAndUnknownAdapter(t *testing.T) {
	for _, mutate := range []func(*TestSuite){
		func(s *TestSuite) { s.Required = true },
		func(s *TestSuite) { s.ID = "@configured-check" },
		func(s *TestSuite) { s.Expected = "" },
		func(s *TestSuite) { s.Enabled = false },
		func(s *TestSuite) { s.ReportFormat = "scan-skip-text" },
		func(s *TestSuite) { s.SpecKey = "project/testing/strategy" },
		func(s *TestSuite) { s.Prerequisites = []TestPrerequisite{{Kind: "command", Value: "probe-browser"}} },
	} {
		s := sampleTestSuite()
		mutate(&s)
		if (TestingConfig{Suites: []TestSuite{s}}).Validate() == nil {
			t.Fatalf("invalid suite accepted: %+v", s)
		}
	}
}
