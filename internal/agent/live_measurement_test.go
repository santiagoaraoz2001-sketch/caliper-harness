package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"specific-harness/internal/llm"
	"specific-harness/internal/outcome"
	"specific-harness/internal/session"
	"specific-harness/internal/tool"
)

func measurementEvents(t *testing.T, sink *recordSink) []session.Continuation {
	t.Helper()
	var readings []session.Continuation
	for i, kind := range sink.kinds {
		if kind != "context" {
			continue
		}
		var c session.Continuation
		if err := json.Unmarshal([]byte(sink.texts[i]), &c); err != nil || len(c.Entries) != 0 || c.Coding != nil || c.Revision != 0 || c.BasisChars != 0 || c.BasisTokens != 0 {
			t.Fatal("measurement event replaced full conversation/calibration", c, err)
		}
		readings = append(readings, c)
	}
	if len(readings) == 0 {
		t.Fatal("Runner did not publish any live context event")
	}
	return readings
}

type measurementPartialModel struct{ first *scriptModel }

func (m measurementPartialModel) Chat(ctx context.Context, msgs []llm.Message) (string, string, llm.Usage, error) {
	if len(m.first.seen) == 0 {
		return m.first.Chat(ctx, msgs)
	}
	return interruptedModel{text: "partial cannot complete", usage: llm.Usage{In: 19, Out: 7, Reported: true}, err: io.ErrUnexpectedEOF}.Chat(ctx, msgs)
}

func TestLiveMeasurementMissingAndPartialRemainUnknown(t *testing.T) {
	for _, partial := range []bool{false, true} {
		sink := &recordSink{}
		var calls []string
		reg := tool.NewRegistry()
		reg.Register(echoTool(&calls))
		model := &scriptModel{replies: []string{echoBlock("observed"), "FINAL"}, usages: []llm.Usage{{In: 100, Out: 4, Reported: true}, {}}}
		var m Model = model
		if partial {
			m = measurementPartialModel{model}
		}
		r := &Runner{Model: m, ModelKey: "fixture", Registry: reg, Session: sink, MaxSteps: 2}
		_, _, err := r.Run(context.Background(), "ORIGINAL")
		if partial && !errors.Is(err, io.ErrUnexpectedEOF) || !partial && err != nil {
			t.Fatal("transport error changed", err)
		}
		cs := measurementEvents(t, sink)
		if cs[len(cs)-1].Measured || r.Continuation.Measured {
			t.Fatal("partial/unreported response became complete measurement")
		}
		if partial {
			if r.UsedIn != 119 || r.UsedOut != 11 || len(calls) != 1 || len(cs) != 3 || !cs[1].Measured {
				t.Fatal("partial cost/effects changed")
			}
		} else if r.UsedIn != 100 || !r.UsageUnknown || len(cs) != 3 || !cs[1].Measured || cs[1].Prompt != 100 {
			t.Fatal("missing usage invented totals or lost first reading", cs)
		}
	}
}

func TestLiveMeasurementMutationBoundaries(t *testing.T) {
	for _, mutation := range []string{"btw", "prune", "compact"} {
		t.Run(mutation, func(t *testing.T) {
			sink := &recordSink{}
			var calls []string
			reg := tool.NewRegistry()
			reg.Register(echoTool(&calls))
			model := &scriptModel{replies: []string{echoBlock(strings.Repeat("x", 5000)), echoBlock("b"), "FINAL"}, usages: []llm.Usage{{In: 20, Out: 5, Reported: true}, {In: 950, Out: 5, Reported: true}, {In: 100, Out: 5, Reported: true}}}
			r := &Runner{Model: model, ModelKey: "fixture", Registry: reg, Session: sink, MaxSteps: 3, MaxTokens: 10000}
			if mutation == "btw" {
				heard := false
				r.Btw = func() string {
					if len(calls) > 0 && !heard {
						heard = true
						return "HEARD NOTE"
					}
					return ""
				}
			} else if mutation == "prune" {
				r.Prune.OutputsAgeTokens = 1
				// Pruning now batches at existing pressure/role boundaries.
				// Keep this a measurement-invalidation test by reaching 90%.
				r.Compaction.Trigger = 90
				model.usages[1].In = 9500
			} else {
				model.replies = []string{echoBlock(strings.Repeat("x", 5000)), echoBlock("b"), "Observed source read; verification pending.", "FINAL"}
				model.usages = []llm.Usage{{In: 20, Out: 5, Reported: true}, {In: 1750, Out: 5, Reported: true}, {In: 20, Out: 5, Reported: true}, {In: 100, Out: 5, Reported: true}}
				r = compactRunner(model, sink, testTemplateDir(t))
				r.ModelKey = "fixture"
				r.Compaction.Keep = 5
			}
			if _, _, err := r.Run(context.Background(), "ORIGINAL"); err != nil {
				t.Fatal(err)
			}
			cs := measurementEvents(t, sink)
			knownThenUnknown := false
			for i := 1; i < len(cs); i++ {
				knownThenUnknown = knownThenUnknown || cs[i-1].Measured && !cs[i].Measured
			}
			if !knownThenUnknown || !cs[len(cs)-1].Measured || cs[len(cs)-1].Prompt != 100 {
				t.Fatal("mutation did not invalidate then remeasure", cs)
			}
			found := false
			for _, k := range sink.kinds {
				found = found || k == mutation || mutation == "btw" && k == "btw_committed"
			}
			if !found {
				t.Fatal("control did not exercise actual mutation", mutation, sink.kinds)
			}
			var events []session.Event
			for i, k := range sink.kinds {
				events = append(events, session.Event{Kind: k, Text: sink.texts[i]})
			}
			restored, _, err := session.Restore(events, 10000, r.ModelKey)
			without := events[:0:0]
			for _, e := range events {
				if e.Kind != "context" {
					without = append(without, e)
				}
			}
			control, _, controlErr := session.Restore(without, 10000, r.ModelKey)
			if err != nil || controlErr != nil || !reflect.DeepEqual(restored, control) {
				t.Fatal("measurement projection polluted persisted continuation", err, controlErr)
			}
		})
	}
}

type measurementWriteFailure struct{ cause error }

func (s measurementWriteFailure) Append(_ context.Context, _, kind, _ string) error {
	if kind == "context" {
		return s.cause
	}
	return nil
}

func TestLiveMeasurementRecordingFailureRemainsLoud(t *testing.T) {
	cause := errors.New("context receipt write failed")
	r := &Runner{Model: &scriptModel{replies: []string{"FINAL"}, usage: llm.Usage{In: 100, Reported: true}}, Registry: tool.NewRegistry(), Session: measurementWriteFailure{cause}, MaxSteps: 1}
	if _, _, err := r.Run(context.Background(), "ORIGINAL"); !errors.Is(err, cause) || r.Outcome.Status != outcome.Failed {
		t.Fatal("recording failure became successful", err, r.Outcome)
	}
}
