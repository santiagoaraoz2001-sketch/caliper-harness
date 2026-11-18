// Package workflowlib stores reusable workflow definitions. Published
// versions are immutable. Executions pin one published workflow version
// and every published block version named by that workflow.
package workflowlib

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	StatusDraft     = "draft"
	StatusPublished = "published"
	StatusActive    = "active"
	StatusComplete  = "complete"
)

// maxWorkflowDepth bounds workflow-in-workflow nesting at publish time so
// an execution's expansion stays reviewable in one sitting.
const maxWorkflowDepth = 4

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

type Implementation struct {
	Type            string `json:"type"`
	Tool            string `json:"tool,omitempty"`
	Instructions    string `json:"instructions,omitempty"`
	WorkflowID      string `json:"workflow_id,omitempty"`
	WorkflowVersion int    `json:"workflow_version,omitempty"`
}

type Block struct {
	ID                 string         `json:"id"`
	Version            int            `json:"version"`
	Status             string         `json:"status"`
	Revision           int64          `json:"revision"`
	Title              string         `json:"title"`
	Purpose            string         `json:"purpose"`
	Inputs             []string       `json:"inputs,omitempty"`
	Outputs            []string       `json:"outputs,omitempty"`
	Examples           []string       `json:"examples,omitempty"`
	Implementation     Implementation `json:"implementation"`
	CompletionEvidence string         `json:"completion_evidence"`
	FailureHandling    string         `json:"failure_handling"`
	CreatedAt          string         `json:"created_at"`
	PublishedAt        string         `json:"published_at,omitempty"`
}

type Step struct {
	BlockID      string `json:"block_id"`
	BlockVersion int    `json:"block_version"`
}

type Workflow struct {
	ID                     string   `json:"id"`
	Version                int      `json:"version"`
	Status                 string   `json:"status"`
	Revision               int64    `json:"revision"`
	Title                  string   `json:"title"`
	Purpose                string   `json:"purpose"`
	AppliesTo              []string `json:"applies_to,omitempty"`
	BasedOn                string   `json:"based_on,omitempty"` // source workflow-id@version for a fork
	RevisionReason         string   `json:"revision_reason,omitempty"`
	RevisionKind           string   `json:"revision_kind,omitempty"` // preference or correction for versions after the first
	Inputs                 []string `json:"inputs,omitempty"`
	Steps                  []Step   `json:"steps,omitempty"`
	CompletionRequirements string   `json:"completion_requirements"`
	RevisionRoute          string   `json:"revision_route"`
	CreatedAt              string   `json:"created_at"`
	PublishedAt            string   `json:"published_at,omitempty"`
}

type Activation struct {
	WorkflowID string `json:"workflow_id"`
	Version    int    `json:"version"`
	Enabled    bool   `json:"enabled"`
}

type StepRecord struct {
	BlockID      string `json:"block_id"`
	BlockVersion int    `json:"block_version"`
	Evidence     string `json:"evidence"`
	Output       string `json:"output"`
	CompletedAt  string `json:"completed_at"`
}

