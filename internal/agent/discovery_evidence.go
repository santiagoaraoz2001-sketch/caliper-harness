package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"specific-harness/internal/tool"
)

// Source coverage means bytes were delivered, never understood or verified.
// Hashes identify observed versions; these facts do not assert current disk state.
type sourceCoverage struct {
	Tool, Path, FileSHA256 string
	LastCallID             string `json:",omitempty"`
	Ranges                 [][2]int
	EOFLine                *int `json:",omitempty"`
	CompleteRead           bool
	Reads                  int
}

type repeatedObservation struct {
	Tool, ArgumentsSHA256, OutputSHA256 string
	Args                                tool.Args
	LastCallID                          string `json:",omitempty"`
	Occurrences                         int
}

type discoveryEvidence struct {
	Sources              []sourceCoverage
	RepeatedObservations []repeatedObservation
	OmittedFacts         int
}

type discoveryFact struct {
	source *sourceCoverage
	query  *repeatedObservation
	key    [32]byte
	count  int
}

// This is an evictable advisory cache within the existing watcher, not another
// execution ledger. Raw receipts remain authoritative. Its memory allowance is
// the configured advisory input budget; eviction means unknown, not novelty.
type discoveryHistory struct {
	facts   []discoveryFact
	omitted int
}

func sourceReceipt(note *observed) (SourceCheckpoint, *sourceCoverage) {
	point := SourceCheckpoint{Tool: note.Tool, Range: "unknown", FileSHA256: "unknown", ReceiptSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(note.Result.Text)))}
	point.Path, _ = note.Args["path"].(string)
	if note.Error != "" || note.Result.Failed || note.Result.Blocked || note.Tool != "read" && note.Tool != "read_outside" {
		return point, nil
	}
	header, _, _ := strings.Cut(note.Result.Text, "\n")
	path, hash, found := strings.Cut(header, " sha256=")
	decoded, err := hex.DecodeString(hash)
	if !found || err != nil || len(decoded) != sha256.Size {
		return point, nil
	}
	point.Path, point.FileSHA256 = filepath.Clean(path), hash
	start := strings.LastIndex(note.Result.Text, "\n[")
	if start < 0 || !strings.HasSuffix(note.Result.Text, "; metadata not source lines]") {
		return point, nil
	}
	fields := map[string]string{}
	for _, part := range strings.Split(note.Result.Text[start+2:], "; ") {
		key, value, ok := strings.Cut(part, "=")
		if ok {
			fields[key] = value
		}
	}
	span, eof, truncated := fields["shown_range"], fields["EOF"], fields["truncated"]
	point.Range = span
	if outline, _ := note.Args["outline"].(bool); outline || fields["selection_complete"] != "" {
		return point, nil // Selected definitions do not establish complete source delivery.
	}
	if span == "" || (eof != "true" && eof != "false") || (truncated != "true" && truncated != "false") {
		return point, nil
	}
	coverage := &sourceCoverage{Tool: note.Tool, Path: point.Path, FileSHA256: hash, LastCallID: note.CallID, Reads: 1}
	if span != "none" {
		first, last, _ := strings.Cut(span, "-")
		lo, err1 := strconv.Atoi(first)
		hi, err2 := strconv.Atoi(last)
		if err1 != nil || err2 != nil || lo < 1 || hi < lo {
			return point, nil
		}
		coverage.Ranges = [][2]int{{lo, hi}}
		if eof == "true" && truncated == "false" && !note.Result.Truncated {
			coverage.EOFLine = &hi
		}
	} else if eof == "true" && truncated == "false" && !note.Result.Truncated {
		// An empty read past EOF does not prove the file is empty.
		if strings.HasPrefix(note.Result.Text[start+2:], "0 lines shown from line 1;") {
			zero := 0
			coverage.EOFLine = &zero
		}
	}
	coverage.merge(nil)
	return point, coverage
}

func (s *sourceCoverage) merge(previous *sourceCoverage) {
	if previous != nil {
		s.Reads += previous.Reads
		s.Ranges = append(s.Ranges, previous.Ranges...)
		if s.EOFLine == nil {
			s.EOFLine = previous.EOFLine
		}
	}
	sort.Slice(s.Ranges, func(i, j int) bool { return s.Ranges[i][0] < s.Ranges[j][0] })
	merged := s.Ranges[:0]
	for _, span := range s.Ranges {
		if len(merged) > 0 && span[0] <= merged[len(merged)-1][1]+1 {
			merged[len(merged)-1][1] = max(merged[len(merged)-1][1], span[1])
		} else {
			merged = append(merged, span)
		}
	}
	s.Ranges = merged
	s.CompleteRead = s.EOFLine != nil && (*s.EOFLine == 0 || len(merged) == 1 && merged[0][0] == 1 && merged[0][1] == *s.EOFLine)
}

