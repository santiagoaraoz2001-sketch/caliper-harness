package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"specific-harness/internal/decision"
	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
)

// ThinkingLoop detects consecutive exact byte units in one streamed text field.
// Field switches reset history; interleaved or nonexact loops are outside this detector.
type ThinkingLoop struct{ Repeat, MinUnitBytes, MaxUnitBytes, SampleBytes, MaxInputBytes int }

var thinkingLoopStop = errors.New("generation thinking loop stopped; explicit resume required")

func (p ThinkingLoop) validate() error {
	if p.Repeat < 0 || p.MinUnitBytes <= 0 || p.MaxUnitBytes < p.MinUnitBytes || p.SampleBytes <= 0 || p.MaxInputBytes <= 0 || p.Repeat > p.SampleBytes/p.MaxUnitBytes {
		return fmt.Errorf("invalid thinking loop policy: positive bounds and sample >= repeat * max unit required")
	}
	return nil
}

// thinkingContext owns no client state and never executes a partial tool call.
func (r *Runner) thinkingContext(ctx context.Context, log func(string, string) error, deferAdvice ...func(string)) (context.Context, error) {
	p := r.ThinkingLoop
	if p.Repeat == 0 {
		return ctx, nil
	}
	if err := ctx.Err(); err != nil {
		return ctx, err
	}
	if err := p.validate(); err != nil {
		return ctx, err
	}
	ring, field, count, used, next := make([]byte, p.SampleBytes), "", 0, 0, 0
	var cleared []byte
	position := 0
	at := func(back int) byte { return ring[(next-1-back+len(ring))%len(ring)] }
	observer := func(callCtx context.Context, d llm.AuxDelta) error {
		if err := callCtx.Err(); err != nil {
			return err
		}
		if d.Field != field {
			field, count, used, next, cleared, position = d.Field, 0, 0, 0, nil, 0
		}
		for i := 0; i < len(d.Delta); i++ {
			if err := callCtx.Err(); err != nil {
				return err
			}
			b := d.Delta[i]
			ring[next], next = b, (next+1)%len(ring)
			count++
			if used < len(ring) {
				used++
			}
			if len(cleared) > 0 {
				if b == cleared[position] {
					position = (position + 1) % len(cleared)
					continue
				}
				cleared = nil
			}
			unit := 0
			for n := p.MinUnitBytes; n <= p.MaxUnitBytes && n <= used/p.Repeat; n++ {
				matches := true
				for j := n; j < n*p.Repeat; j++ {
					if at(j) != at(j%n) {
						matches = false
						break
					}
				}
				if matches {
					unit = n
					break
				}
			}
			if unit == 0 {
				continue
			}
			sample := make([]byte, used)
			for j := range sample {
				sample[j] = at(used - 1 - j)
			}
			original, current, comments, taskComments := r.deliveredUserContext()
			phase := "ordinary generation"
			if r.Assigned != nil && r.PlanningActive() {
				phase = "unclaimed main preparation"
			}
			if r.settlement != nil {
				phase = "settled main bookkeeping"
			}
			claimed := false
			if a := r.Continuation.Assignment; a != nil {
				claimed = r.ClaimedTaskScope(&session.WorkflowScope{Store: a.Store, Kind: "task", ID: a.TaskID})
			}
			if claimed {
				phase = "claimed direct main execution"
			}
			encode := func(data []byte) (string, string) {
				if utf8.Valid(data) {
					return string(data), "utf8"
				}
				return base64.StdEncoding.EncodeToString(data), "base64"
			}
			unitValue, unitEncoding := encode(sample[len(sample)-unit:])
			coding, sameCommand := r.Continuation.Coding, false
			if coding != nil && strings.TrimSpace(coding.ConfigCommand) != "" && coding.ConfigCommand == coding.Command {
				view := *coding
				view.ConfigCommand = ""
				coding, sameCommand = &view, true
			}
			buildRequest := func(kept []byte) (decision.Request, error) {
				sampleValue, sampleEncoding := encode(kept)
				originalHash, originalBytes := "", 0
				if len(kept) < len(sample) {
					originalHash, originalBytes = fmt.Sprintf("%x", sha256.Sum256(sample)), len(sample)
				}
				raw, err := json.Marshal(struct {
					OriginalUserIntent, CurrentUserRequest, Phase, Field, SampleSHA256    string
					OriginalSampleSHA256                                                  string                 `json:",omitempty"`
					OriginalSampleBytes                                                   int                    `json:",omitempty"`
					DeliveredUserComments                                                 []deliveredUserComment `json:",omitempty"`
					DeliveredTaskComments                                                 []outcome.TaskComment  `json:",omitempty"`
					Assignment                                                            *session.Assignment
					ConfiguredCommandSameAsCommand                                        bool
					Claimed                                                               bool
					Coding                                                                *session.CodingState
					Repeat, ObservedBytes, OmittedBytes, UnitValueBytes, SampleValueBytes int
					UnitEncoding, SampleEncoding                                          string
				}{OriginalUserIntent: original, CurrentUserRequest: current, Phase: phase, Field: field, SampleSHA256: fmt.Sprintf("%x", sha256.Sum256(kept)), OriginalSampleSHA256: originalHash, OriginalSampleBytes: originalBytes,
					DeliveredUserComments: comments, DeliveredTaskComments: taskComments, Assignment: r.Continuation.Assignment,
					ConfiguredCommandSameAsCommand: sameCommand, Claimed: claimed, Coding: coding, Repeat: p.Repeat, ObservedBytes: count,
					OmittedBytes: count - len(kept), UnitValueBytes: len(unitValue), SampleValueBytes: len(sampleValue), UnitEncoding: unitEncoding, SampleEncoding: sampleEncoding})
				if err != nil {
					return decision.Request{}, fmt.Errorf("%w: cannot encode evidence: %v", thinkingLoopStop, err)
				}
				// Supply each value once, outside JSON escapes. Metadata lengths delimit
				// untrusted text even when it contains the section labels itself.
				evidence := string(raw) + "\nUnit:\n" + unitValue + "\nSample:\n" + sampleValue
				return decision.Request{Context: evidence, Instructions: r.Prompts.Text("decision.thinking_loop.instructions"), Options: []string{loopStuck, loopProgress}, MaxBytes: p.MaxInputBytes}, nil
			}
			req, err := buildRequest(sample)
			if err != nil {
				return err
			}
			encoded, err := json.Marshal(req)
			if cancel := callCtx.Err(); cancel != nil {
				return cancel
			}
			if err != nil {
				return fmt.Errorf("%w: cannot encode thinking judgment evidence: %v", thinkingLoopStop, err)
			}
			if log != nil {
				if err := log("thinking_loop", string(encoded)); err != nil {
					if cancel := callCtx.Err(); cancel != nil {
						return cancel
					}
					return err
				}
			}
			if cancel := callCtx.Err(); cancel != nil {
				return cancel
			}
			// A repetition judgment is advice, not permission to interrupt the
			// worker. Mark this exact unit attempted even when its judge is
			// unavailable so the next streamed byte does not repeat the call.
			cleared, position = append([]byte(nil), sample[len(sample)-unit:]...), 0
			failedAdvice := func(cause error) error {
				if err := callCtx.Err(); err != nil {
					return err
				}
				if log != nil {
					return log("judgment-error", fmt.Sprintf("source=generation-thinking error=%q", cause))
				}
				return nil
			}
			if r.Judge == nil {
				if err := failedAdvice(errors.New("no selected judge")); err != nil {
					return err
				}
				continue
			}
			v, err := r.Judge(callCtx, req)
			kept := sample
			for {
				if cancel := callCtx.Err(); cancel != nil {
					return cancel
				}
				var capacity *decision.ContextCapacityError
				if !errors.As(err, &capacity) || len(kept) <= unit*p.Repeat {
					break
				}
				// Only pre-inference admission may reduce optional judge evidence.
				// Keep the actual repetition and all intent/claim facts; neither
				// this projection nor its omissions alter the worker's stream.
				kept = kept[len(kept)-max(unit*p.Repeat, len(kept)/2):]
				req, err = buildRequest(kept)
				if err != nil {
					return err
				}
				encoded, err = json.Marshal(req)
				if err != nil {
					return err
				}
				if log != nil {
					if err := log("thinking_loop", string(encoded)); err != nil {
						if cancel := callCtx.Err(); cancel != nil {
							return cancel
						}
						return err
					}
				}
				if cancel := callCtx.Err(); cancel != nil {
					return cancel
				}
				v, err = r.Judge(callCtx, req)
			}
			if err != nil {
				if err := failedAdvice(err); err != nil {
					return err
				}
				continue
			}
			if v.Pick < 0 || v.Pick >= len(req.Options) || v.Option != req.Options[v.Pick] || math.IsNaN(v.Confidence) || v.Confidence < 0 || v.Confidence > 1 {
				if err := failedAdvice(errors.New("invalid decision verdict")); err != nil {
					return err
				}
				continue
			}
			resolution := "continued"
			if v.Confidence < advisoryConfidence {
				resolution = "failed-confidence"
			} else if v.Option == loopStuck {
				resolution = "deferred-guidance"
			}
			var saved error
			decision.LogJudgment(func(k, t string) {
				if saved == nil && log != nil {
					saved = log(k, t)
				}
			}, "generation-thinking", req, v, resolution, r.JudgmentContextChars)
			if cancel := callCtx.Err(); cancel != nil {
				return cancel
			}
			if saved != nil {
				return saved
			}
			if resolution == "deferred-guidance" && len(deferAdvice) > 0 && deferAdvice[0] != nil {
				deferAdvice[0](r.Prompts.Text("continuation.discovery.reconsider"))
			}
		}
		return callCtx.Err()
	}
	return llm.WithAuxObserver(ctx, observer), nil
}
