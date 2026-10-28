package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"specific-harness/internal/graph/model"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"strings"
	"time"

	"github.com/vektah/gqlparser/v2/ast"
)

func evidencePage(ctx context.Context, sessionID, taskID string, offset, limit int) (*model.EvidencePage, error) {
	s, err := scope(ctx)
	if err != nil {
		return nil, err
	}
	if budget(ctx) == nil {
		return nil, fmt.Errorf("selected evidence requires ExecBounded with configured GraphQL budgets")
	}
	if offset < 0 || limit <= 0 {
		return nil, fmt.Errorf("evidence offset must be nonnegative and limit must be positive")
	}
	p := &model.EvidencePage{ProjectRoot: s.root, SessionID: sessionID, TaskID: taskID, Historical: true, Groups: []*model.EvidenceGroup{}}
	if err := charge(ctx, 1); err != nil {
		return nil, err
	}
	for _, f := range selected(ctx, rootSelection(ctx), "EvidencePage") {
		value := map[string]string{"projectRoot": p.ProjectRoot, "sessionId": sessionID, "taskId": taskID}[f.Name]
		if err := charge(ctx, 0, value); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (r *queryResolver) SessionEvidence(ctx context.Context, sessionID string, offset, limit int, callID *string) (*model.EvidencePage, error) {
	p, err := evidencePage(ctx, sessionID, "", offset, limit)
	if err != nil {
		return nil, err
	}
	err = collectEvidencePage(ctx, p, offset, limit, callID, "session:"+sessionID, func(visit func(session.EvidenceGroup) error) (int, *session.EvidenceGroup, error) {
		return session.VisitEvidence(ctx, r.sessionsDir, sessionID, p.ProjectRoot, visit)
	})
	if err != nil {
		return nil, err
	}
	// Refresh only after the complete operation and encoded result fit the budget.
	b := budget(ctx)
	b.sessionUses = append(b.sessionUses, struct{ dir, id string }{r.sessionsDir, sessionID})
	return p, nil
}

func collectEvidencePage(ctx context.Context, p *model.EvidencePage, offset, limit int, callID *string, reference string, scan func(func(session.EvidenceGroup) error) (int, *session.EvidenceGroup, error)) error {
	if callID != nil && strings.TrimSpace(*callID) == "" {
		return fmt.Errorf("callId must be an exact nonempty recorded native call id")
	}
	matches, foundCall := 0, false
	_, groupSelections := childSelection(ctx, rootSelection(ctx), "EvidencePage", "groups")
	count, pending, err := scan(func(g session.EvidenceGroup) error {
		end := g.Records[len(g.Records)-1].Offset + 1
		if offset > g.Offset && offset < end {
			return fmt.Errorf("offset %d cuts a call/result group; use offset %d", offset, g.Offset)
		}
		if g.Offset < offset {
			return nil
		}
		if callID != nil {
			match := false
			for _, call := range g.Calls {
				match = match || call.ID == *callID
			}
			if !match {
				return nil
			}
			foundCall = true
		}
		if matches >= limit {
			if p.NextOffset == nil {
				n := g.Offset
				p.NextOffset = &n
			}
			return nil
		}
		matches++
		if groupSelections == 0 {
			return nil
		}
		out := &model.EvidenceGroup{Reference: fmt.Sprintf("%s:%d", reference, g.Offset), Offset: g.Offset, CallIds: []string{}, Records: []*model.EvidenceRecord{}}
		for _, f := range selected(ctx, rootSelection(ctx), "EvidencePage") {
			if f.Name == "groups" {
				if err := projectEvidenceGroup(ctx, out, g, f.Selections); err != nil {
					return err
				}
			}
		}
		p.Groups = append(p.Groups, out)
		return nil
	})
	if err != nil {
		return err
	}
	if pending != nil {
		p.UnsettledOffset = &pending.Offset
		if offset > pending.Offset {
			return fmt.Errorf("offset is after unsettled group %d; reread that boundary after settlement", pending.Offset)
		}
		for _, call := range pending.Calls {
			if callID != nil && call.ID == *callID {
				return fmt.Errorf("call %s belongs to unsettled group %d; its complete recorded call/result group is unavailable", call.ID, pending.Offset)
			}
		}
		if p.NextOffset == nil {
			p.NextOffset = &pending.Offset
		}
	}
	if offset > count || callID != nil && !foundCall {
		return fmt.Errorf("evidence reference is unavailable; use a recorded group offset/call id")
	}
	return nil
}

func (r *queryResolver) TaskEvidence(ctx context.Context, taskID string, offset, limit int, attempt *int, kind *string, recordIndex *int, phase, callID *string) (*model.EvidencePage, error) {
	p, err := evidencePage(ctx, "", taskID, offset, limit)
	if err != nil {
		return nil, err
	}
	if attempt != nil && *attempt < 0 || kind != nil && strings.TrimSpace(*kind) == "" {
		return nil, fmt.Errorf("task evidence needs a nonnegative stored attempt and nonempty kind when supplied")
	}
	if (recordIndex == nil) != (phase == nil) || recordIndex != nil && *recordIndex < 0 || callID != nil && phase == nil {
		return nil, fmt.Errorf("embedded task events need both an explicit record index and phase; callId applies only to those events")
	}
	if phase != nil && *phase != "execution" && *phase != "reviewer" && *phase != "check" {
		return nil, fmt.Errorf("task evidence phase must be execution, reviewer or check")
	}
	matches := 0
	foundRecord := false
	_, groupSelections := childSelection(ctx, rootSelection(ctx), "EvidencePage", "groups")
	count, err := plan.VisitAttemptRecords(plan.WithProject(ctx, p.ProjectRoot), r.store, taskID, budget(ctx).bytes, func(index int, raw json.RawMessage) error {
		var record struct {
			Kind    string
			Attempt int
			Comment *outcome.TaskComment
		}
		if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &record) != nil || record.Kind == "" || record.Attempt < 0 {
			return fmt.Errorf("task %s has corrupt evidence at offset %d", taskID, index)
		}
		if record.Comment != nil {
			record.Attempt = record.Comment.Attempt
			if record.Comment.TaskID != taskID || record.Attempt <= 0 {
				return fmt.Errorf("task %s has corrupt comment provenance at offset %d", taskID, index)
			}
		}
		if phase != nil {
			if index != *recordIndex {
				return nil
			}
			foundRecord = true
			if attempt != nil && record.Attempt != *attempt || kind != nil && record.Kind != *kind {
				return fmt.Errorf("selected task record does not match the supplied stored attempt/kind")
			}
			var sources struct {
				ExecutionEvents  *[]session.Event
				ReviewerEvents   *[]session.Event
				CheckEvents      *[]session.Event
				ExecutionSession string
				ReviewerSession  string
				CheckSession     string
			}
			if err := json.Unmarshal(raw, &sources); err != nil {
				return fmt.Errorf("task %s has corrupt saved event sources: %w", taskID, err)
			}
			archive := map[string]string{"execution": sources.ExecutionSession, "reviewer": sources.ReviewerSession, "check": sources.CheckSession}[*phase]
			if archive != "" {
				reference := fmt.Sprintf("task:%s:%d:%s", taskID, index, *phase)
				if err := collectEvidencePage(ctx, p, offset, limit, callID, reference, func(visit func(session.EvidenceGroup) error) (int, *session.EvidenceGroup, error) {
					ownerPhase := map[string]string{"execution": "execution_pending", "reviewer": "review_pending", "check": "check_pending"}[*phase]
					return session.VisitRoleEvidence(ctx, r.sessionsDir, archive, p.ProjectRoot, taskID, record.Attempt, ownerPhase, visit)
				}); err != nil {
					return err
				}
				budget(ctx).sessionUses = append(budget(ctx).sessionUses, struct{ dir, id string }{r.sessionsDir, archive})
				return nil
			}
			events := map[string]*[]session.Event{"execution": sources.ExecutionEvents, "reviewer": sources.ReviewerEvents, "check": sources.CheckEvents}[*phase]
			if events == nil {
				return fmt.Errorf("task record %d has no saved %s events; inspect its labeled raw history instead", index, *phase)
			}
			reference := fmt.Sprintf("task:%s:%d:%s", taskID, index, *phase)
			return collectEvidencePage(ctx, p, offset, limit, callID, reference, func(visit func(session.EvidenceGroup) error) (int, *session.EvidenceGroup, error) {
				return session.VisitEventEvidence(ctx, *events, p.ProjectRoot, visit)
			})
		}
		if index < offset || attempt != nil && record.Attempt != *attempt || kind != nil && record.Kind != *kind {
			return nil
		}
		if matches >= limit {
			if p.NextOffset == nil {
				n := index
				p.NextOffset = &n
			}
			return nil
		}
		matches++
		if groupSelections == 0 {
			return nil
		}
		source := "host-recorded task outcome; embedded model text is authored"
		if record.Kind == "comment" || record.Kind == "review" || strings.HasPrefix(record.Kind, "revision_") {
			source = "authored task record; no execution authority"
		}
		g := session.EvidenceGroup{Offset: index, Records: []session.EvidenceRecord{{Offset: index, Event: session.Event{Kind: record.Kind, Text: string(raw)}}}}
		out := &model.EvidenceGroup{Reference: fmt.Sprintf("task:%s:%d", taskID, index), Offset: index, CallIds: []string{}, Records: []*model.EvidenceRecord{}}
		for _, f := range selected(ctx, rootSelection(ctx), "EvidencePage") {
			if f.Name == "groups" {
				if err := projectEvidenceGroup(ctx, out, g, f.Selections, source); err != nil {
					return err
				}
			}
		}
		p.Groups = append(p.Groups, out)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if phase != nil {
		if !foundRecord {
			return nil, fmt.Errorf("task attempt-record index is unavailable; discover its absolute offset first")
		}
		return p, nil
	}
	if offset > count {
		return nil, fmt.Errorf("task evidence offset is outside the recorded attempt log")
	}
	return p, nil
}

func projectEvidenceGroup(ctx context.Context, out *model.EvidenceGroup, g session.EvidenceGroup, set ast.SelectionSet, taskSource ...string) error {
	if err := charge(ctx, 1); err != nil {
		return err
	}
	for _, f := range selected(ctx, set, "EvidenceGroup") {
		switch f.Name {
		case "reference":
			if err := charge(ctx, 0, out.Reference); err != nil {
				return err
			}
		case "callIds":
			out.CallIds = []string{}
			for _, call := range g.Calls {
				if err := charge(ctx, 1, call.ID); err != nil {
					return err
				}
				out.CallIds = append(out.CallIds, call.ID)
			}
		case "records":
			if len(out.Records) == 0 {
				for _, v := range g.Records {
					out.Records = append(out.Records, &model.EvidenceRecord{Offset: v.Offset})
				}
			}
			for i, v := range g.Records {
				if err := projectEvidenceRecord(ctx, out.Records[i], v, f.Selections, taskSource...); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func projectEvidenceRecord(ctx context.Context, out *model.EvidenceRecord, v session.EvidenceRecord, set ast.SelectionSet, taskSource ...string) error {
	if err := charge(ctx, 1); err != nil {
		return err
	}
	source := "archived event; authority unknown"
	switch v.Event.Kind {
	case "native_reply", "reply", "done", "partial", "compact":
		source = "model-authored message"
	case "native_result", "result":
		source = "recorded tool result; correctness unknown"
	case "prompt", "btw":
		source = "user-authored message"
	case "test_start", "test_result", "test_suite_result", "test_check", "test_approval":
		source = "host-recorded standalone Tests evidence; never Chat/PAT qualification"
	case "workspace", "coding", "continuation", "context", "approval":
		source = "host-recorded state"
	}
	if len(taskSource) > 0 {
		source = taskSource[0]
	}
	for _, f := range selected(ctx, set, "EvidenceRecord") {
		value := ""
		switch f.Name {
		case "time":
			if !v.Event.Time.IsZero() {
				out.Time = v.Event.Time.Format(time.RFC3339Nano)
			}
			value = out.Time
		case "kind":
			out.Kind, value = v.Event.Kind, v.Event.Kind
		case "source":
			out.Source, value = source, source
		case "workspace":
			out.Workspace, value = v.Workspace, v.Workspace
		case "text":
			out.Text, value = v.Event.Text, v.Event.Text
		case "message":
			if v.Entry != nil {
				if out.Message == nil {
					out.Message = &model.EvidenceMessage{ToolCalls: []*model.EvidenceCall{}}
				}
				if err := projectEvidenceMessage(ctx, out.Message, v.Entry.Message, f.Selections); err != nil {
					return err
				}
			}
		}
		if err := charge(ctx, 0, value); err != nil {
			return err
		}
	}
	return nil
}

func projectEvidenceMessage(ctx context.Context, out *model.EvidenceMessage, m llm.Message, set ast.SelectionSet) error {
	if err := charge(ctx, 1); err != nil {
		return err
	}
	for _, f := range selected(ctx, set, "EvidenceMessage") {
		value := ""
		switch f.Name {
		case "role":
			out.Role, value = m.Role, m.Role
		case "content":
			out.Content, value = m.Content, m.Content
		case "thinking":
			out.Thinking, value = m.Thinking, m.Thinking
		case "reasoning":
			out.Reasoning, value = m.Reasoning, m.Reasoning
		case "reasoningContent":
			out.ReasoningContent, value = m.ReasoningContent, m.ReasoningContent
		case "toolCallId":
			out.ToolCallID, value = m.ToolCallID, m.ToolCallID
		case "name":
			out.Name, value = m.Name, m.Name
		case "toolCalls":
			if len(out.ToolCalls) == 0 {
				for range m.ToolCalls {
					out.ToolCalls = append(out.ToolCalls, &model.EvidenceCall{})
				}
			}
			for i, call := range m.ToolCalls {
				if err := charge(ctx, 1); err != nil {
					return err
				}
				for _, c := range selected(ctx, f.Selections, "EvidenceCall") {
					v := ""
					switch c.Name {
					case "id":
						out.ToolCalls[i].ID, v = call.ID, call.ID
					case "name":
						out.ToolCalls[i].Name, v = call.Name, call.Name
					case "arguments":
						out.ToolCalls[i].Arguments, v = call.Arguments, call.Arguments
					}
					if err := charge(ctx, 0, v); err != nil {
						return err
					}
				}
			}
		}
		if err := charge(ctx, 0, value); err != nil {
			return err
		}
	}
	return nil
}