func (h *discoveryHistory) record(note *observed, budget int) {
	// Any known file effect, or shell command with unbounded effects, ends the
	// comparison epoch. Identical output after that boundary is not a duplicate.
	if note.Result.FileEffect != nil || note.Result.Changed || note.Tool == "bash" {
		h.omitted += len(h.facts)
		h.facts = nil
	}
	if budget <= 0 || note.Error != "" || note.Result.Failed || note.Result.Blocked || note.Result.ExitCode != nil && *note.Result.ExitCode != 0 {
		return
	}
	switch note.Tool {
	case "read", "read_outside", "grep", "find", "find_outside", "find_file", "find_dir":
	default:
		return
	}
	_, source := sourceReceipt(note)
	if source != nil {
		for i := 0; i < len(h.facts); i++ {
			prior := h.facts[i].source
			if prior != nil && prior.Tool == source.Tool && prior.Path == source.Path {
				if prior.FileSHA256 == source.FileSHA256 {
					source.merge(prior)
				} else {
					// A known version change invalidates cross-call comparisons.
					h.omitted += len(h.facts)
					h.facts = nil
					break
				}
				h.facts = append(h.facts[:i], h.facts[i+1:]...)
				break
			}
		}
		h.facts = append(h.facts, discoveryFact{source: source})
	} else {
		args, _ := json.Marshal(note.Args)
		query := repeatedObservation{Tool: note.Tool, Args: note.Args, LastCallID: note.CallID, ArgumentsSHA256: fmt.Sprintf("%x", sha256.Sum256(args)), OutputSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(note.Result.Text))), Occurrences: 1}
		identity, _ := json.Marshal(struct {
			Tool, Arguments, Output string
			Truncated, NoMatches    bool
		}{query.Tool, query.ArgumentsSHA256, query.OutputSHA256, note.Result.Truncated, note.Result.NoMatches})
		key := sha256.Sum256(identity)
		for i, prior := range h.facts {
			if prior.source == nil && prior.key == key {
				query.Occurrences += prior.count
				h.facts = append(h.facts[:i], h.facts[i+1:]...)
				break
			}
		}
		fact := discoveryFact{key: key, count: query.Occurrences}
		if query.Occurrences > 1 {
			fact.query = &query
		}
		// A first query needs only its exact identity digest. Its full locator is
		// already in recent receipts and is captured here when recurrence exists.
		h.facts = append(h.facts, fact)
	}
	// Count actual encoded fact bytes, including unique paths and range lists.
	// Source bodies stay in settled receipts. Exact recurring query arguments
	// are retained here so older repeats remain actionable after window eviction.
	for len(h.facts) > 0 {
		sourceBytes, queryBytes := 0, 0
		for _, fact := range h.facts {
			if fact.source != nil {
				raw, _ := json.Marshal(fact.source)
				sourceBytes += len(raw)
			} else {
				queryBytes += 2*sha256.Size + 8 // digest and occurrence count
				if fact.query != nil {
					raw, _ := json.Marshal(fact.query)
					queryBytes += len(raw)
				}
			}
		}
		if sourceBytes+queryBytes <= budget {
			break
		}
		// Query churn must not erase the initial complete source evidence.
		// Half the existing allowance remains available to source provenance.
		removeSource := sourceBytes > budget/2
		for i, fact := range h.facts {
			if (fact.source != nil) == removeSource {
				h.facts = append(h.facts[:i], h.facts[i+1:]...)
				break
			}
		}
		h.omitted++
	}
}

func (h *discoveryHistory) evidence() discoveryEvidence {
	evidence := discoveryEvidence{OmittedFacts: h.omitted}
	for _, fact := range h.facts {
		if fact.source != nil {
			evidence.Sources = append(evidence.Sources, *fact.source)
		}
		if fact.query != nil && fact.query.Occurrences > 1 {
			evidence.RepeatedObservations = append(evidence.RepeatedObservations, *fact.query)
		}
	}
	return evidence
}
