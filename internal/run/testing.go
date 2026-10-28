package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"specific-harness/internal/agent"
	"specific-harness/internal/auth"
	"specific-harness/internal/checkreport"
	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/pathutil"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
	"specific-harness/internal/tools/process"
)

const ConfiguredCheckID = "@configured-check"

var errTestSpecChanged = errors.New("test spec changed or unavailable")

type TestDeps struct {
	Cfg       config.Config
	Workspace string
	Store     *store.Store
	Session   agent.SessionLog
	AskYesNo  func(context.Context, string) bool
	In        io.Reader
	Out       io.Writer
}

// TestSelection owns a frozen optional execution, never a Chat or PAT attempt.
type TestSelection struct {
	ID             string             `json:"id"`
	Workspace      string             `json:"workspace"`
	ConfigRevision string             `json:"config_revision"`
	Hash           string             `json:"hash"`
	Suites         []config.TestSuite `json:"suites"`
}

type TestResult struct {
	SuiteID  string                `json:"suite_id"`
	Status   string                `json:"status"`
	Reason   string                `json:"reason,omitempty"`
	Receipt  *session.CheckReceipt `json:"receipt,omitempty"`
	Coverage TestCoverage          `json:"coverage"`
}

type TestRun struct {
	Selection       TestSelection `json:"selection"`
	Started         string        `json:"started"`
	Ended           string        `json:"ended,omitempty"`
	Status          string        `json:"status"`
	Reason          string        `json:"reason,omitempty"`
	WorkspaceBefore string        `json:"workspace_before,omitempty"`
	WorkspaceAfter  string        `json:"workspace_after,omitempty"`
	Results         []TestResult  `json:"results,omitempty"`
}

// TestSuites presents the immutable configured obligation alongside optional
// project suites. Running this displayed obligation remains standalone evidence.
func TestSuites(c config.Config) []config.TestSuite {
	var suites []config.TestSuite
	if strings.TrimSpace(c.Coding.CheckCommand) != "" {
		expected := "The configured command succeeds; coverage remains unknown"
		if c.Coding.ReportFormat != "" {
			expected = "The configured command and declared required stages succeed; semantic test quality remains unverified"
		}
		contract := c.Coding.ReportContract()
		suites = append(suites, config.TestSuite{ID: ConfiguredCheckID, Name: "Configured check", Purpose: "Required by the existing coding contract", Expected: expected, Command: c.Coding.CheckCommand, ReportFormat: contract.Format, ExpectedTests: contract.Expected, Stages: contract.Stages, Enabled: true, Required: true})
	}
	suites = append(suites, c.Testing.Suites...)
	return suites
}

func testDigest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func testConfigRevision(c config.Config) string {
	return testDigest(struct {
		Suites []config.TestSuite
		Coding config.CodingConfig
		Bash   config.BashConfig
	}{c.Testing.Suites, c.Coding, c.Tools.Bash})
}

func selectionHash(s TestSelection) string {
	s.ID, s.Hash = "", ""
	return testDigest(s)
}

func testWorkspace(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("Tests requires an explicit workspace")
	}
	p, err := filepath.Abs(root)
	if err == nil {
		p, err = pathutil.EvalSymlinks(p)
	}
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("Tests workspace is not an available directory: %s", p)
	}
	return p, nil
}

func testSpec(ctx context.Context, d TestDeps, s config.TestSuite) error {
	if s.SpecKey == "" {
		return nil
	}
	if d.Store == nil {
		return fmt.Errorf("spec %s cannot be verified without the project store", s.SpecKey)
	}
	entries, err := memory.Knowledge(ctx, d.Store, d.Workspace, s.SpecKey, "", 1, 8192, false)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].ID != s.SpecVersion {
		return fmt.Errorf("%w: %s; revise the suite's explicit reference", errTestSpecChanged, s.SpecKey)
	}
	return nil
}

