package plan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"specific-harness/internal/store"
)

// Elaboration statuses. Content is immutable after creation; only status and
// decision provenance change on an existing version.
const (
	ElaborationDraft            = "draft"
	ElaborationPending          = "pending"
	ElaborationApproved         = "approved"
	ElaborationChangesRequested = "changes_requested"
)

// Elaboration is one immutable explanation version attached to a Plan,
// Approach, or Task in the captured project.
type Elaboration struct {
	ID          string    `json:"id"`
	TargetKind  string    `json:"targetKind"`
	TargetID    string    `json:"targetId"`
	ProjectRoot string    `json:"projectRoot"`
	Version     int       `json:"version"`
	PreviousID  string    `json:"previousId"`
	Status      string    `json:"status"`
	Purpose     string    `json:"purpose"`
	Inputs      string    `json:"inputs"`
	Evidence    string    `json:"evidence"`
	Risks       string    `json:"risks"`
	SessionRef  string    `json:"sessionRef"`
	DecisionBy  string    `json:"decisionBy,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	DecidedAt   time.Time `json:"decidedAt,omitempty"`
}

func elaborationProjectRoot(s stateStore, kind, id string) (string, error) {
	switch kind {
	case "plan":
		p, err := GetPlan(s, id)
		if err != nil {
			return "", err
		}
		return p.ProjectRoot, nil
	case "approach":
		p, err := ApproachPlan(s, id)
		if err != nil {
			return "", err
		}
		return p.ProjectRoot, nil
	case "task":
		a, err := taskApproach(s, id)
		if err != nil {
			return "", err
		}
		p, err := ApproachPlan(s, a.ID)
		if err != nil {
			return "", err
		}
		return p.ProjectRoot, nil
	default:
		return "", fmt.Errorf("elaboration target kind must be plan, approach, or task, got %q", kind)
	}
}

func checkElaborationTarget(ctx context.Context, s stateStore, kind, id string) (string, error) {
	root, err := elaborationProjectRoot(s, kind, id)
	if err != nil {
		return "", err
	}
	if err := CheckProject(s, kind, id, ProjectRoot(ctx)); err != nil {
		return "", err
	}
	if captured := ProjectRoot(ctx); captured != "" {
		return captured, nil
	}
	return root, nil
}

func elaborationOf(row map[string]any) (Elaboration, error) {
	created, ok := row["created_at"].(time.Time)
	version, _ := row["version"].(int64)
	out := Elaboration{
		ID: stringField(row, "id"), TargetKind: stringField(row, "target_kind"), TargetID: stringField(row, "target_id"),
		ProjectRoot: stringField(row, "project_root"), Version: int(version), PreviousID: stringField(row, "previous_id"),
		Status: stringField(row, "status"), Purpose: stringField(row, "purpose"), Inputs: stringField(row, "inputs"),
		Evidence: stringField(row, "evidence"), Risks: stringField(row, "risks"), SessionRef: stringField(row, "session_ref"),
		DecisionBy: stringField(row, "decision_by"), CreatedAt: created,
	}
	if decided, ok := row["decided_at"].(time.Time); ok {
		out.DecidedAt = decided
	}
	if out.ID == "" || !ok || out.Version < 1 {
		return Elaboration{}, fmt.Errorf("elaboration row has wrong shape: %v", row)
	}
	return out, nil
}

const elaborationFields = `e.id AS id, e.target_kind AS target_kind, e.target_id AS target_id, e.project_root AS project_root, e.version AS version, e.previous_id AS previous_id, e.status AS status, e.purpose AS purpose, e.inputs AS inputs, e.evidence AS evidence, e.risks AS risks, e.session_ref AS session_ref, e.decision_by AS decision_by, e.created_at AS created_at, e.decided_at AS decided_at`

func latestElaboration(s stateStore, kind, id string) (*Elaboration, error) {
	rows, err := s.Query(`MATCH (e:Elaboration) WHERE e.target_kind=$kind AND e.target_id=$id RETURN `+elaborationFields+` ORDER BY version DESC LIMIT 1`,
		map[string]any{"kind": kind, "id": id})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out, err := elaborationOf(rows[0])
	return &out, err
}

// CreateElaboration appends one immutable draft version. expectedPreviousID
// must equal the current latest version ID, or be empty for version 1.
func CreateElaboration(ctx context.Context, s *store.Store, kind, id, sessionRef, purpose, inputs, evidence, risks, expectedPreviousID string) (*Elaboration, error) {
	for name, value := range map[string]string{"sessionRef": sessionRef, "purpose": purpose, "inputs": inputs, "evidence": evidence, "risks": risks} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("elaboration %s must not be blank", name)
		}
	}
	var out *Elaboration
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		root, err := checkElaborationTarget(ctx, tx, kind, id)
		if err != nil {
			return err
		}
		latest, err := latestElaboration(tx, kind, id)
		if err != nil {
			return err
		}
		previousID, version := "", 1
		if latest != nil {
			previousID, version = latest.ID, latest.Version+1
		}
		if expectedPreviousID != previousID {
			return fmt.Errorf("stale elaboration previous ID for %s %s: got %q, current %q; read the latest version again", kind, id, expectedPreviousID, previousID)
		}
		out = &Elaboration{ID: uuid.NewString(), TargetKind: kind, TargetID: id, ProjectRoot: root, Version: version, PreviousID: previousID,
			Status: ElaborationDraft, Purpose: purpose, Inputs: inputs, Evidence: evidence, Risks: risks, SessionRef: sessionRef, CreatedAt: time.Now().UTC()}
		return tx.Write(`CREATE (e:Elaboration {id:$id,target_kind:$kind,target_id:$target,project_root:$root,version:$version,previous_id:$previous,status:$status,purpose:$purpose,inputs:$inputs,evidence:$evidence,risks:$risks,session_ref:$session,decision_by:"",created_at:$created})`,
			map[string]any{"id": out.ID, "kind": kind, "target": id, "root": root, "version": int64(version), "previous": previousID, "status": out.Status,
				"purpose": purpose, "inputs": inputs, "evidence": evidence, "risks": risks, "session": sessionRef, "created": out.CreatedAt})
	})
	return out, err
}

// ListElaborations returns every version for one target in version order.
func ListElaborations(ctx context.Context, s *store.Store, kind, id string) ([]Elaboration, error) {
	if _, err := checkElaborationTarget(ctx, s, kind, id); err != nil {
		return nil, err
	}
	rows, err := s.QueryContext(ctx, `MATCH (e:Elaboration) WHERE e.target_kind=$kind AND e.target_id=$id RETURN `+elaborationFields+` ORDER BY version`,
		map[string]any{"kind": kind, "id": id})
	if err != nil {
		return nil, err
	}
	out := make([]Elaboration, 0, len(rows))
	for _, row := range rows {
		item, err := elaborationOf(row)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// ListAllElaborations returns every saved version in target/version order.
// It is the one-query read used by whole-tree displays.
func ListAllElaborations(ctx context.Context, s *store.Store) ([]Elaboration, error) {
	rows, err := s.QueryContext(ctx, `MATCH (e:Elaboration) RETURN `+elaborationFields+` ORDER BY target_kind, target_id, version`, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Elaboration, 0, len(rows))
	for _, row := range rows {
		item, err := elaborationOf(row)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// LatestElaboration returns the newest version for one target.
func LatestElaboration(ctx context.Context, s *store.Store, kind, id string) (*Elaboration, error) {
	if _, err := checkElaborationTarget(ctx, s, kind, id); err != nil {
		return nil, err
	}
	out, err := latestElaboration(s, kind, id)
	if err == nil && out == nil {
		err = fmt.Errorf("no elaboration for %s %q: %w", kind, id, ErrNotFound)
	}
	return out, err
}

// GetElaboration loads one exact version by its own ID.
func GetElaboration(ctx context.Context, s *store.Store, elaborationID string) (*Elaboration, error) {
	rows, err := s.QueryContext(ctx, `MATCH (e:Elaboration) WHERE e.id=$id RETURN `+elaborationFields, map[string]any{"id": elaborationID})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no elaboration %q: %w", elaborationID, ErrNotFound)
	}
	out, err := elaborationOf(rows[0])
	if err != nil {
		return nil, err
	}
	if _, err := checkElaborationTarget(ctx, s, out.TargetKind, out.TargetID); err != nil {
		return nil, err
	}
	return &out, nil
}

func transitionElaboration(ctx context.Context, s *store.Store, elaborationID, from, to, decisionBy string) (*Elaboration, error) {
	var out *Elaboration
	err := s.UpdateContext(ctx, func(tx *store.Tx) error {
		rows, err := tx.Query(`MATCH (e:Elaboration) WHERE e.id=$id RETURN `+elaborationFields, map[string]any{"id": elaborationID})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return fmt.Errorf("no elaboration %q: %w", elaborationID, ErrNotFound)
		}
		current, err := elaborationOf(rows[0])
		if err != nil {
			return err
		}
		if _, err := checkElaborationTarget(ctx, tx, current.TargetKind, current.TargetID); err != nil {
			return err
		}
		if current.Status != from {
			return fmt.Errorf("elaboration %s is %s; this action needs %s", elaborationID, current.Status, from)
		}
		current.Status = to
		params := map[string]any{"id": elaborationID, "status": to}
		query := `MATCH (e:Elaboration) WHERE e.id=$id SET e.status=$status`
		if to == ElaborationApproved || to == ElaborationChangesRequested {
			current.DecisionBy = decisionBy
			current.DecidedAt = time.Now().UTC()
			params["by"] = decisionBy
			params["decided"] = current.DecidedAt
			query += `, e.decision_by=$by, e.decided_at=$decided`
		}
		if err := tx.Write(query, params); err != nil {
			return err
		}
		out = &current
		return nil
	})
	return out, err
}

// SubmitElaboration marks one draft version ready for a user decision.
func SubmitElaboration(ctx context.Context, s *store.Store, elaborationID string) (*Elaboration, error) {
	return transitionElaboration(ctx, s, elaborationID, ElaborationDraft, ElaborationPending, "")
}

// DecideElaboration records the user's decision on one pending version. It is
// a host/user operation, not a model self-approval operation.
func DecideElaboration(ctx context.Context, s *store.Store, elaborationID string, approved bool, decisionBy string) (*Elaboration, error) {
	if strings.TrimSpace(decisionBy) == "" {
		return nil, fmt.Errorf("elaboration decision needs decision provenance")
	}
	to := ElaborationChangesRequested
	if approved {
		to = ElaborationApproved
	}
	return transitionElaboration(ctx, s, elaborationID, ElaborationPending, to, decisionBy)
}
