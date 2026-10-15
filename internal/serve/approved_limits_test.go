package serve

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Missing log parent guarantees no process can start, regardless of probe outcome.
// Reflection preserves compilation against the original non-variadic owner.
func TestApprovedProbeDeadlineAdmission(t *testing.T) {
	cancelled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-time.After(100 * time.Millisecond):
			fmt.Fprint(w, `{"data":[]}`)
		}
	}))
	defer srv.Close()
	r := Recipe{Name: "fixture", Kind: KindOpenAI, URL: srv.URL, Argv: []string{"true"}, localPath: "fixture"}
	f := reflect.ValueOf(StartContext)
	args := []reflect.Value{reflect.ValueOf(context.Background()), reflect.ValueOf(r), reflect.ValueOf(filepath.Join(t.TempDir(), "missing", "log"))}
	if f.Type().IsVariadic() {
		args = append(args, reflect.ValueOf(20*time.Millisecond))
	}
	result := f.Call(args)
	if !result[0].IsNil() {
		t.Fatal("unexpected process")
	}
	err, _ := result[1].Interface().(error)
	if err == nil || !strings.Contains(err.Error(), "cannot open log") {
		t.Fatalf("deadline admission=%v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("actual probe cancellation not observed")
	}
}
