package decision

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Judgment event vocabulary. Sources and resolutions are free strings on
// purpose: the summary counts distinct values, so new gates need no enum
// edits. These constants keep the four wired sources spelled one way.
const (
	KindJudgment      = "judgment"
	SourceRiskyAuth   = "risky-auth"
	SourceCoherence   = "coherence"
	SourceLoopConfirm = "loop-confirm"
	SourceJudgeTool   = "judge-tool"
)

// LogJudgment records one verdict at resolution time: the verdict plus what
// the harness did with it. That pair is the calibration dataset for the
// risk threshold. A nil log skips silently (sessionless runs); maxCtx <= 0
// leaves the context whole, else it cuts at maxCtx runes.
// Format is key=value pairs joined by " | ", context last so it may
// contain the separator. ParseJudgment reads it back.
func LogJudgment(log func(kind, text string), source string, req Request, v Verdict, resolution string, maxCtx int) {
	if log == nil {
		return
	}
	ctx := req.Context
	if maxCtx > 0 && len([]rune(ctx)) > maxCtx {
		ctx = string([]rune(ctx)[:maxCtx]) + "…"
	}
	log(KindJudgment, fmt.Sprintf("source=%s | pick=%s | conf=%.3f | resolution=%s | options=%s | context=%s",
		source, v.Option, v.Confidence, resolution, strings.Join(req.Options, "; "), ctx))
}

// Judgment is one parsed judgment event.
type Judgment struct {
	Source     string
	Pick       string
	Confidence float64
	Resolution string
}

// ParseJudgment reads back LogJudgment's format. Malformed lines fail
// closed (false) so the summary skips rather than invents verdicts.
func ParseJudgment(text string) (Judgment, bool) {
	parts := strings.SplitN(text, " | ", 6)
	if len(parts) != 6 {
		return Judgment{}, false
	}
	var j Judgment
	for _, p := range parts[:5] {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return Judgment{}, false
		}
		switch k {
		case "source":
			j.Source = v
		case "pick":
			j.Pick = v
		case "conf":
			c, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return Judgment{}, false
			}
			j.Confidence = c
		case "resolution":
			j.Resolution = v
		case "options":
		default:
			return Judgment{}, false
		}
	}
	if !strings.HasPrefix(parts[5], "context=") || j.Source == "" || j.Pick == "" || j.Resolution == "" {
		return Judgment{}, false
	}
	return j, true
}

// SourceStat aggregates one source: verdict count plus mean confidence.
type SourceStat struct {
	Count   int
	AvgConf float64
}

// Bucket is one confidence histogram bar over ascending edges.
type Bucket struct {
	Label string
	Count int
}

// Summary aggregates parsed judgments for threshold calibration.
type Summary struct {
	Total        int
	BySource     map[string]SourceStat
	ByResolution map[string]int
	Buckets      []Bucket
}

// Summarize folds judgments into counts plus a confidence histogram over
// edges (ascending cut points; empty edges yield one "all" bucket).
func Summarize(js []Judgment, edges []float64) Summary {
	s := Summary{BySource: map[string]SourceStat{}, ByResolution: map[string]int{}}
	labels := bucketLabels(edges)
	s.Buckets = make([]Bucket, len(labels))
	for i, l := range labels {
		s.Buckets[i].Label = l
	}
	for _, j := range js {
		s.Total++
		st := s.BySource[j.Source]
		st.AvgConf = (st.AvgConf*float64(st.Count) + j.Confidence) / float64(st.Count+1)
		st.Count++
		s.BySource[j.Source] = st
		s.ByResolution[j.Resolution]++
		s.Buckets[bucketOf(j.Confidence, edges)].Count++
	}
	return s
}

func bucketLabels(edges []float64) []string {
	if len(edges) == 0 {
		return []string{"all"}
	}
	out := []string{"<" + strconv.FormatFloat(edges[0], 'g', -1, 64)}
	for i := 1; i < len(edges); i++ {
		out = append(out, strconv.FormatFloat(edges[i-1], 'g', -1, 64)+"–"+strconv.FormatFloat(edges[i], 'g', -1, 64))
	}
	return append(out, ">="+strconv.FormatFloat(edges[len(edges)-1], 'g', -1, 64))
}

func bucketOf(c float64, edges []float64) int {
	for i, e := range edges {
		if c < e {
			return i
		}
	}
	return len(edges)
}

// String renders the summary for the judgments command: totals, then
// per-source counts with mean confidence, resolutions, and the
// histogram. Sorted for stable output.
func (s Summary) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d judgment(s)\n", s.Total)
	sources := make([]string, 0, len(s.BySource))
	for k := range s.BySource {
		sources = append(sources, k)
	}
	sort.Strings(sources)
	for _, k := range sources {
		fmt.Fprintf(&b, "source %s: %d (avg conf %.3f)\n", k, s.BySource[k].Count, s.BySource[k].AvgConf)
	}
	res := make([]string, 0, len(s.ByResolution))
	for k := range s.ByResolution {
		res = append(res, k)
	}
	sort.Strings(res)
	for _, k := range res {
		fmt.Fprintf(&b, "resolution %s: %d\n", k, s.ByResolution[k])
	}
	for _, bk := range s.Buckets {
		fmt.Fprintf(&b, "conf %s: %d\n", bk.Label, bk.Count)
	}
	return b.String()
}
