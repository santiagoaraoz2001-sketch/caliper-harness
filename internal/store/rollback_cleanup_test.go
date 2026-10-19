package store

import (
	"context"
	"errors"
	"fmt"
	kuzu "github.com/kuzudb/go-kuzu"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// No manual COMMIT misuse or concurrent writers: cancelled updates must leave
// the same row writable by the next ordinary operation on another lease.
func TestRollbackLeavesRowWritable(t *testing.T) {
	for _, query := range []bool{false, true} {
		t.Run(fmt.Sprint(query), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db")
			s := openLease(t, path)
			other := openLease(t, path)
			if err := s.Write(`CREATE(p:Plan{id:'rollback',title:'original',goal:'g',state:'draft',created_at:$now})`, map[string]any{"now": time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 10; i++ {
				ctx, cancel := context.WithCancel(context.Background())
				err := s.UpdateContext(ctx, func(tx *Tx) error {
					stmt := `MATCH(p:Plan) WHERE p.id='rollback' SET p.title='uncommitted'`
					var err error
					if query {
						_, err = tx.Query(stmt, nil)
					} else {
						err = tx.Write(stmt, nil)
					}
					if err != nil {
						return err
					}
					cancel()
					return ctx.Err()
				})
				cancel()
				if !errors.Is(err, context.Canceled) || !rollbackConfirmed(err) {
					t.Fatalf("cancelled update %d: %v", i, err)
				}
				rows, err := other.Query(`MATCH(p:Plan) WHERE p.id='rollback' RETURN p.title AS title`, nil)
				if err != nil || len(rows) != 1 || rows[0]["title"] != "original" {
					t.Fatalf("rollback %d: %v %v", i, rows, err)
				}
				if err := other.Update(func(tx *Tx) error { return tx.Write(`MATCH(p:Plan) WHERE p.id='rollback' SET p.title='original'`, nil) }); err != nil {
					t.Fatalf("next write %d: %v", i, err)
				}
			}
		})
	}
}

func TestRollbackCheckpointWaitsForReaderSettlement(t *testing.T) {
	s := openLease(t, filepath.Join(t.TempDir(), "db"))
	if err := writeNote(s, "original"); err != nil {
		t.Fatal(err)
	}
	reader, err := kuzu.OpenConnection(s.owner.db)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	s.owner.readers.RLock()
	held := true
	defer func() {
		if held {
			s.owner.readers.RUnlock()
		}
	}()
	for _, query := range []string{"BEGIN TRANSACTION READ ONLY", `MATCH(m:MemoryEntry) RETURN m.body`} {
		res, err := reader.Query(query)
		if res != nil {
			res.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	changed := make(chan struct{})
	settled := make(chan error, 1)
	refused := errors.New("refused after write")
	go func() {
		settled <- s.Update(func(tx *Tx) error {
			if err := tx.Write(`MATCH(m:MemoryEntry) SET m.body='uncommitted'`, nil); err != nil {
				return err
			}
			close(changed)
			return refused
		})
	}()
	<-changed
	res, err := reader.Query("COMMIT")
	if res != nil {
		res.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	held = false
	s.owner.readers.RUnlock()
	if err := <-settled; !errors.Is(err, refused) || !rollbackConfirmed(err) {
		t.Fatalf("rollback: %v", err)
	}
	if err := s.Update(func(tx *Tx) error { return tx.Write(`MATCH(m:MemoryEntry) SET m.body='next'`, nil) }); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointFailureKeepsWorkAndRefusesFurtherWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := writeNote(s, "original"); err != nil {
		t.Fatal(err)
	}
	// A directory where the native checkpoint must create a file forces an
	// actual I/O error on macOS/Linux/Windows, without mocked transaction facts.
	obstacle := path + ".shadow"
	if err := os.Mkdir(obstacle, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(obstacle)
	refused := errors.New("refused")
	err = s.Update(func(tx *Tx) error {
		if err := tx.Write(`MATCH(m:MemoryEntry) SET m.body='uncommitted'`, nil); err != nil {
			return err
		}
		return refused
	})
	var fact *TransactionError
	if !errors.Is(err, refused) || !errors.As(err, &fact) || fact.maintenanceErr == nil || !fact.RollbackConfirmed() {
		t.Fatalf("maintenance fact: %v", err)
	}
	if err := writeNote(s, "must-not-write"); err == nil || !strings.Contains(err.Error(), "maintenance failed") {
		t.Fatalf("unusable writer accepted: %v", err)
	}
	if err := os.Remove(obstacle); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened := openLease(t, path)
	rows, err := reopened.Query(`MATCH(m:MemoryEntry) RETURN m.body AS body`, nil)
	if err != nil || len(rows) != 1 || rows[0]["body"] != "original" {
		t.Fatalf("previous work lost: %v %v", rows, err)
	}
	if err := writeNote(reopened, "after-reopen"); err != nil {
		t.Fatal(err)
	}
}

func TestNativeAutoRollbackLeavesRowWritable(t *testing.T) {
	s := openLease(t, filepath.Join(t.TempDir(), "db"))
	if err := writeNote(s, "original"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		err := s.Update(func(tx *Tx) error {
			if err := tx.Write(`MATCH(m:MemoryEntry) SET m.body='uncommitted'`, nil); err != nil {
				return err
			}
			return tx.Write("THIS IS NOT CYPHER", nil)
		})
		if err == nil {
			t.Fatal("invalid statement accepted")
		}
		rows, err := s.Query(`MATCH(m:MemoryEntry) RETURN m.body AS body`, nil)
		if err != nil || len(rows) != 1 || rows[0]["body"] != "original" {
			t.Fatalf("rollback: %v %v", rows, err)
		}
		if err := s.Update(func(tx *Tx) error { return tx.Write(`MATCH(m:MemoryEntry) SET m.body='original'`, nil) }); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSingleStatementAutoRollbackLeavesRowWritable(t *testing.T) {
	s := openLease(t, filepath.Join(t.TempDir(), "db"))
	if err := s.Write(`CREATE(p:Plan{id:'1',title:'1',goal:'g',state:'draft',created_at:$now}), (q:Plan{id:'2',title:'bad',goal:'g',state:'draft',created_at:$now})`, map[string]any{"now": time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := s.Write(`MATCH(p:Plan) SET p.title=CAST(CAST(p.title AS INT64)+1 AS STRING)`, nil); err == nil {
			t.Fatal("invalid cast succeeded")
		}
		rows, err := s.Query(`MATCH(p:Plan) WHERE p.id='1' RETURN p.title AS title`, nil)
		if err != nil || len(rows) != 1 || rows[0]["title"] != "1" {
			t.Fatalf("implicit rollback: %v %v", rows, err)
		}
		if err := s.Write(`MATCH(p:Plan) WHERE p.id='1' SET p.title='1'`, nil); err != nil {
			t.Fatal(err)
		}
	}
}
