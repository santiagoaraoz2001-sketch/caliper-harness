package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeRemainingDeadlineAndReset(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Keep native interruption granularity well below the distinction being
	// tested: ~2s total with a remaining budget, ~3.5s if the second statement
	// reuses the original 2s admission timeout. The former 300/200ms timeline
	// left only 150ms for native settlement and flaked under concurrent suites.
	const budget = 2 * time.Second
	const consumed = 1500 * time.Millisecond
	const latestRemaining = budget + consumed/2
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	start := time.Now()
	err = s.UpdateContext(ctx, func(tx *Tx) error {
		if err := tx.Write(`CREATE(p:Plan{id:'deadline',title:'uncommitted',goal:'g',state:'draft',created_at:$now})`, map[string]any{"now": time.Now().UTC()}); err != nil {
			return err
		}
		// Consume most of the transaction budget before a second actual native call.
		time.Sleep(consumed)
		_, err := tx.Query(`UNWIND range(1, 100000) AS a UNWIND range(1, 100000) AS b RETURN sum(a+b) AS n`, nil)
		return err
	})
	elapsed := time.Since(start)
	t.Logf("writer elapsed=%s error=%v", elapsed, err)
	if !errors.Is(err, context.DeadlineExceeded) || elapsed > latestRemaining {
		t.Fatalf("statement reused admission timeout: %s %v", elapsed, err)
	}
	rows, err := s.Query(`MATCH(p:Plan) WHERE p.id='deadline' RETURN p.id AS id`, nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("timed-out transaction not rolled back: %v %v", rows, err)
	}
	if err := s.Write(`CREATE(p:Plan{id:'after',title:'reset',goal:'g',state:'draft',created_at:$now})`, map[string]any{"now": time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Query(`MATCH(p:Plan) WHERE p.id='after' RETURN p.title AS title`, nil)
	if err != nil || len(rows) != 1 || rows[0]["title"] != "reset" {
		t.Fatalf("writer timeout/interrupt leaked: %v %v", rows, err)
	}
	// A cancelled private reader must settle before its connection closes. Its
	// watcher cannot interrupt this separately owned subsequent writer/read.
	readCtx, stop := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer stop()
	_, err = s.QueryContext(readCtx, `UNWIND range(1,100000) AS a UNWIND range(1,100000) AS b RETURN sum(a+b) AS n`, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("native read deadline: %v", err)
	}
	if _, err = s.Query(`RETURN 7 AS n`, nil); err != nil {
		t.Fatalf("reader cancellation leaked: %v", err)
	}
}
