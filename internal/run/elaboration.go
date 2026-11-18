package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"specific-harness/internal/launch"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

type elaborationReceipt struct {
	plan.Elaboration
	NextAction string `json:"next_action,omitempty"`
}

func elaborationDefinition(d launch.Deps, sessionRef string, catalog *prompts.Catalog) tool.Definition {
	pc := catalog
	if pc == nil || !pc.Has("tools.elaborate.description") {
		pc = prompts.Builtin()
	}
	param := func(name, typ string, required bool, enum ...string) tool.Param {
		return tool.Param{Name: name, Type: typ, Required: required, ExactEnum: len(enum) > 0, Enum: enum, AllowEmpty: name == "expected_previous_id", Desc: pc.Text("tools.elaborate.parameters." + name)}
	}
	return tool.Definition{
		Name: "elaborate", Prompts: pc, Desc: pc.Text("tools.elaborate.description"), Purpose: pc.Text("tools.elaborate.purpose"),
		Params: []tool.Param{
			param("action", tool.TypeString, true, "get", "set", "submit"),
			param("kind", tool.TypeString, false, "plan", "approach", "task"),
			param("id", tool.TypeString, false),
			param("elaboration_id", tool.TypeString, false),
			param("purpose", tool.TypeString, false),
			param("inputs", tool.TypeString, false),
			param("evidence", tool.TypeString, false),
			param("risks", tool.TypeString, false),
			param("expected_previous_id", tool.TypeString, false),
			param("request_decision", tool.TypeBool, false),
		},
		Example: tool.Args{"action": "get", "kind": "approach", "id": "<actual approach ID>"},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			if d.Store == nil || d.Store.Identity() == "" {
				return tool.Result{}, fmt.Errorf("elaborate needs the owning graph store")
			}
			ctx = plan.WithProject(ctx, d.Workspace)
			get := func(name string) string { value, _ := args[name].(string); return strings.TrimSpace(value) }
			action, kind, id := get("action"), get("kind"), get("id")
			requestDecision, _ := args["request_decision"].(bool)
			focus := func(item *plan.Elaboration) *tool.WorkflowScope {
				if item == nil {
					return nil
				}
				return &tool.WorkflowScope{Store: d.Store.Identity(), Kind: item.TargetKind, ID: item.TargetID}
			}
			receipt := func(item *plan.Elaboration, next string) (tool.Result, error) {
				raw, err := json.Marshal(elaborationReceipt{Elaboration: *item, NextAction: next})
				return tool.Result{Text: string(raw), Focus: focus(item)}, err
			}
			decide := func(item *plan.Elaboration) (*plan.Elaboration, string, error) {
				if item.Status == plan.ElaborationDraft {
					var err error
					item, err = plan.SubmitElaboration(ctx, d.Store, item.ID)
					if err != nil {
						return nil, "", err
					}
				}
				if item.Status != plan.ElaborationPending {
					return item, "This exact version already has a recorded decision.", nil
				}
				if d.Approve == nil {
					return item, "The exact version is pending. No user decision transport is wired; use /plan to approve it with a or request changes with x.", nil
				}
				prompt := fmt.Sprintf("Approve %s %s elaboration version %d?\nPurpose: %s\nInputs: %s\nCompletion evidence: %s\nRisks: %s", item.TargetKind, item.TargetID, item.Version, item.Purpose, item.Inputs, item.Evidence, item.Risks)
				approved := d.Approve(ctx, prompt)
				if err := ctx.Err(); err != nil {
					return nil, "", err
				}
				decided, err := plan.DecideElaboration(ctx, d.Store, item.ID, approved, "user")
				if err != nil {
					return nil, "", err
				}
				if approved {
					return decided, "The user approved this exact elaboration version.", nil
				}
				return decided, "The user requested changes. Create a new elaboration version with expected_previous_id set to this version ID.", nil
			}

			switch action {
			case "get":
				if requestDecision {
					return tool.Result{}, fmt.Errorf("action=get does not request a decision; use set or submit")
				}
				if elaborationID := get("elaboration_id"); elaborationID != "" {
					item, err := plan.GetElaboration(ctx, d.Store, elaborationID)
					if err != nil {
						return tool.Result{}, err
					}
					if kind != "" && kind != item.TargetKind || id != "" && id != item.TargetID {
						return tool.Result{}, fmt.Errorf("elaboration %s belongs to %s %s, not %s %s", elaborationID, item.TargetKind, item.TargetID, kind, id)
					}
					return receipt(item, "")
				}
				if kind == "" || id == "" {
					return tool.Result{}, fmt.Errorf("action=get needs kind and id, or elaboration_id")
				}
				versions, err := plan.ListElaborations(ctx, d.Store, kind, id)
				if err != nil {
					return tool.Result{}, err
				}
				var latest *plan.Elaboration
				if len(versions) > 0 {
					latest = &versions[len(versions)-1]
				}
				raw, err := json.Marshal(struct {
					TargetKind string             `json:"targetKind"`
					TargetID   string             `json:"targetId"`
					Latest     *plan.Elaboration  `json:"latest"`
					Versions   []plan.Elaboration `json:"versions"`
					NextAction string             `json:"next_action,omitempty"`
				}{kind, id, latest, versions, "Use the latest ID as expected_previous_id when creating the next version."})
				result := tool.Result{Text: string(raw)}
				if latest != nil {
					result.Focus = focus(latest)
				}
				return result, err
			case "set":
				if kind == "" || id == "" {
					return tool.Result{}, fmt.Errorf("action=set needs kind and the actual target id")
				}
				item, err := plan.CreateElaboration(ctx, d.Store, kind, id, sessionRef, get("purpose"), get("inputs"), get("evidence"), get("risks"), get("expected_previous_id"))
				if err != nil {
					return tool.Result{}, err
				}
				next := "Draft saved. Use action=submit when it is ready for user sign-off."
				if requestDecision {
					item, next, err = decide(item)
					if err != nil {
						return tool.Result{}, err
					}
				}
				return receipt(item, next)
			case "submit":
				var item *plan.Elaboration
				var err error
				if elaborationID := get("elaboration_id"); elaborationID != "" {
					item, err = plan.GetElaboration(ctx, d.Store, elaborationID)
				} else if kind != "" && id != "" {
					item, err = plan.LatestElaboration(ctx, d.Store, kind, id)
				} else {
					err = fmt.Errorf("action=submit needs elaboration_id, or kind and id")
				}
				if err != nil {
					if errors.Is(err, plan.ErrNotFound) {
						return tool.Result{}, fmt.Errorf("%w; use action=set first", err)
					}
					return tool.Result{}, err
				}
				next := "The exact version is pending user sign-off in /plan."
				if item.Status == plan.ElaborationDraft {
					item, err = plan.SubmitElaboration(ctx, d.Store, item.ID)
					if err != nil {
						return tool.Result{}, err
					}
				}
				if requestDecision {
					item, next, err = decide(item)
					if err != nil {
						return tool.Result{}, err
					}
				}
				return receipt(item, next)
			default:
				return tool.Result{}, fmt.Errorf("unknown elaborate action %q", action)
			}
		},
	}
}
