package decision

import (
	"strings"
	"testing"
)

func judgedVerdict() Verdict {
	return Verdict{Pick: 0, Option: "allow", Confidence: 0.755, Model: "typed-decisions", Device: "gpu"}
}

func judgedRequest() Request {
	return Request{Context: "The agent wants to run tool \"bash\" with cmd=ls.", Instructions: "Answer allow or deny.", Options: []string{"allow", "deny"}}
}

func TestLogJudgmentFormat(t *testing.T) {
	var kinds, texts []string
	log := func(kind, text string) { kinds, texts = append(kinds, kind), append(texts, text) }
	LogJudgment(log, SourceRiskyAuth, judgedRequest(), judgedVerdict(), "allowed", 500)
	if len(kinds) != 1 || kinds[0] != "judgment" {
		t.Fatalf("kinds=%v", kinds)
	}
	want := "source=risky-auth | pick=allow | conf=0.755 | resolution=allowed | options=allow; deny | context=The agent wants to run tool \"bash\" with cmd=ls."
	if texts[0] != want {
		t.Errorf("got:\n%s\nwant:\n%s", texts[0], want)
	}
}

func TestLogJudgmentTruncatesContext(t *testing.T) {
	var texts []string
	log := func(_, text string) { texts = append(texts, text) }
	req := judgedRequest()
	req.Context = "abcdefghij"
	LogJudgment(log, SourceRiskyAuth, req, judgedVerdict(), "allowed", 4)
	if !strings.HasSuffix(texts[0], "context=abcd…") {
		t.Errorf("text=%q", texts[0])
	}
	LogJudgment(log, SourceRiskyAuth, req, judgedVerdict(), "allowed", 0)
	if !strings.HasSuffix(texts[1], "context=abcdefghij") {
		t.Errorf("unlimited must keep whole context: %q", texts[1])
	}
}

func TestLogJudgmentNilLogSkips(t *testing.T) {
	LogJudgment(nil, SourceRiskyAuth, judgedRequest(), judgedVerdict(), "allowed", 500)
}

func TestParseJudgmentRoundTrip(t *testing.T) {
	var texts []string
	log := func(_, text string) { texts = append(texts, text) }
	// Context containing the separator must survive (context is last).
	req := judgedRequest()
	req.Context = "a | b"
	LogJudgment(log, SourceCoherence, req, Verdict{Option: "every task serves the goal", Confidence: 0.84}, "launched", 0)
	j, ok := ParseJudgment(texts[0])
	if !ok {
		t.Fatalf("parse failed: %q", texts[0])
	}
	if j.Source != SourceCoherence || j.Pick != "every task serves the goal" || j.Confidence != 0.84 || j.Resolution != "launched" {
		t.Errorf("parsed=%+v", j)
	}
}

func TestParseJudgmentRejectsMalformed(t *testing.T) {
	bad := []string{
		"not an event",
		"source=x | pick=y",
		"source=x | pick=y | conf=nope | resolution=z | options=a | context=c",
		"source=x | pick=y | conf=0.5 | resolution=z | bogus=a | context=c",
		"source=x | pick=y | conf=0.5 | resolution=z | options=a | body=c",
		"source= | pick=y | conf=0.5 | resolution=z | options=a | context=c",
	}
	for _, b := range bad {
		if _, ok := ParseJudgment(b); ok {
			t.Errorf("accepted %q", b)
		}
	}
}

func TestSummarizeCounts(t *testing.T) {
	js := []Judgment{
		{Source: SourceRiskyAuth, Pick: "allow", Confidence: 0.9, Resolution: "allowed"},
		{Source: SourceRiskyAuth, Pick: "deny", Confidence: 0.8, Resolution: "denied"},
		{Source: SourceLoopConfirm, Pick: "the agent is stuck in a loop and should stop", Confidence: 0.73, Resolution: "stopped"},
	}
	s := Summarize(js, []float64{0.5, 0.7, 0.9})
	if s.Total != 3 {
		t.Errorf("total=%d", s.Total)
	}
	st := s.BySource[SourceRiskyAuth]
	if st.Count != 2 || st.AvgConf < 0.849 || st.AvgConf > 0.851 {
		t.Errorf("risky-auth=%+v", st)
	}
	if s.ByResolution["allowed"] != 1 || s.ByResolution["denied"] != 1 || s.ByResolution["stopped"] != 1 {
		t.Errorf("resolutions=%v", s.ByResolution)
	}
	// 0.9 and 0.8 land >=0.9? No: 0.8 < 0.9. Buckets: <0.5, 0.5–0.7, 0.7–0.9, >=0.9.
	want := []int{0, 0, 2, 1}
	for i, b := range s.Buckets {
		if b.Count != want[i] {
			t.Errorf("bucket %s=%d, want %d", b.Label, b.Count, want[i])
		}
	}
	if s.Buckets[0].Label != "<0.5" || s.Buckets[1].Label != "0.5–0.7" || s.Buckets[3].Label != ">=0.9" {
		t.Errorf("labels=%v", s.Buckets)
	}
}

func TestSummarizeBucketEdges(t *testing.T) {
	// A confidence exactly on an edge belongs to the higher bucket.
	s := Summarize([]Judgment{{Source: "s", Pick: "p", Confidence: 0.7, Resolution: "r"}}, []float64{0.5, 0.7, 0.9})
	if s.Buckets[2].Count != 1 {
		t.Errorf("buckets=%v", s.Buckets)
	}
	empty := Summarize(nil, nil)
	if len(empty.Buckets) != 1 || empty.Buckets[0].Label != "all" || empty.Total != 0 {
		t.Errorf("empty=%+v", empty)
	}
}

func TestSummaryString(t *testing.T) {
	s := Summarize([]Judgment{
		{Source: "b-src", Pick: "x", Confidence: 0.6, Resolution: "r2"},
		{Source: "a-src", Pick: "y", Confidence: 0.8, Resolution: "r1"},
	}, []float64{0.7})
	out := s.String()
	want := "2 judgment(s)\nconfidence source unspecified: 2\nPooled scores: confidence sources are not comparable; unspecified provenance is unknown.\nsource a-src: 1 (avg conf 0.800)\nsource b-src: 1 (avg conf 0.600)\nresolution r1: 1\nresolution r2: 1\nconf <0.7: 1\nconf >=0.7: 1\n"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestJudgmentConfidenceProvenance(t *testing.T) {
	var js []Judgment
	for _, source := range []string{"model-self-report", ""} {
		v := judgedVerdict()
		v.ConfidenceSource = source
		LogJudgment(func(kind, text string) {
			if kind != KindJudgment {
				return
			}
			j, ok := ParseJudgment(text)
			if !ok {
				t.Fatalf("cannot parse %q", text)
			}
			if j.ConfidenceSource != source {
				t.Fatalf("provenance %q want %q", j.ConfidenceSource, source)
			}
			js = append(js, j)
		}, SourceRiskyAuth, judgedRequest(), v, "allowed", 0)
	}
	out := Summarize(js, []float64{0.5}).String()
	for _, want := range []string{"confidence source model-self-report: 1", "confidence source unspecified: 1", "not comparable"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}
