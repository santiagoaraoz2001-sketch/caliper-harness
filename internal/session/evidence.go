package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"specific-harness/internal/llm"
	"specific-harness/internal/pathutil"
	"strings"
)

// EvidenceGroup keeps physical JSONL offsets and every recorded native result.
// Unlike resume, this reader never settles a crash tail or creates a receipt.
type EvidenceGroup struct {
	Offset  int
	Calls   []llm.ToolCall
	Records []EvidenceRecord
}
type EvidenceRecord struct {
	Offset    int
	Event     Event
	Entry     *Entry
	Workspace string
}

// VisitEvidence validates the entire archive's host association and correlation,
// one event/group at a time. The caller retains only its selected projection.
// Output budgets apply in the caller; a large unselected line is not a scan cap.
func VisitEvidence(ctx context.Context, dir, id, project string, visit func(EvidenceGroup) error) (int, *EvidenceGroup, error) {
	path, err := resolve(dir, id)
	if err != nil {
		return 0, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, nil, fmt.Errorf("session %s is not a readable regular archive: %v", id, err)
	}
	reader := bufio.NewReader(f)
	return visitEvidence(ctx, id, project, true, func() (Event, error) {
		raw, readErr := reader.ReadBytes('\n')
		if len(raw) == 0 || readErr != nil && readErr != io.EOF {
			return Event{}, readErr
		}
		var e Event
		err := json.Unmarshal(raw, &e)
		return e, err
	}, visit)
}

// VisitEventEvidence reads actual events retained on an already scoped Task.
// The caller must establish that owning association before calling this owner.
func VisitEventEvidence(ctx context.Context, events []Event, project string, visit func(EvidenceGroup) error) (int, *EvidenceGroup, error) {
	i := 0
	return visitEvidence(ctx, "task events", project, false, func() (Event, error) {
		if i == len(events) {
			return Event{}, io.EOF
		}
		e := events[i]
		i++
		return e, nil
	}, visit)
}

// VisitRoleEvidence requires an already project-scoped owning Task and exact
// recorded role identity. Archived worktree paths remain evidence, not authority.
func VisitRoleEvidence(ctx context.Context, dir, id, project, task string, attempt int, phase string, visit func(EvidenceGroup) error) (int, *EvidenceGroup, error) {
	if task == "" || attempt < 1 || phase == "" {
		return 0, nil, fmt.Errorf("role archive requires exact task, attempt and phase")
	}
	path, err := resolve(dir, id)
	if err != nil {
		return 0, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, nil, fmt.Errorf("role session is not a regular archive: %v", err)
	}
	reader := bufio.NewReader(f)
	owned := false
	total, pending, err := visitEvidence(ctx, id, project, false, func() (Event, error) {
		raw, readErr := reader.ReadBytes('\n')
		if len(raw) == 0 || readErr != nil && readErr != io.EOF {
			return Event{}, readErr
		}
		var e Event
		if err := json.Unmarshal(raw, &e); err != nil {
			return e, err
		}
		if e.Kind == "role_owner" {
			var owner struct {
				Task    string
				Attempt int
				Phase   string
			}
			if err := json.Unmarshal([]byte(e.Text), &owner); err != nil {
				return e, err
			}
			if owner.Task != task || owner.Attempt != attempt || owner.Phase != phase {
				return e, fmt.Errorf("role archive owner mismatch")
			}
			owned = true
		} else if !owned && e.Kind != "workspace" {
			return e, fmt.Errorf("role archive has no owning identity before evidence")
		}
		return e, nil
	}, visit)
	if err == nil && !owned {
		err = fmt.Errorf("role archive has no owning identity")
	}
	return total, pending, err
}