// FreezeTests copies configuration and explicit spec references before dispatch.
// A nil ID list selects enabled optional suites, never an implicit obligation.
func FreezeTests(ctx context.Context, d TestDeps, ids []string) (TestSelection, error) {
	if err := d.Cfg.Validate(); err != nil {
		return TestSelection{}, err
	}
	root, err := testWorkspace(d.Workspace)
	if err != nil {
		return TestSelection{}, err
	}
	d.Workspace = root
	available := TestSuites(d.Cfg)
	if len(ids) == 0 {
		for _, s := range available {
			if s.Enabled && !s.Required {
				ids = append(ids, s.ID)
			}
		}
	}
	if len(ids) == 0 || len(ids) > 33 {
		return TestSelection{}, fmt.Errorf("select between 1 and 33 enabled test suites")
	}
	out := TestSelection{ID: "tests-" + session.NewID(), Workspace: root, ConfigRevision: testConfigRevision(d.Cfg)}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return TestSelection{}, fmt.Errorf("test suite %q selected twice", id)
		}
		seen[id] = true
		found := false
		for _, s := range available {
			if s.ID != id {
				continue
			}
			if !s.Enabled {
				return TestSelection{}, fmt.Errorf("suite %s is disabled: %s", id, s.DisabledReason)
			}
			if err := testSpec(ctx, d, s); err != nil {
				return TestSelection{}, err
			}
			// Deep-copy the slices; future edits cannot mutate this execution.
			s.Prerequisites = append([]config.TestPrerequisite(nil), s.Prerequisites...)
			s.ExpectedTests = append([]string(nil), s.ExpectedTests...)
			s.Stages = s.ReportContract().Stages
			out.Suites = append(out.Suites, s)
			found = true
			break
		}
		if !found {
			return TestSelection{}, fmt.Errorf("unknown test suite %q", id)
		}
	}
	out.Hash = selectionHash(out)
	return out, nil
}

func testPrerequisites(root string, suite config.TestSuite) error {
	for _, p := range suite.Prerequisites {
		if status, reason := process.PrerequisitePresence(root, p); status != "present" {
			return fmt.Errorf("%s prerequisite %s: %s", status, p.Value, reason)
		}
	}
	return nil
}

// TestReadiness inspects declarations without executing any prerequisite probe.
// Enabled selection and runnable prerequisites are deliberately separate facts.
func TestReadiness(ctx context.Context, d TestDeps, suite config.TestSuite) (bool, string) {
	if err := ctx.Err(); err != nil {
		return false, err.Error()
	}
	root, err := testWorkspace(d.Workspace)
	if err != nil {
		return false, err.Error()
	}
	d.Workspace = root
	if err := testSpec(ctx, d, suite); err != nil {
		return false, err.Error()
	}
	if err := testPrerequisites(root, suite); err != nil {
		return false, err.Error()
	}
	for _, stage := range suite.Stages {
		for _, p := range stage.Prerequisites {
			if status, reason := process.PrerequisitePresence(root, p); status != "present" {
				return false, fmt.Sprintf("stage %s prerequisite %s is %s: %s; the check can run but cannot qualify yet", stage.ID, p.Value, status, reason)
			}
		}
	}
	return true, "declared prerequisites present; runtime health is unverified until execution"
}

func appendTest(ctx context.Context, sink agent.SessionLog, id, kind string, v any) error {
	raw, err := json.Marshal(v)
	if err == nil {
		err = sink.Append(context.WithoutCancel(ctx), id, kind, string(raw))
	}
	return err
}

// The existing check owner emits coding snapshots. Namespace them so no restore
// of these optional executions can replace a Chat/PAT CodingState.
type testCheckLog struct {
	sink      agent.SessionLog
	id, suite string
}

func (s testCheckLog) Append(ctx context.Context, _ string, kind, text string) error {
	return appendTest(ctx, s.sink, s.id, "test_check", struct {
		SuiteID string          `json:"suite_id"`
		Kind    string          `json:"kind"`
		Receipt json.RawMessage `json:"receipt"`
	}{s.suite, kind, json.RawMessage(text)})
}

