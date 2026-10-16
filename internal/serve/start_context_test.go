package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestD09CancelledNativePreflightDoesNotSpawn(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		c, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		c.Close()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		p   *Proc
		err error
	}
	done := make(chan result, 1)
	// Private path binding drives real preflight; executable is a neutral owned sleep,
	// not MLX. No weights/native inference occur in this lifecycle control.
	go func() {
		p, err := StartContext(ctx, Recipe{Name: "owned-neutral-native-control", Argv: napCmd("20"), URL: srv.URL + "/v1", localPath: "component-path"}, filepath.Join(t.TempDir(), "log"))
		done <- result{p, err}
	}()
	<-entered
	cancel()
	close(release)
	select {
	case got := <-done:
		if got.p != nil {
			got.p.Stop()
			t.Fatal("cancelled preflight spawned owned process")
		}
		if got.err == nil {
			t.Fatal("cancelled admission not refused")
		}
	case <-time.After(time.Second):
		t.Fatal("preflight ignored cancellation")
	}
}
func TestD09ResidentProcessOutlivesActionContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, err := StartContext(ctx, Recipe{Name: "owned-neutral-control", Argv: napCmd("20")}, filepath.Join(t.TempDir(), "log"))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer p.Stop()
	cancel()
	if p.PID() == 0 {
		t.Fatal("settled child lifetime tied to completed action context")
	}
}
