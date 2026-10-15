package agent

import (
	"errors"
	"specific-harness/internal/tool"
	"strings"
	"testing"
	"unicode/utf8"
)

// Fault-injected machine metadata controls, not model or filesystem attestations.
func TestCompletionMetadataUncertaintyAndDisplay(t *testing.T) {
	r := &Runner{CodingRequired: true}
	a, b, c := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	r.observeFile(tool.Result{Changed: true}, nil)
	if got := r.completionEvidence(); !strings.Contains(got, "publications=1 tracked=0 net=0 partial=true") {
		t.Fatal(got)
	}
	r.fileEvidence = fileEvidence{}
	r.observeFile(tool.Result{Changed: true, FileEffect: &tool.FileEffect{Path: "α\nquoted\"", BeforeHash: a, AfterHash: b}}, nil)
	r.observeFile(tool.Result{Changed: true, FileEffect: &tool.FileEffect{Path: "α\nquoted\"", BeforeHash: c, AfterHash: a}}, nil)
	got := r.completionEvidence()
	if !strings.Contains(got, "partial=true") || !strings.Contains(got, `"α\nquoted\"":reverted`) || strings.Contains(got, "α\nquoted") {
		t.Fatal(got)
	}
	r.observeFile(tool.Result{}, errors.New("unknown effect boundary"))
	if !r.fileEvidence.partial {
		t.Fatal("error was inventoried as complete")
	}
	long := strings.Repeat("界", 200) + "hidden tail"
	quoted := evidenceQuote(long)
	if !utf8.ValidString(quoted) || !strings.Contains(quoted, "display omitted") || strings.Contains(quoted, "hidden tail") || len([]rune(quoted)) > 200 {
		t.Fatal(quoted)
	}
}