type Execution struct {
	ID              string            `json:"id"`
	WorkflowID      string            `json:"workflow_id"`
	WorkflowVersion int               `json:"workflow_version"`
	Inputs          map[string]string `json:"inputs,omitempty"`
	Steps           []Step            `json:"steps"`
	Current         int               `json:"current"`
	Status          string            `json:"status"`
	Records         []StepRecord      `json:"records,omitempty"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
}

type library struct {
	Revision    int64                 `json:"revision"`
	Blocks      []Block               `json:"blocks,omitempty"`
	Workflows   []Workflow            `json:"workflows,omitempty"`
	Activation  map[string]Activation `json:"activation,omitempty"`
	Executions  []Execution           `json:"executions,omitempty"`
	Conversions []Conversion          `json:"conversions,omitempty"`
	NextExec    int                   `json:"next_exec"`
}

type Store struct {
	Path string
	mu   sync.Mutex
	lib  library
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func Open(path string) (*Store, error) {
	s := &Store{Path: path}
	s.lib.Activation = map[string]Activation{}
	s.lib.NextExec = 1
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s.lib); err != nil {
		return nil, fmt.Errorf("read workflow library: %w", err)
	}
	if s.lib.Activation == nil {
		s.lib.Activation = map[string]Activation{}
	}
	if s.lib.NextExec < 1 {
		s.lib.NextExec = len(s.lib.Executions) + 1
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.lib, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

func checkID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("id %q must start with a letter and use lowercase letters, digits, _ or -", id)
	}
	return nil
}

func splitList(v string) []string {
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *Store) blockIndex(id string, version int) int {
	for i := range s.lib.Blocks {
		if s.lib.Blocks[i].ID == id && (version == 0 || s.lib.Blocks[i].Version == version) {
			if version != 0 {
				return i
			}
		}
	}
	if version != 0 {
		return -1
	}
	best := -1
	for i := range s.lib.Blocks {
		b := s.lib.Blocks[i]
		if b.ID != id {
			continue
		}
		if b.Status == StatusDraft {
			return i
		}
		if best < 0 || b.Version > s.lib.Blocks[best].Version {
			best = i
		}
	}
	return best
}

func (s *Store) workflowIndex(id string, version int) int {
	if version != 0 {
		for i := range s.lib.Workflows {
			if s.lib.Workflows[i].ID == id && s.lib.Workflows[i].Version == version {
				return i
			}
		}
		return -1
	}
	best := -1
	for i := range s.lib.Workflows {
		w := s.lib.Workflows[i]
		if w.ID != id {
			continue
		}
		if w.Status == StatusDraft {
			return i
		}
		if best < 0 || w.Version > s.lib.Workflows[best].Version {
			best = i
		}
	}
	return best
}

func (s *Store) nextRevisionLocked() int64 {
	s.lib.Revision++
	return s.lib.Revision
}

func (s *Store) CreateBlockDraft(id, title string) (Block, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkID(id); err != nil {
		return Block{}, err
	}
	if strings.TrimSpace(title) == "" {
		return Block{}, fmt.Errorf("title is required")
	}
	if i := s.blockIndex(id, 0); i >= 0 && s.lib.Blocks[i].Status == StatusDraft {
		return Block{}, fmt.Errorf("block %s already has draft version %d", id, s.lib.Blocks[i].Version)
	}
	version := 1
	for _, b := range s.lib.Blocks {
		if b.ID == id && b.Version >= version {
			version = b.Version + 1
		}
	}
	b := Block{ID: id, Version: version, Status: StatusDraft, Revision: s.nextRevisionLocked(), Title: strings.TrimSpace(title), CreatedAt: now()}
	s.lib.Blocks = append(s.lib.Blocks, b)
	return b, s.saveLocked()
}

func MissingBlockFields(b Block) []string {
	var missing []string
	if strings.TrimSpace(b.Purpose) == "" {
		missing = append(missing, "purpose")
	}
	if len(b.Inputs) == 0 {
		missing = append(missing, "inputs")
	}
	switch b.Implementation.Type {
	case "tool":
		if strings.TrimSpace(b.Implementation.Tool) == "" {
			missing = append(missing, "impl_tool")
		}
	case "main_model", "delegated_worker":
		if strings.TrimSpace(b.Implementation.Instructions) == "" {
			missing = append(missing, "impl_instructions")
		}
	case "workflow":
		if b.Implementation.WorkflowID == "" || b.Implementation.WorkflowVersion < 1 {
			missing = append(missing, "impl_workflow")
		}
	default:
		missing = append(missing, "impl_type")
	}
	if len(b.Outputs) == 0 {
		missing = append(missing, "outputs")
	}
	if strings.TrimSpace(b.CompletionEvidence) == "" {
		missing = append(missing, "completion_evidence")
	}
	if strings.TrimSpace(b.FailureHandling) == "" {
		missing = append(missing, "failure_handling")
	}
	if len(b.Examples) == 0 {
		missing = append(missing, "examples")
	}
	return missing
}

// blockFieldOrder is the canonical application order for multi-field sets:
// identity and implementation fields land before outputs and evidence.
var blockFieldOrder = []string{"title", "purpose", "inputs", "impl_type", "impl_tool", "impl_instructions", "impl_workflow", "outputs", "completion_evidence", "failure_handling", "examples"}

func unknownField(fields map[string]string, order []string, kind string) error {
	known := make(map[string]bool, len(order))
	for _, name := range order {
		known[name] = true
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !known[name] {
			return fmt.Errorf("unknown %s field %q", kind, name)
		}
	}
	return nil
}

func applyBlockField(b *Block, field, value string) error {
	value = strings.TrimSpace(value)
	switch field {
	case "title":
		b.Title = value
	case "purpose":
		b.Purpose = value
	case "inputs":
		b.Inputs = splitList(value)
	case "outputs":
		b.Outputs = splitList(value)
	case "examples":
		b.Examples = splitList(value)
	case "impl_type":
		b.Implementation.Type = value
	case "impl_tool":
		b.Implementation.Tool = value
	case "impl_instructions":
		b.Implementation.Instructions = value
	case "impl_workflow":
		parts := strings.Split(value, "@")
		if len(parts) != 2 {
			return fmt.Errorf("impl_workflow must use workflow-id@version")
		}
		var version int
		if _, err := fmt.Sscanf(parts[1], "%d", &version); err != nil || version < 1 {
			return fmt.Errorf("impl_workflow version must be a positive integer")
		}
		b.Implementation.WorkflowID, b.Implementation.WorkflowVersion = strings.TrimSpace(parts[0]), version
	case "completion_evidence":
		b.CompletionEvidence = value
	case "failure_handling":
		b.FailureHandling = value
	default:
		return fmt.Errorf("unknown block field %q", field)
	}
	return nil
}

func (s *Store) SetBlockField(id string, expectedRevision int64, field, value string) (Block, []string, error) {
	return s.SetBlockFields(id, expectedRevision, map[string]string{field: value})
}

// SetBlockFields applies every supplied field under one expected-revision
// check and one revision advance. Application is atomic: an unknown field
// or invalid value refuses the whole call with nothing changed.
func (s *Store) SetBlockFields(id string, expectedRevision int64, fields map[string]string) (Block, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.blockIndex(id, 0)
	if i < 0 {
		return Block{}, nil, fmt.Errorf("unknown block %s", id)
	}
	b := &s.lib.Blocks[i]
	if b.Status != StatusDraft {
		return *b, MissingBlockFields(*b), fmt.Errorf("block %s version %d is published; use edit to create a new draft version", id, b.Version)
	}
	if expectedRevision != b.Revision {
		return *b, MissingBlockFields(*b), fmt.Errorf("stale revision for block %s: got %d, current %d; read the draft again", id, expectedRevision, b.Revision)
	}
	if len(fields) == 0 {
		return *b, MissingBlockFields(*b), fmt.Errorf("no fields supplied for block %s", id)
	}
	if err := unknownField(fields, blockFieldOrder, "block"); err != nil {
		return *b, MissingBlockFields(*b), err
	}
	next := *b
	for _, field := range blockFieldOrder {
		if value, ok := fields[field]; ok {
			if err := applyBlockField(&next, field, value); err != nil {
				return *b, MissingBlockFields(*b), err
			}
		}
	}
	next.Revision = s.nextRevisionLocked()
	*b = next
	return *b, MissingBlockFields(*b), s.saveLocked()
}

func (s *Store) PublishBlock(id string, expectedRevision int64) (Block, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.blockIndex(id, 0)
	if i < 0 {
		return Block{}, fmt.Errorf("unknown block %s", id)
	}
	b := &s.lib.Blocks[i]
	if b.Status != StatusDraft {
		return *b, fmt.Errorf("block %s version %d is already published", id, b.Version)
	}
	if expectedRevision != b.Revision {
		return *b, fmt.Errorf("stale revision for block %s: got %d, current %d", id, expectedRevision, b.Revision)
	}
	if missing := MissingBlockFields(*b); len(missing) > 0 {
		return *b, fmt.Errorf("block %s cannot publish; missing: %s", id, strings.Join(missing, ", "))
	}
	if b.Implementation.Type == "workflow" {
		wi := s.workflowIndex(b.Implementation.WorkflowID, b.Implementation.WorkflowVersion)
		if wi < 0 || s.lib.Workflows[wi].Status != StatusPublished {
			return *b, fmt.Errorf("workflow implementation %s@%d is not published", b.Implementation.WorkflowID, b.Implementation.WorkflowVersion)
		}
	}
	b.Status, b.PublishedAt, b.Revision = StatusPublished, now(), s.nextRevisionLocked()
	return *b, s.saveLocked()
}

func (s *Store) EditBlock(id string) (Block, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.blockIndex(id, 0)
	if i < 0 {
		return Block{}, fmt.Errorf("unknown block %s", id)
	}
	if s.lib.Blocks[i].Status == StatusDraft {
		return s.lib.Blocks[i], nil
	}
	next := s.lib.Blocks[i]
	next.Version++
	next.Status, next.PublishedAt, next.Revision, next.CreatedAt = StatusDraft, "", s.nextRevisionLocked(), now()
	s.lib.Blocks = append(s.lib.Blocks, next)
	return next, s.saveLocked()
}

func (s *Store) GetBlock(id string, version int) (Block, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.blockIndex(id, version)
	if i < 0 {
		return Block{}, false
	}
	return s.lib.Blocks[i], true
}

func MissingWorkflowFields(w Workflow) []string {
	var missing []string
	if strings.TrimSpace(w.Purpose) == "" {
		missing = append(missing, "purpose")
	}
	if w.Version > 1 {
		if strings.TrimSpace(w.RevisionReason) == "" {
			missing = append(missing, "revision_reason")
		}
		if w.RevisionKind != "preference" && w.RevisionKind != "correction" {
			missing = append(missing, "revision_kind")
		}
	}
	if len(w.Inputs) == 0 {
		missing = append(missing, "inputs")
	}
	if len(w.Steps) == 0 {
		missing = append(missing, "steps")
	}
	if strings.TrimSpace(w.CompletionRequirements) == "" {
		missing = append(missing, "completion_requirements")
	}
	if strings.TrimSpace(w.RevisionRoute) == "" {
		missing = append(missing, "revision_route")
	}
	return missing
}

var tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// parseTags normalizes applies_to tags: lowercase tokens, deduplicated,
// order preserved. Tags are the deterministic discovery key for "is
// there a workflow for this kind of task".
func parseTags(v string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range splitList(strings.ReplaceAll(v, ",", ";")) {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if !tagPattern.MatchString(tag) {
			return nil, fmt.Errorf("applies_to tag %q must use lowercase letters, digits, _ or - only", raw)
		}
		if !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out, nil
}

// WorkflowsByTag returns every workflow version carrying tag, in List order.
func (s *Store) WorkflowsByTag(tag string) []Workflow {
	tag = strings.ToLower(strings.TrimSpace(tag))
	_, workflows := s.List()
	var out []Workflow
	for _, w := range workflows {
		for _, t := range w.AppliesTo {
			if t == tag {
				out = append(out, w)
				break
			}
		}
	}
	return out
}

func parseSteps(v string) ([]Step, error) {
	var steps []Step
	for _, item := range splitList(strings.ReplaceAll(v, ",", ";")) {
		parts := strings.Split(item, "@")
		if len(parts) != 2 {
			return nil, fmt.Errorf("step %q must use block-id@version", item)
		}
		var version int
		if _, err := fmt.Sscanf(parts[1], "%d", &version); err != nil || version < 1 {
			return nil, fmt.Errorf("step %q version must be a positive integer", item)
		}
		steps = append(steps, Step{BlockID: strings.TrimSpace(parts[0]), BlockVersion: version})
	}
	return steps, nil
}

func (s *Store) CreateWorkflowDraft(id, title string) (Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkID(id); err != nil {
		return Workflow{}, err
	}
	if strings.TrimSpace(title) == "" {
		return Workflow{}, fmt.Errorf("title is required")
	}
	if i := s.workflowIndex(id, 0); i >= 0 && s.lib.Workflows[i].Status == StatusDraft {
		return Workflow{}, fmt.Errorf("workflow %s already has draft version %d", id, s.lib.Workflows[i].Version)
	}
	version := 1
	for _, w := range s.lib.Workflows {
		if w.ID == id && w.Version >= version {
			version = w.Version + 1
		}
	}
	w := Workflow{ID: id, Version: version, Status: StatusDraft, Revision: s.nextRevisionLocked(), Title: strings.TrimSpace(title), CreatedAt: now()}
	s.lib.Workflows = append(s.lib.Workflows, w)
	return w, s.saveLocked()
}

// workflowFieldOrder is the canonical application order for multi-field sets.
var workflowFieldOrder = []string{"title", "purpose", "applies_to", "based_on", "revision_reason", "revision_kind", "inputs", "steps", "completion_requirements", "revision_route"}

func applyWorkflowField(w *Workflow, field, value string) error {
	value = strings.TrimSpace(value)
	switch field {
	case "title":
		w.Title = value
	case "purpose":
		w.Purpose = value
	case "applies_to":
		tags, err := parseTags(value)
		if err != nil {
			return err
		}
		w.AppliesTo = tags
	case "based_on":
		w.BasedOn = value
	case "revision_reason":
		w.RevisionReason = value
	case "revision_kind":
		if value != "preference" && value != "correction" {
			return fmt.Errorf("revision_kind must be preference or correction")
		}
		w.RevisionKind = value
	case "inputs":
		w.Inputs = splitList(value)
	case "steps":
		steps, err := parseSteps(value)
		if err != nil {
			return err
		}
		w.Steps = steps
	case "completion_requirements":
		w.CompletionRequirements = value
	case "revision_route":
		w.RevisionRoute = value
	default:
		return fmt.Errorf("unknown workflow field %q", field)
	}
	return nil
}

func (s *Store) SetWorkflowField(id string, expectedRevision int64, field, value string) (Workflow, []string, error) {
	return s.SetWorkflowFields(id, expectedRevision, map[string]string{field: value})
}

// SetWorkflowFields applies every supplied field under one expected-revision
// check and one revision advance, atomically: an unknown field or invalid
// value refuses the whole call with nothing changed.
func (s *Store) SetWorkflowFields(id string, expectedRevision int64, fields map[string]string) (Workflow, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.workflowIndex(id, 0)
	if i < 0 {
		return Workflow{}, nil, fmt.Errorf("unknown workflow %s", id)
	}
	w := &s.lib.Workflows[i]
	if w.Status != StatusDraft {
		return *w, MissingWorkflowFields(*w), fmt.Errorf("workflow %s version %d is published; use edit to create a new draft version", id, w.Version)
	}
	if expectedRevision != w.Revision {
		return *w, MissingWorkflowFields(*w), fmt.Errorf("stale revision for workflow %s: got %d, current %d; read the draft again", id, expectedRevision, w.Revision)
	}
	if len(fields) == 0 {
		return *w, MissingWorkflowFields(*w), fmt.Errorf("no fields supplied for workflow %s", id)
	}
	if err := unknownField(fields, workflowFieldOrder, "workflow"); err != nil {
		return *w, MissingWorkflowFields(*w), err
	}
	next := *w
	for _, field := range workflowFieldOrder {
		if value, ok := fields[field]; ok {
			if err := applyWorkflowField(&next, field, value); err != nil {
				return *w, MissingWorkflowFields(*w), err
			}
		}
	}
	next.Revision = s.nextRevisionLocked()
	*w = next
	return *w, MissingWorkflowFields(*w), s.saveLocked()
}

func (s *Store) workflowCycle(id string, version int, seen map[string]bool) bool {
	key := fmt.Sprintf("%s@%d", id, version)
	if seen[key] {
		return true
	}
	seen[key] = true
	defer delete(seen, key)
	wi := s.workflowIndex(id, version)
	if wi < 0 {
		return false
	}
	for _, step := range s.lib.Workflows[wi].Steps {
		bi := s.blockIndex(step.BlockID, step.BlockVersion)
		if bi < 0 {
			continue
		}
		impl := s.lib.Blocks[bi].Implementation
		if impl.Type == "workflow" && s.workflowCycle(impl.WorkflowID, impl.WorkflowVersion, seen) {
			return true
		}
	}
	return false
}

// workflowDepth reports nesting depth: a workflow of leaf blocks is 1;
// each nested workflow reference adds one level. Cycles report a depth
// above any cap (PublishWorkflow refuses them separately first).
func (s *Store) workflowDepth(id string, version int, seen map[string]bool) int {
	key := fmt.Sprintf("%s@%d", id, version)
	if seen[key] {
		return maxWorkflowDepth + 1
	}
	seen[key] = true
	defer delete(seen, key)
	wi := s.workflowIndex(id, version)
	if wi < 0 {
		return 1
	}
	depth := 1
	for _, step := range s.lib.Workflows[wi].Steps {
		bi := s.blockIndex(step.BlockID, step.BlockVersion)
		if bi < 0 {
			continue
		}
		if impl := s.lib.Blocks[bi].Implementation; impl.Type == "workflow" {
			if d := 1 + s.workflowDepth(impl.WorkflowID, impl.WorkflowVersion, seen); d > depth {
				depth = d
			}
		}
	}
	return depth
}

func (s *Store) PublishWorkflow(id string, expectedRevision int64) (Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.workflowIndex(id, 0)
	if i < 0 {
		return Workflow{}, fmt.Errorf("unknown workflow %s", id)
	}
	w := &s.lib.Workflows[i]
	if w.Status != StatusDraft {
		return *w, fmt.Errorf("workflow %s version %d is already published", id, w.Version)
	}
	if expectedRevision != w.Revision {
		return *w, fmt.Errorf("stale revision for workflow %s: got %d, current %d", id, expectedRevision, w.Revision)
	}
	if missing := MissingWorkflowFields(*w); len(missing) > 0 {
		return *w, fmt.Errorf("workflow %s cannot publish; missing: %s", id, strings.Join(missing, ", "))
	}
	if w.BasedOn != "" {
		source, err := parseSteps(w.BasedOn)
		if err != nil || len(source) != 1 {
			return *w, fmt.Errorf("workflow %s based_on must use workflow-id@version", id)
		}
		si := s.workflowIndex(source[0].BlockID, source[0].BlockVersion)
		if si < 0 || s.lib.Workflows[si].Status != StatusPublished {
			return *w, fmt.Errorf("workflow %s based_on source %s is not published", id, w.BasedOn)
		}
	}
	for _, step := range w.Steps {
		bi := s.blockIndex(step.BlockID, step.BlockVersion)
		if bi < 0 || s.lib.Blocks[bi].Status != StatusPublished {
			return *w, fmt.Errorf("workflow step %s@%d is not a published block", step.BlockID, step.BlockVersion)
		}
	}
	if s.workflowCycle(w.ID, w.Version, map[string]bool{}) {
		return *w, fmt.Errorf("workflow %s contains a recursive workflow reference", id)
	}
	if depth := s.workflowDepth(w.ID, w.Version, map[string]bool{}); depth > maxWorkflowDepth {
		return *w, fmt.Errorf("workflow %s exceeds maximum nesting depth %d (depth %d); flatten nested workflows before publishing", id, maxWorkflowDepth, depth)
	}
	w.Status, w.PublishedAt, w.Revision = StatusPublished, now(), s.nextRevisionLocked()
	return *w, s.saveLocked()
}

func (s *Store) EditWorkflow(id string) (Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.workflowIndex(id, 0)
	if i < 0 {
		return Workflow{}, fmt.Errorf("unknown workflow %s", id)
	}
	if s.lib.Workflows[i].Status == StatusDraft {
		return s.lib.Workflows[i], nil
	}
	next := s.lib.Workflows[i]
	next.Version++
	next.RevisionReason, next.RevisionKind = "", ""
	next.Status, next.PublishedAt, next.Revision, next.CreatedAt = StatusDraft, "", s.nextRevisionLocked(), now()
	s.lib.Workflows = append(s.lib.Workflows, next)
	return next, s.saveLocked()
}

// ForkWorkflow creates a new draft from one published workflow version.
// The source stays immutable; BasedOn records provenance.
func (s *Store) ForkWorkflow(sourceID string, sourceVersion int, id, title string) (Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkID(id); err != nil {
		return Workflow{}, err
	}
	if strings.TrimSpace(title) == "" {
		return Workflow{}, fmt.Errorf("title is required")
	}
	si := s.workflowIndex(sourceID, sourceVersion)
	if si < 0 || s.lib.Workflows[si].Status != StatusPublished {
		return Workflow{}, fmt.Errorf("fork source %s@%d is not published", sourceID, sourceVersion)
	}
	if i := s.workflowIndex(id, 0); i >= 0 {
		return Workflow{}, fmt.Errorf("workflow %s already exists; use edit to revise it", id)
	}
	next := s.lib.Workflows[si]
	next.ID, next.Version, next.Title = id, 1, strings.TrimSpace(title)
	next.BasedOn = fmt.Sprintf("%s@%d", sourceID, sourceVersion)
	next.RevisionReason, next.RevisionKind = "", ""
	next.Status, next.PublishedAt, next.Revision, next.CreatedAt = StatusDraft, "", s.nextRevisionLocked(), now()
	next.AppliesTo = append([]string(nil), next.AppliesTo...)
	next.Inputs = append([]string(nil), next.Inputs...)
	next.Steps = append([]Step(nil), next.Steps...)
	s.lib.Workflows = append(s.lib.Workflows, next)
	return next, s.saveLocked()
}

// LatestPublishedWorkflow returns the highest published version, ignoring drafts.
func (s *Store) LatestPublishedWorkflow(id string) (Workflow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	best := -1
	for i := range s.lib.Workflows {
		w := s.lib.Workflows[i]
		if w.ID == id && w.Status == StatusPublished && (best < 0 || w.Version > s.lib.Workflows[best].Version) {
			best = i
		}
	}
	if best < 0 {
		return Workflow{}, false
	}
	return s.lib.Workflows[best], true
}

// SearchToken binds authoring to one observed library state. Listing returns
// it; starting a new workflow requires the same token unless the user
// explicitly requested creation. Any library mutation invalidates it.
func (s *Store) SearchToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := sha256.New()
	fmt.Fprintf(h, "revision:%d\n", s.lib.Revision)
	for _, b := range s.lib.Blocks {
		fmt.Fprintf(h, "block:%s@%d:%s:%d\n", b.ID, b.Version, b.Status, b.Revision)
	}
	for _, w := range s.lib.Workflows {
		fmt.Fprintf(h, "workflow:%s@%d:%s:%d:%s\n", w.ID, w.Version, w.Status, w.Revision, strings.Join(w.AppliesTo, ","))
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}

func workflowTokens(w Workflow) map[string]bool {
	stop := map[string]bool{"the": true, "and": true, "for": true, "with": true, "one": true, "task": true, "work": true, "workflow": true}
	out := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(w.Title+" "+w.Purpose), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) {
		if len(word) >= 3 && !stop[word] {
			out[word] = true
		}
	}
	return out
}

// SimilarWorkflows returns the current draft-or-latest version of other
// workflows sharing a tag, title, or at least two significant title/purpose
// words. It supports the read-before-authoring decision; it does not forbid
// a deliberate fork or distinct specialization.
func (s *Store) SimilarWorkflows(candidate Workflow) []Workflow {
	_, all := s.List()
	byID := map[string]Workflow{}
	for _, w := range all {
		if w.ID == candidate.ID {
			continue
		}
		current, ok := byID[w.ID]
		if !ok || w.Status == StatusDraft && current.Status != StatusDraft || w.Version > current.Version && current.Status != StatusDraft {
			byID[w.ID] = w
		}
	}
	candidateTokens := workflowTokens(candidate)
	candidateTags := map[string]bool{}
	for _, tag := range candidate.AppliesTo {
		candidateTags[tag] = true
	}
	var out []Workflow
	for _, w := range byID {
		similar := strings.EqualFold(strings.TrimSpace(w.Title), strings.TrimSpace(candidate.Title)) && candidate.Title != ""
		for _, tag := range w.AppliesTo {
			similar = similar || candidateTags[tag]
		}
		shared := 0
		for token := range workflowTokens(w) {
			if candidateTokens[token] {
				shared++
			}
		}
		if similar || shared >= 2 {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) GetWorkflow(id string, version int) (Workflow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.workflowIndex(id, version)
	if i < 0 {
		return Workflow{}, false
	}
	return s.lib.Workflows[i], true
}

func (s *Store) SetActivation(id string, version int, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.workflowIndex(id, version)
	if i < 0 || s.lib.Workflows[i].Status != StatusPublished {
		return fmt.Errorf("workflow %s@%d is not published", id, version)
	}
	s.lib.Activation[id] = Activation{WorkflowID: id, Version: version, Enabled: enabled}
	s.nextRevisionLocked()
	return s.saveLocked()
}

func (s *Store) StartExecution(id string, inputs map[string]string) (Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	act, ok := s.lib.Activation[id]
	if !ok || !act.Enabled {
		return Execution{}, fmt.Errorf("workflow %s is not enabled; enable one published version first", id)
	}
	wi := s.workflowIndex(id, act.Version)
	if wi < 0 {
		return Execution{}, fmt.Errorf("enabled workflow %s@%d is missing", id, act.Version)
	}
	w := s.lib.Workflows[wi]
	exec := Execution{
		ID: fmt.Sprintf("exec-%d", s.lib.NextExec), WorkflowID: id, WorkflowVersion: w.Version,
		Inputs: inputs, Steps: append([]Step(nil), w.Steps...), Status: StatusActive,
		CreatedAt: now(), UpdatedAt: now(),
	}
	s.lib.NextExec++
	s.lib.Executions = append(s.lib.Executions, exec)
	s.nextRevisionLocked()
	return exec, s.saveLocked()
}

func (s *Store) AdvanceExecution(id, evidence, output string) (Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(evidence) == "" {
		return Execution{}, fmt.Errorf("evidence is required to advance an execution")
	}
	for i := range s.lib.Executions {
		e := &s.lib.Executions[i]
		if e.ID != id {
			continue
		}
		if e.Status != StatusActive {
			return *e, fmt.Errorf("execution %s is %s", id, e.Status)
		}
		step := e.Steps[e.Current]
		e.Records = append(e.Records, StepRecord{BlockID: step.BlockID, BlockVersion: step.BlockVersion, Evidence: evidence, Output: output, CompletedAt: now()})
		e.Current++
		e.UpdatedAt = now()
		if e.Current >= len(e.Steps) {
			e.Status = StatusComplete
		}
		s.nextRevisionLocked()
		return *e, s.saveLocked()
	}
	return Execution{}, fmt.Errorf("unknown execution %s", id)
}

func (s *Store) GetExecution(id string) (Execution, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.lib.Executions {
		if e.ID == id {
			return e, true
		}
	}
	return Execution{}, false
}

func (s *Store) Activation(id string) (Activation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.lib.Activation[id]
	return a, ok
}

func (s *Store) List() ([]Block, []Workflow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	blocks := append([]Block(nil), s.lib.Blocks...)
	workflows := append([]Workflow(nil), s.lib.Workflows...)
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].ID < blocks[j].ID || (blocks[i].ID == blocks[j].ID && blocks[i].Version < blocks[j].Version)
	})
	sort.Slice(workflows, func(i, j int) bool {
		return workflows[i].ID < workflows[j].ID || (workflows[i].ID == workflows[j].ID && workflows[i].Version < workflows[j].Version)
	})
	return blocks, workflows
}

func (s *Store) ListExecutions() []Execution {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Execution(nil), s.lib.Executions...)
}
