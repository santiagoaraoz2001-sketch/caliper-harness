package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestReaderDrainDeadlineNeverStartsExpiredMutation(t *testing.T) {
	s := openLease(t, filepath.Join(t.TempDir(), "db"))
	if err := writeNote(s, "original"); err != nil {
		t.Fatal(err)
	}
	for _, callback := range []bool{false, true} {
		s.owner.readers.RLock()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		settled := make(chan error, 1)
		called := false
		go func() {
			if callback {
				settled <- s.UpdateContext(ctx, func(tx *Tx) error {
					called = true
					return tx.Write(`MATCH(m:MemoryEntry) SET m.body='unexpected'`, nil)
				})
			} else {
				settled <- s.WriteContext(ctx, `MATCH(m:MemoryEntry) SET m.body='unexpected'`, nil)
			}
		}()
		<-ctx.Done()
		s.owner.readers.RUnlock()
		err := <-settled
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || called {
			t.Fatalf("expired write ran: callback=%v called=%v err=%v", callback, called, err)
		}
		rows, err := s.Query(`MATCH(m:MemoryEntry) RETURN m.body AS body`, nil)
		if err != nil || len(rows) != 1 || rows[0]["body"] != "original" {
			t.Fatal("expired mutation changed data", rows, err)
		}
	}
}