func visitEvidence(ctx context.Context, id, project string, sessionScoped bool, next func() (Event, error), visit func(EvidenceGroup) error) (int, *EvidenceGroup, error) {
	associated, workspace := !sessionScoped, ""
	associate := func(workspace string) error {
		if !sessionScoped {
			return nil // Task ownership grants this read; archived worktree paths grant no authority.
		}
		if !filepath.IsAbs(workspace) {
			return fmt.Errorf("session %s has invalid host workspace association", id)
		}
		physical, err := pathutil.EvalSymlinks(workspace)
		if err != nil || physical != project {
			return fmt.Errorf("session %s is outside the captured project or mixes workspaces", id)
		}
		associated = true
		return nil
	}
	var pending *EvidenceGroup
	legacy := false
	resultAt, offset := 0, 0
	testHash := ""
	testSelection := ""
	for {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		e, readErr := next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil || e.Kind == "" || e.Time.IsZero() {
			return 0, nil, fmt.Errorf("session %s has a corrupt event at offset %d: %v", id, offset, readErr)
		}
		var err error
		record := EvidenceRecord{Offset: offset, Event: e}
		switch e.Kind {
		case "workspace":
			workspace = e.Text
			err = associate(e.Text)
		case "test_start", "test_result":
			var state struct {
				Selection struct{ ID, Workspace, Hash string }
			}
			err = json.Unmarshal([]byte(e.Text), &state)
			if err == nil && (!strings.HasPrefix(id, "tests-") || state.Selection.ID != id || state.Selection.Hash == "") {
				err = fmt.Errorf("Tests archive owner is invalid")
			}
			if err == nil {
				workspace = state.Selection.Workspace
				err = associate(workspace)
			}
			var rawSelection struct{ Selection map[string]any }
			if err == nil {
				decoder := json.NewDecoder(strings.NewReader(e.Text))
				decoder.UseNumber()
				err = decoder.Decode(&rawSelection)
			}
			canonical, _ := json.Marshal(rawSelection.Selection)
			if err == nil {
				if e.Kind == "test_start" && testHash == "" {
					testSelection = string(canonical)
					testHash = state.Selection.Hash
				} else if e.Kind == "test_result" && testHash == state.Selection.Hash && testSelection == string(canonical) {
				} else {
					err = fmt.Errorf("Tests archive selection changed or start is missing")
				}
			}
		case "coding":
			var coding CodingState
			err = json.Unmarshal([]byte(e.Text), &coding)
			if err == nil {
				err = coding.Validate()
			}
			if err == nil {
				workspace = coding.Workspace
				err = associate(coding.Workspace)
			}
		case "continuation":
			var c Continuation
			err = json.Unmarshal([]byte(e.Text), &c)
			if err == nil {
				err = c.Validate()
			}
			if err == nil && c.Coding != nil {
				workspace = c.Coding.Workspace
				err = associate(c.Coding.Workspace)
			}
			if err == nil && c.Assignment != nil {
				workspace = c.Assignment.Workspace
				err = associate(c.Assignment.Workspace)
			}
		case "native_reply", "native_result":
			var entry Entry
			err = json.Unmarshal([]byte(e.Text), &entry)
			if err == nil {
				record.Entry = &entry
				if entry.Round != nil {
					err = associate(entry.Round.Workspace)
					record.Workspace = entry.Round.Workspace
				}
			}
		}
		if err != nil {
			return 0, nil, fmt.Errorf("session %s event %d: %w", id, offset, err)
		}
		if record.Entry == nil || record.Entry.Round == nil {
			record.Workspace = workspace
		}
		if e.Kind == "native_reply" {
			if pending != nil {
				return 0, nil, fmt.Errorf("session %s has an unsettled call group before another reply at offset %d", id, pending.Offset)
			}
			m := record.Entry.Message
			candidate := Continuation{Entries: []Entry{*record.Entry}}
			for _, call := range m.ToolCalls {
				// These matching-role values validate call syntax only. They are
				// never recorded or returned as results; actual receipts follow.
				candidate.Entries = append(candidate.Entries, Entry{Message: llm.Message{Role: "tool", ToolCallID: call.ID, Name: call.Name}})
			}
			if m.Role != "assistant" || candidate.validateGroups() != nil {
				return 0, nil, fmt.Errorf("session %s has invalid native calls at offset %d", id, offset)
			}
			if len(m.ToolCalls) > 0 {
				pending = &EvidenceGroup{Offset: offset, Calls: m.ToolCalls}
				resultAt, legacy = 0, false
			}
		}
		if e.Kind == "native_result" {
			if pending == nil || legacy || resultAt >= len(pending.Calls) {
				return 0, nil, fmt.Errorf("session %s has an orphan native result at offset %d", id, offset)
			}
			call, m := pending.Calls[resultAt], record.Entry.Message
			if m.Role != "tool" || m.ToolCallID != call.ID || m.Name != call.Name || len(m.ToolCalls) != 0 {
				return 0, nil, fmt.Errorf("session %s native result association mismatch at offset %d", id, offset)
			}
			resultAt++
		}
		if e.Kind == "tool" && pending == nil {
			pending, legacy = &EvidenceGroup{Offset: offset}, true
		}
		if pending == nil {
			err = visit(EvidenceGroup{Offset: offset, Records: []EvidenceRecord{record}})
		} else {
			pending.Records = append(pending.Records, record)
			if legacy && e.Kind == "result" || !legacy && resultAt == len(pending.Calls) {
				err = visit(*pending)
				pending = nil
			}
		}
		if err != nil {
			return 0, nil, err
		}
		offset++
	}
	if !associated {
		return 0, nil, fmt.Errorf("session %s has no host workspace association; legacy history is unavailable to project-scoped recall", id)
	}
	return offset, pending, ctx.Err()
}
