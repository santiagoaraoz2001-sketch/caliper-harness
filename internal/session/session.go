// Package session owns ALL session write logic: append, read, list, sweep.
// GraphQL resolvers and the agent loop are thin callers over it — session
// edits concentrate here, never in callers.
package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"specific-harness/internal/checkreport"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"strings"
	"time"
)

var SummaryPrefix = prompts.Builtin().Text("continuation.summary_prefix")

// NewID returns a fresh session id: UTC timestamp plus 4 random hex chars.
// Safe for resolve: no separators, no dots.
func NewID() string {
	var b [2]byte
	_, _ = rand.Read(b[:]) // failure still leaves a timestamp-unique id
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// Event is one timestamped session line.
type Event struct {
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}

// Append adds one event to dir/<id>.jsonl, creating dir and file. Zero Time
// stamps now; empty kind is rejected.
func Append(dir, id string, e Event) error {
	path, err := resolve(dir, id)
	if err != nil {
		return err
	}
	if e.Kind == "" {
		return fmt.Errorf("session event kind cannot be empty")
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create sessions dir %s: %s", dir, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("cannot open session %s: %s", id, err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(e); err != nil {
		return fmt.Errorf("cannot write session %s: %s", id, err)
	}
	return nil
}

// Read returns every event in dir/<id>.jsonl, oldest first.
func Read(dir, id string) ([]Event, error) {
	path, err := resolve(dir, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open session %s: %w", id, err)
	}
	defer f.Close()
	var out []Event
	reader := bufio.NewReader(f)
	for {
		raw, readErr := reader.ReadBytes('\n')
		if readErr != nil && readErr != io.EOF {
			return nil, fmt.Errorf("cannot read session %s: %w", id, readErr)
		}
		if len(raw) == 0 && readErr == io.EOF {
			break
		}
		var e Event
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("session %s has a corrupt line: %w", id, err)
		}
		out = append(out, e)
		if readErr == io.EOF {
			break
		}
	}
	return out, nil
}

// MarkUsed refreshes an existing archive without rewriting its events.
func MarkUsed(dir, id string) error {
	path, err := resolve(dir, id)
	if err == nil {
		now := time.Now()
		err = os.Chtimes(path, now, now)
	}
	if err != nil {
		return fmt.Errorf("cannot record use of session %s: %w", id, err)
	}
	return nil
}

// ReadUsed is explicit user access. Polling and listing must keep using Read.
func ReadUsed(dir, id string) ([]Event, error) {
	events, err := Read(dir, id)
	if err == nil {
		err = MarkUsed(dir, id)
	}
	return events, err
}

// List returns every session id in dir, oldest file first. A missing dir is
// no sessions, not an error.
func List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot list sessions in %s: %s", dir, err)
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".jsonl"))
	}
	return ids, nil
}

// Sweep deletes sessions untouched for months calendar months (file mtime
// before the cutoff) and returns the deleted ids. Months <= 0 disables
// sweeping: nothing is deleted.
func Sweep(dir string, months int) ([]string, error) {
	if months <= 0 {
		return nil, nil
	}
	cutoff := time.Now().AddDate(0, -months, 0)
	ids, err := List(dir)
	if err != nil {
		return nil, err
	}
	var deleted []string
	for _, id := range ids {
		path, err := resolve(dir, id)
		if err != nil {
			return nil, err
		}
		st, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("cannot stat session %s: %s", id, err)
		}
		if st.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil {
				return nil, fmt.Errorf("cannot delete session %s: %s", id, err)
			}
			deleted = append(deleted, id)
		}
	}
	return deleted, nil
}

