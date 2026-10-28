package ask

import (
	"reflect"
	"testing"
)

func TestReservedFallbackNormalizedWithoutChangingAnswers(t *testing.T) {
	want := []string{"Alpha", "Beta, with detail", "Gamma", NoneOfAbove}
	for _, input := range []string{"Alpha\nBeta, with detail\nGamma", "None of the above\nAlpha\nBeta, with detail\nGamma\n none OF the ABOVE "} {
		if got := parseOptions(input); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v", got)
		}
	}
	if got := parseOptions("A\nB\nC\nD\nNone of the above"); len(got) != MaxOptions+2 {
		t.Fatal("four real choices silently truncated")
	}
}