// RunTests calls the existing check executor serially. There is no model loop,
// invented execution engine, workflow transition or adopted Chat state.
func RunTests(ctx context.Context, d TestDeps, selection TestSelection) (TestRun, error) {
	out := TestRun{Selection: selection, Started: time.Now().UTC().Format(time.RFC3339Nano), Status: "running"}
	if d.Session == nil {
		return out, fmt.Errorf("Tests requires a durable session archive")
	}
	ids := make([]string, len(selection.Suites))
	for i, s := range selection.Suites {
		ids[i] = s.ID
	}
	current, err := FreezeTests(ctx, d, ids)
	if err != nil || !strings.HasPrefix(selection.ID, "tests-") || selection.Hash == "" || selection.Hash != selectionHash(selection) || current.Hash != selection.Hash {
		return out, errors.Join(err, fmt.Errorf("test selection changed or is invalid; select again"))
	}
	d.Workspace = selection.Workspace
	pc, err := d.Cfg.LoadPrompts()
	if err != nil {
		return out, err
	}
	d.Cfg.PromptCatalog = pc
	observer := &agent.Runner{Workspace: d.Workspace, WorkspaceExcludes: workspaceExcludes(d.Cfg, d.Store)}
	out.WorkspaceBefore, err = observer.WorkspaceFingerprint(ctx)
	if err != nil {
		return out, fmt.Errorf("cannot fingerprint Tests workspace: %w", err)
	}
	if err = session.Reserve(d.Cfg.Sessions.Path, selection.ID, session.Event{Kind: "workspace", Text: d.Workspace}); err != nil {
		return out, err
	}
	if err = appendTest(ctx, d.Session, selection.ID, "test_start", out); err != nil {
		return out, err
	}
	gate := &auth.Gate{Prompts: pc, DecisionDisabled: d.Cfg.Decision.Disabled, Mode: d.Cfg.Auth.Mode, In: d.In, Out: d.Out, AskFunc: d.AskYesNo, Judge: configuredJudge(d.Cfg), Threshold: d.Cfg.Decision.RiskThreshold, Judged: d.Cfg.Auth.JudgedTools, Free: d.Cfg.Auth.FreeTools, MaxArgChars: d.Cfg.Auth.MaxArgChars, JudgmentContextChars: d.Cfg.Decision.JudgmentContextChars}
	gate.Record = func(c context.Context, event auth.ApprovalEvent) error {
		event.Workspace, event.Session = d.Workspace, selection.ID
		if d.Store != nil {
			event.Store = d.Store.Identity()
		}
		return appendTest(c, d.Session, selection.ID, "test_approval", event)
	}
	gate.Log = func(kind, text string) {
		_ = d.Session.Append(context.WithoutCancel(ctx), selection.ID, "test_"+kind, text)
	}
	for _, suite := range selection.Suites {
		result := TestResult{SuiteID: suite.ID, Status: "blocked", Coverage: TestCoverage{Status: "unknown", Reason: "command not executed"}}
		if ctx.Err() != nil {
			result.Status, result.Reason = "cancelled", ctx.Err().Error()
		} else if e := testSpec(ctx, d, suite); e != nil {
			result.Reason = e.Error()
		} else if e := testPrerequisites(d.Workspace, suite); e != nil {
			result.Reason = e.Error()
		} else {
			contract := suite.ReportContract()
			state := &session.CodingState{Report: contract.Clone(), TaskID: selection.ID + ":" + suite.ID, Workspace: d.Workspace, Command: suite.Command, ConfigCommand: suite.Command}
			r := &agent.Runner{Prompts: pc, Workspace: d.Workspace, CheckCommand: suite.Command, CheckReport: contract.Clone(), SessionID: selection.ID, Session: testCheckLog{d.Session, selection.ID, suite.ID}, Continuation: session.Continuation{Coding: state}}
			def := r.CheckDefinition(bash.NewAt(d.Cfg.Tools.Bash, d.Workspace, pc))
			res, e := gate.Wrap(agent.Direct)(ctx, def, tool.Args{"cmd": suite.Command, "timeout": int64(d.Cfg.Tools.Bash.DefaultTimeout)})
			result.Receipt = state.Receipt
			switch {
			case ctx.Err() != nil:
				result.Status, result.Reason = "cancelled", ctx.Err().Error()
			case e != nil:
				result.Status, result.Reason = "unverified", e.Error()
			case res.Blocked:
				result.Status, result.Reason = "blocked", res.Text
			case state.Passed():
				result.Status, result.Reason = "passed", "process passed; coverage unknown"
			case state.Receipt != nil && state.Receipt.Status == "failed":
				result.Status, result.Reason = "failed", state.Receipt.Reason
			default:
				result.Status, result.Reason = "unverified", "no complete command receipt"
			}
			result.Coverage = checkreport.Coverage{Status: "unknown", Reason: "command not executed"}
			if result.Receipt != nil {
				result.Coverage = result.Receipt.Coverage
				if result.Status == "unverified" && result.Receipt.Reason != "" {
					result.Reason = result.Receipt.Reason
				}
			}
			if result.Status == "passed" {
				result.Reason = "process passed; " + result.Coverage.Reason
			}

		}
		out.Results = append(out.Results, result)
		if e := appendTest(ctx, d.Session, selection.ID, "test_suite_result", result); e != nil {
			err = errors.Join(err, e)
			break // No further commands when their owning evidence cannot persist.
		}
	}
	out.Status, out.Reason = "passed", "standalone test evidence; does not qualify Chat/PAT; inspect coverage and freshness"
	for _, result := range out.Results {
		if result.Status != "passed" {
			out.Status, out.Reason = result.Status, result.Reason
			break
		}
	}
	if len(out.Results) != len(selection.Suites) || err != nil {
		out.Status, out.Reason = "unverified", "execution or receipt recording incomplete"
	}
	out.WorkspaceAfter, err = fingerprintAfter(ctx, observer, err)
	if err != nil {
		out.Status, out.Reason = "unverified", "workspace freshness or receipt recording unavailable"
	}
	out.Ended = time.Now().UTC().Format(time.RFC3339Nano)
	err = errors.Join(err, appendTest(ctx, d.Session, selection.ID, "test_result", out))
	if err != nil {
		out.Status, out.Reason = "unverified", err.Error()
	}
	return out, err
}