// resolve maps an id to its file. Ids with separators or ".." are rejected:
// ids arrive from user input (GraphQL args) and must not escape the dir.
func resolve(dir, id string) (string, error) {
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, `\`) || strings.Contains(id, "..") {
		return "", fmt.Errorf("bad session id %q", id)
	}
	return filepath.Join(dir, id+".jsonl"), nil
}

// Entry records provenance at the append site, never by parsing model text.
// Injected base/behavior/recall entries are transient and omitted from snapshots.
// TaskRound is append-site provenance, never a claim or copied task brief.
type TaskRound struct {
	Store, Workspace, TaskID string
	Attempt                  int
	CommentID                string `json:"comment_id,omitempty"`
}

func RoundFor(a *Assignment, commentID string) *TaskRound {
	if a == nil {
		return nil
	}
	return &TaskRound{Store: a.Store, Workspace: a.Workspace, TaskID: a.TaskID, Attempt: a.Attempt, CommentID: commentID}
}

type Entry struct {
	UserNoteID       string      `json:"user_note_id,omitempty"` // durable queue identity, not authority
	Round            *TaskRound  `json:"round,omitempty"`
	PreparationScope string      `json:"preparation_scope,omitempty"` // append-site owner/project identity, never authority
	Message          llm.Message `json:"message"`
	Kind             string      `json:"kind"`
	Unread           bool        `json:"unread,omitempty"`
}

// WorkflowScope stores identity only; owning state is re-fetched before use.
type WorkflowScope = tool.WorkflowScope

// Continuation is the bounded conversation, independent of the full raw log.
// Prompt is the latest measured request, not current occupancy or cumulative cost.
type Continuation struct {
	// ToolView is an explicit user exposure choice, never inferred from selected nodes or role registries.
	ToolView     string         `json:"tool_view,omitempty"`
	PendingNotes []UserNote     `json:"pending_notes,omitempty"`
	Workflow     *WorkflowScope `json:"workflow,omitempty"`
	Assignment   *Assignment    `json:"assignment,omitempty"`
	Coding       *CodingState   `json:"coding,omitempty"`
	SchemaBytes  int            `json:"schema_bytes,omitempty"`
	SchemaKey    string         `json:"schema_key,omitempty"`
	Version      int            `json:"version"`
	Entries      []Entry        `json:"entries"`
	Compactions  int            `json:"compactions"`
	Revision     int            `json:"revision"`
	Prompt       int            `json:"prompt"`
	Measured     bool           `json:"measured"`
	BasisTokens  int            `json:"basis_tokens,omitempty"`
	BasisChars   int            `json:"basis_chars,omitempty"`
	ModelKey     string         `json:"model_key,omitempty"`
}

func (c Continuation) Clone() Continuation {
	c.PendingNotes = append([]UserNote(nil), c.PendingNotes...)
	if c.Assignment != nil {
		value := *c.Assignment
		c.Assignment = &value
	}
	if c.Workflow != nil {
		value := *c.Workflow
		c.Workflow = &value
	}
	if c.Coding != nil {
		value := *c.Coding
		value.Report = value.Report.Clone()
		c.Coding = &value
		if value.Receipt != nil {
			receipt := *value.Receipt
			receipt.Report = receipt.Report.Clone()
			c.Coding.Receipt = &receipt
			if receipt.Prerequisites != nil {
				observations := *receipt.Prerequisites
				observations.Observations = append([]checkreport.PrerequisiteObservation(nil), observations.Observations...)
				c.Coding.Receipt.Prerequisites = &observations
			}
			if receipt.Acceptance != nil {
				acceptance := *receipt.Acceptance
				c.Coding.Receipt.Acceptance = &acceptance
				if acceptance.ExitCode != nil {
					code := *acceptance.ExitCode
					c.Coding.Receipt.Acceptance.ExitCode = &code
				}
			}
			if receipt.ExitCode != nil {
				code := *receipt.ExitCode
				c.Coding.Receipt.ExitCode = &code
			}
		}
	}
	c.Entries = append([]Entry(nil), c.Entries...)
	for i := range c.Entries {
		c.Entries[i].Message = c.Entries[i].Message.Clone()
		if c.Entries[i].Round != nil {
			round := *c.Entries[i].Round
			c.Entries[i].Round = &round
		}
	}
	return c
}
func (c Continuation) Messages() []llm.Message {
	out := make([]llm.Message, 0, len(c.Entries))
	for _, e := range c.Entries {
		out = append(out, e.Message.Clone())
	}
	return out
}
func (c *Continuation) Add(kind, role, text string, unread bool) {
	c.Entries = append(c.Entries, Entry{Message: llm.Message{Role: role, Content: text}, Kind: kind, Unread: unread})
}

// AddGuidance keeps reissued standalone host advice at the settled tail;
// its exact provenance and all raw enforcement events remain.
func (c *Continuation) AddGuidance(text string, round *TaskRound) {
	for i := 0; i < len(c.Entries); i = c.GroupEnd(i) {
		e := c.Entries[i]
		m := e.Message
		if e.Kind == "guidance" && m.Role == "user" && len(m.ToolCalls) == 0 && m.ToolCallID == "" && m.Name == "" && m.Content == text && (e.Round == nil && round == nil || e.Round != nil && round != nil && *e.Round == *round) {
			copy(c.Entries[i:], c.Entries[i+1:])
			c.Entries[len(c.Entries)-1] = e
			return
		}
	}
	c.Add("guidance", "user", text, false)
	c.Entries[len(c.Entries)-1].Round = round
}

// RetireGuidance makes exact qualified-round host advice eligible for existing
// compaction/resume cleanup. User comments and factual receipts stay protected.
func (c *Continuation) RetireGuidance(round *TaskRound) bool {
	changed := false
	for i := range c.Entries {
		e := &c.Entries[i]
		if e.Kind == "guidance" && !e.Unread && len(e.Message.ToolCalls) == 0 && e.Round != nil && round != nil && *e.Round == *round {
			e.Kind = "history"
			changed = true
		}
	}
	if changed {
		c.Invalidate()
	}
	return changed
}

func (c *Continuation) Invalidate() { c.Measured = false; c.Revision++ }
func (c Continuation) Snapshot() Continuation {
	c = c.Clone()
	entries := c.Entries
	c.Entries = nil
	for _, e := range entries {
		if e.Kind != "injected" {
			c.Entries = append(c.Entries, e)
		}
	}
	return c
}

// Validate checks shape, provenance and complete native correlation. Storage uses the same JSONL format without a scanner-derived size ceiling.
func (c Continuation) Validate() error {
	if c.ToolView != "" && c.ToolView != "coding" && c.ToolView != "full" && c.ToolView != "workflow" {
		return fmt.Errorf("invalid user tool view")
	}
	if c.ToolView == "workflow" && c.Workflow == nil {
		return fmt.Errorf("explicit workflow tool view requires selected scope")
	}
	notes := map[string]bool{}
	for _, note := range c.PendingNotes {
		if note.ID == "" || note.SessionID == "" || strings.TrimSpace(note.Text) == "" || notes[note.ID] {
			return fmt.Errorf("invalid pending user note")
		}
		notes[note.ID] = true
	}
	if w := c.Workflow; w != nil && (w.Store == "" || !(w.Kind == "root" && w.ID == "" || w.ID != "" && (w.Kind == "plan" || w.Kind == "approach" || w.Kind == "task"))) {
		return fmt.Errorf("invalid workflow scope identity")
	}

	if a := c.Assignment; a != nil && (a.Store == "" || a.Workspace == "" || a.PlanID == "" || a.ApproachID == "" || a.TaskID == "" || a.Attempt <= 0) {
		return fmt.Errorf("invalid assignment provenance")
	}
	if err := c.Coding.Validate(); err != nil {
		return err
	}
	if err := c.validateGroups(); err != nil {
		return err
	}
	if c.SchemaBytes < 0 || (c.SchemaBytes > 0 && c.SchemaKey == "") {
		return fmt.Errorf("invalid tool schema accounting")
	}
	if c.Version != 1 || len(c.Entries) == 0 || c.Compactions < 0 || c.Revision < 0 || c.Prompt < 0 || c.BasisTokens < 0 || c.BasisChars < 0 {
		return fmt.Errorf("invalid continuation shape/version")
	}
	intent, task := 0, 0
	for _, e := range c.Entries {
		if e.PreparationScope != "" && (e.Kind != "guidance" && e.Kind != "schema" && e.Kind != "history" || e.Message.Role != "user" || e.Unread || len(e.Message.ToolCalls) != 0 || e.Message.ToolCallID != "" || e.Message.Name != "") {
			return fmt.Errorf("invalid preparation-guidance provenance")
		}
		if r := e.Round; r != nil && (r.Store == "" || r.Workspace == "" || r.TaskID == "" || r.Attempt <= 0 || e.Kind != "note" && e.Kind != "guidance" && e.Kind != "history") {
			return fmt.Errorf("invalid task-round provenance")
		}
		role := "user"
		switch e.Kind {
		case "reply":
			role = "assistant"
		case "intent":
			intent++
		case "task":
			task++
		case "history", "note", "answer", "output", "schema", "summary", "failure", "evidence", "guidance":
		default:
			return fmt.Errorf("invalid continuation kind %q", e.Kind)
		}
		if e.Kind == "history" && e.Message.Role == "assistant" {
			role = "assistant"
		}
		if e.Message.Role == "tool" && (e.Kind == "output" || e.Kind == "answer" || e.Kind == "failure" || e.Kind == "history") {
			role = "tool"
		}
		if e.Message.Role != role || (e.Unread && e.Kind != "output" && e.Kind != "answer" && e.Kind != "schema" && e.Kind != "failure") {
			return fmt.Errorf("invalid continuation role/unread")
		}
	}
	if intent != 1 || task > 1 {
		return fmt.Errorf("continuation needs one original intent and at most one latest task")
	}
	_, err := json.Marshal(c)
	return err
}
func (c Continuation) Encode() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(c)
	return string(b), err
}

// Restore selects the latest valid snapshot and reconstructs only its raw tail.
// Legacy omissions are disclosed; failures/notes/intent never silently disappear.
func Restore(events []Event, window int, modelKey ...string) (Continuation, string, error) {
	pc, e := prompts.Default()
	if e != nil {
		return Continuation{}, "", e
	}
	return RestoreWithPrompts(events, window, pc, modelKey...)
}
func RestoreWithPrompts(events []Event, window int, pc *prompts.Catalog, modelKey ...string) (Continuation, string, error) {
	c := Continuation{Version: 1}
	start := 0
	notice := "legacy bounded reconstruction"
	invalid := false
	for i, e := range events {
		if e.Kind == "continuation" {
			var next Continuation
			if json.Unmarshal([]byte(e.Text), &next) == nil && next.Validate() == nil {
				c = next
				start = i + 1
				notice = "restored bounded continuation"
				invalid = false
			} else {
				invalid = true
			}
		}
	}
	if invalid {
		notice += "; newer invalid snapshot ignored"
	}
	// Queue receipts can precede the latest continuation. Reconcile the whole
	// existing archive by stable identity, so a crash before capture loses none.
	queued, acknowledged := map[string]UserNote{}, map[string]UserNote{}
	var queueOrder []string
	for _, event := range events {
		if event.Kind != "btw_queued" && event.Kind != "btw_ack" {
			continue
		}
		var note UserNote
		if err := json.Unmarshal([]byte(event.Text), &note); err != nil || note.ID == "" {
			return c, notice, fmt.Errorf("invalid durable user-note receipt")
		}
		if event.Kind == "btw_queued" {
			if prior, ok := queued[note.ID]; ok && prior != note {
				return c, notice, fmt.Errorf("user-note identity has conflicting text")
			}
			if _, ok := queued[note.ID]; !ok {
				queueOrder = append(queueOrder, note.ID)
			}
			queued[note.ID] = note
		} else {
			if prior, ok := acknowledged[note.ID]; ok && prior != note {
				return c, notice, fmt.Errorf("user-note acknowledgement conflicts")
			}
			acknowledged[note.ID] = note
		}
	}
	rawTextResults := map[int]bool{}
	for _, e := range events[start:] {
		switch e.Kind {
		case "consumed":
			var receipt ConsumptionReceipt
			if json.Unmarshal([]byte(e.Text), &receipt) != nil || !c.consume(receipt, rawTextResults) {
				// A corrupt, stale or legacy projection cannot prove processing.
				if !strings.Contains(notice, "unconfirmed consumption retained") {
					notice += "; unconfirmed consumption retained unread"
				}
			}
		case "coding":
			if err := json.Unmarshal([]byte(e.Text), &c.Coding); err != nil {
				return c, notice, err
			}
		case "native_reply", "native_result":
			var entry Entry
			if err := json.Unmarshal([]byte(e.Text), &entry); err != nil {
				return c, notice, err
			}
			if e.Kind == "native_reply" && entry.Message.Role != "assistant" || e.Kind == "native_result" && entry.Message.Role != "tool" {
				return c, notice, fmt.Errorf("invalid native event role")
			}
			c.Entries = append(c.Entries, entry)
		case "prompt":
			for i := range c.Entries {
				if c.Entries[i].Kind == "task" {
					c.Entries[i].Kind = "history"
				}
			}
			kind := "task"
			if len(c.Entries) == 0 {
				kind = "intent"
			}
			c.Add(kind, "user", e.Text, false)
		case "done":
			if n := len(c.Entries); n == 0 || c.Entries[n-1].Message.Role != "assistant" || c.Entries[n-1].Message.Content != e.Text {
				c.Add("reply", "assistant", e.Text, false)
			}
		case "reply", "partial":
			c.Add("reply", "assistant", e.Text, false)
		case "btw":
			c.Add("note", "user", "USER COMMENT: "+e.Text, false)
		case "btw_received":
			// Capture is not delivery. A later continuation replaces this fallback
			// with the committed note; interruption still preserves the user's intent.
			c.Add("note", "user", "Undelivered user comment: "+e.Text, false)
		case "task_comment":
			var comment outcome.TaskComment
			if err := json.Unmarshal([]byte(e.Text), &comment); err != nil {
				return c, notice, err
			}
			raw, err := json.Marshal(comment)
			if err != nil {
				return c, notice, err
			}
			c.Add("note", "user", pc.Text("continuation.task_comment_prefix")+string(raw), false)
			if a := c.Assignment; a != nil && a.TaskID == comment.TaskID && a.Attempt == comment.Attempt {
				c.Entries[len(c.Entries)-1].Round = RoundFor(a, comment.ID)
			}
		case "result":
			rawTextResults[len(c.Entries)] = true
			c.Add("output", "user", e.Text, true)
		case "ask_answer":
			n := len(c.Entries)
			if n > 0 && c.Entries[n-1].Kind == "output" && c.Entries[n-1].Message.Content == e.Text {
				c.Entries[n-1].Kind = "answer"
			} else {
				c.Add("answer", "user", e.Text, false)
			}
		case "error", "failed", "stopped", "cancelled":
			c.Add("failure", "user", e.Kind+": "+e.Text, false)
		case "enforcement":
			c.Add("guidance", "user", e.Text, false)
		case "evidence":
			c.Add("evidence", "user", e.Text, false)
		case "compact":
			clear(rawTextResults) // Conservative reconstruction changes entry indices.
			keep, entries := c.Protected(3), c.Entries
			c.Entries = nil
			for i, entry := range entries {
				if keep[i] {
					c.Entries = append(c.Entries, entry)
				}
			}
			c.Add("summary", "user", pc.Text("continuation.summary_prefix")+e.Text, false)
			c.Compactions++
			if !strings.Contains(notice, "conservative compact reconstruction") {
				notice += "; conservative compact reconstruction (unknown observations retained)"
			}
		}
	}
	c.Coding.Historical()
	c.settleCrashTail()
	if err := c.validateGroups(); err != nil {
		return c, notice, err
	}
	for _, note := range c.PendingNotes {
		if _, ok := queued[note.ID]; !ok {
			queued[note.ID] = note
			queueOrder = append(queueOrder, note.ID)
		}
	}
	c.PendingNotes = nil
	for _, id := range queueOrder {
		note := queued[id]
		ack, acknowledgedNote := acknowledged[id]
		if acknowledgedNote && ack != note {
			return c, notice, fmt.Errorf("user-note acknowledgement does not match queued intent")
		}
		if !acknowledgedNote {
			c.PendingNotes = append(c.PendingNotes, note)
			continue
		}
		found := false
		for _, entry := range c.Entries {
			found = found || entry.UserNoteID == id
		}
		if !found {
			c.Add("note", "user", "USER COMMENT: "+note.Text, false)
			c.Entries[len(c.Entries)-1].UserNoteID = id
		}
	}
	c.Invalidate()
	if len(modelKey) == 0 || modelKey[0] != c.ModelKey {
		c.BasisTokens, c.BasisChars = 0, 0
	}
	if window > 0 && c.Estimate() > window {
		notice += fmt.Sprintf("; restored content estimate %d exceeds window %d; retained history needs owning context cleanup before generation", c.Estimate(), window)
	}
	return c, notice, c.Validate()
}
func contentBytes(messages []llm.Message) int {
	n := 0
	for _, m := range messages {
		n += m.Size()
	}
	return n
}

// estimate is explicitly content based, not a backend tokenizer measurement.
// The denominator belongs to the exact measured request; growth never resets it.
func (c Continuation) Estimate() int {
	size := contentBytes(c.Messages()) + c.SchemaBytes
	if c.BasisChars > 0 && c.BasisTokens > 0 {
		return int(math.Ceil(float64(size) * float64(c.BasisTokens) / float64(c.BasisChars)))
	}
	return size + 20*len(c.Entries) // conservative byte estimate + disclosed message allowance
}

// Protected is shared by pruning, compaction and resume; latest three schemas
// remain mandatory even when a smaller class count is requested.
func (c Continuation) Protected(schemasKeep int) []bool {
	keep := make([]bool, len(c.Entries))
	schemas := 0
	if schemasKeep < 3 {
		schemasKeep = 3
	}
	for i := len(c.Entries) - 1; i >= 0; i-- {
		e := c.Entries[i]
		switch e.Kind {
		case "injected", "intent", "task", "note", "answer", "failure", "evidence", "guidance":
			keep[i] = true
		case "schema":
			schemas++
			keep[i] = schemas <= schemasKeep
		}
		keep[i] = keep[i] || e.Unread
		// Native Ask replies are authenticated tool results; preserve the whole
		// correlated call/result group even after the answer has been consumed.
		keep[i] = keep[i] || e.Message.Role == "tool" && e.Message.Name == "ask"
	}
	for i := 0; i < len(c.Entries); {
		end := c.GroupEnd(i)
		protect := false
		for j := i; j < end; j++ {
			protect = protect || keep[j]
		}
		if protect {
			for j := i; j < end; j++ {
				keep[j] = true
			}
		}
		i = end
	}
	return keep
}

// ReadBounded is an explicit graph projection; full raw Read remains unchanged.
func ReadBounded(ctx context.Context, dir, id string, rows, bytes int) ([]Event, error) {
	path, err := resolve(dir, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if rows <= 0 || bytes <= 0 {
		return nil, fmt.Errorf("GraphQL session budget exhausted; narrow selected events")
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, min(1024, bytes)), bytes)
	out := []Event{}
	remaining := bytes
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(out) >= rows {
			return nil, fmt.Errorf("GraphQL session exceeds aggregate rows; narrow query")
		}
		if len(sc.Bytes()) > remaining {
			return nil, fmt.Errorf("GraphQL session exceeds aggregate source bytes; narrow query")
		}
		remaining -= len(sc.Bytes())
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("GraphQL session record exceeds source budget or read failed: %w", err)
	}
	return out, ctx.Err()
}
func ListBounded(ctx context.Context, dir string, rows int) ([]string, error) {
	f, err := os.Open(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []string{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := f.ReadDir(1)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		e := entries[0]
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if len(out) >= rows {
			return nil, fmt.Errorf("GraphQL session list exceeds aggregate rows; narrow query")
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".jsonl"))
	}
	sort.Strings(out)
	return out, nil
}

// Exists validates identity and actual readability without loading any events.
func Exists(ctx context.Context, dir, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	path, err := resolve(dir, id)
	if err != nil {
		return false, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("session %s is not a regular file", id)
	}
	return true, ctx.Err()
}