func fingerprintAfter(ctx context.Context, r *agent.Runner, prior error) (string, error) {
	// Do not extend cancelled execution with an unbounded directory walk.
	if ctx.Err() != nil {
		return "", prior
	}
	hash, err := r.WorkspaceFingerprint(ctx)
	return hash, errors.Join(prior, err)
}

// TestFreshness is derived on inspection; historical receipt bytes never change.
func TestFreshness(ctx context.Context, d TestDeps, result TestRun) (string, error) {
	states, err := TestFreshnessBatch(ctx, d, []TestRun{result})
	return states[0], err
}

// TestFreshnessBatch observes source once per refresh and each referenced graph
// head once. Rendering/listing old receipts must not repeatedly walk the project.
func TestFreshnessBatch(ctx context.Context, d TestDeps, results []TestRun) ([]string, error) {
	states := make([]string, len(results))
	for i := range states {
		states[i] = "unknown"
	}
	if len(results) == 0 {
		return states, nil
	}
	root, err := testWorkspace(d.Workspace)
	if err != nil {
		return states, err
	}
	d.Workspace = root
	r := &agent.Runner{Workspace: root, WorkspaceExcludes: workspaceExcludes(d.Cfg, d.Store)}
	hash, err := r.WorkspaceFingerprint(ctx)
	if err != nil {
		return states, err
	}
	revision := testConfigRevision(d.Cfg)
	type head struct {
		id  string
		err error
	}
	heads := map[string]head{}
	var observationErr error
	for i, result := range results {
		if result.Ended == "" || result.WorkspaceBefore == "" || result.WorkspaceAfter == "" {
			continue
		}
		if root != result.Selection.Workspace || revision != result.Selection.ConfigRevision || result.Selection.Hash != selectionHash(result.Selection) || result.WorkspaceBefore != result.WorkspaceAfter || hash != result.WorkspaceAfter {
			states[i] = "stale"
			continue
		}
		states[i] = "current"
		for _, s := range result.Selection.Suites {
			if s.SpecKey == "" {
				continue
			}
			h, ok := heads[s.SpecKey]
			if !ok {
				if d.Store == nil {
					h.err = fmt.Errorf("spec %s cannot be verified without the project store", s.SpecKey)
				} else {
					entries, e := memory.Knowledge(ctx, d.Store, root, s.SpecKey, "", 1, 8192, false)
					h.err = e
					if len(entries) == 1 {
						h.id = entries[0].ID
					}
				}
				heads[s.SpecKey] = h
				observationErr = errors.Join(observationErr, h.err)
			}
			if h.err != nil {
				states[i] = "unknown"
				break
			}
			if h.id != s.SpecVersion {
				states[i] = "stale"
				break
			}
		}
	}
	return states, observationErr
}

func TestHistory(events []session.Event) ([]TestRun, error) {
	var out []TestRun
	positions := map[string]int{}
	for _, event := range events {
		if event.Kind != "test_start" && event.Kind != "test_result" {
			continue
		}
		var r TestRun
		if err := json.Unmarshal([]byte(event.Text), &r); err != nil {
			return nil, err
		}
		if r.Selection.ID == "" || r.Selection.Hash != selectionHash(r.Selection) {
			return nil, fmt.Errorf("invalid Tests archive selection")
		}
		if event.Kind == "test_start" {
			r.Status, r.Reason = "incomplete", "run started; no final receipt recorded"
		}
		if pos, ok := positions[r.Selection.ID]; ok {
			out[pos] = r
		} else {
			positions[r.Selection.ID] = len(out)
			out = append(out, r)
		}
	}
	return out, nil
}

// ListTestRuns reads only dedicated Tests archives, newest first. Raw commands
// and logs remain with the existing session owner; there is no second ledger.
func ListTestRuns(ctx context.Context, dir, workspace string, limit int) ([]TestRun, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("Tests history limit must be between 1 and 100")
	}
	root, err := testWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	ids, err := session.List(dir)
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	var out []TestRun
	inspected := 0
	for _, id := range ids {
		if !strings.HasPrefix(id, "tests-") {
			continue
		}
		if inspected >= 200 {
			break
		}
		inspected++
		events, err := session.ReadBounded(ctx, dir, id, 1024, 32*1024*1024)
		if err != nil {
			return out, err
		}
		runs, err := TestHistory(events)
		if err != nil {
			return out, err
		}
		for _, r := range runs {
			if r.Selection.Workspace == root {
				out = append(out, r)
			}
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}
