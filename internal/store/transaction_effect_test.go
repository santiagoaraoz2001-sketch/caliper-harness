package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The interface lets the identical control compile against the pre-fix owner.
func rollbackConfirmed(err error) bool {
	var fact interface{ RollbackConfirmed() bool }
	return errors.As(err, &fact) && fact.RollbackConfirmed()
}

func TestTransactionEffectFacts(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Write(`CREATE(p:Plan{id:'effect',title:'before',goal:'g',state:'draft',created_at:$now})`, map[string]any{"now": time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		t.Helper()
		rows, err := s.Query(`MATCH(p:Plan) WHERE p.id='effect' RETURN p.title AS title`, nil)
		if err != nil || len(rows) != 1 {
			t.Fatalf("read: %v %v", rows, err)
		}
		return rows[0]["title"].(string)
	}
	write := func(tx *Tx, title string) error {
		return tx.Write(`MATCH(p:Plan) WHERE p.id='effect' SET p.title=$title`, map[string]any{"title": title})
	}
	refused := errors.New("owner validation refused")
	t.Run("validation", func(t *testing.T) {
		err := s.Update(func(*Tx) error { return refused })
		if !errors.Is(err, refused) || !rollbackConfirmed(err) || read() != "before" {
			t.Fatalf("fact=%v actual=%s error=%v", rollbackConfirmed(err), read(), err)
		}
	})
	t.Run("written_then_refused", func(t *testing.T) {
		err := s.Update(func(tx *Tx) error {
			if err := write(tx, "partial"); err != nil {
				return err
			}
			return refused
		})
		if !errors.Is(err, refused) || !rollbackConfirmed(err) || read() != "before" {
			t.Fatalf("fact=%v actual=%s error=%v", rollbackConfirmed(err), read(), err)
		}
	})
	t.Run("panic", func(t *testing.T) {
		err := s.Update(func(tx *Tx) error {
			if err := write(tx, "panic"); err != nil {
				return err
			}
			panic("controlled")
		})
		if err == nil || !strings.Contains(err.Error(), "controlled") || !rollbackConfirmed(err) || read() != "before" {
			t.Fatalf("fact=%v actual=%s error=%v", rollbackConfirmed(err), read(), err)
		}
	})
	t.Run("cancelled_callback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := s.UpdateContext(ctx, func(tx *Tx) error {
			if err := write(tx, "cancelled"); err != nil {
				return err
			}
			cancel()
			return ctx.Err()
		})
		if !errors.Is(err, context.Canceled) || !rollbackConfirmed(err) || read() != "before" {
			t.Fatalf("fact=%v actual=%s error=%v", rollbackConfirmed(err), read(), err)
		}
	})
	t.Run("normal_commit", func(t *testing.T) {
		err := s.Update(func(tx *Tx) error { return write(tx, "committed") })
		if err != nil || rollbackConfirmed(err) || read() != "committed" {
			t.Fatalf("actual=%s error=%v", read(), err)
		}
	})
	// Deliberately violate callback transaction ownership to exercise real Kuzu
	// settlement failures. These are component controls, not user operations.
	t.Run("rollback_failure", func(t *testing.T) {
		err := s.Update(func(tx *Tx) error {
			if err := write(tx, "early commit"); err != nil {
				return err
			}
			if err := tx.Write("COMMIT", nil); err != nil {
				return err
			}
			return refused
		})
		if !errors.Is(err, refused) || rollbackConfirmed(err) || read() != "early commit" {
			t.Fatalf("fact=%v actual=%s error=%v", rollbackConfirmed(err), read(), err)
		}
	})
	t.Run("commit_failure", func(t *testing.T) {
		err := s.Update(func(tx *Tx) error {
			if err := write(tx, "ambiguous commit"); err != nil {
				return err
			}
			return tx.Write("COMMIT", nil)
		})
		if err == nil || rollbackConfirmed(err) || read() != "ambiguous commit" {
			t.Fatalf("fact=%v actual=%s error=%v", rollbackConfirmed(err), read(), err)
		}
	})
	t.Run("commit_attempt_successful_cleanup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := s.UpdateContext(ctx, func(tx *Tx) error {
			if err := write(tx, "cleanup"); err != nil {
				return err
			}
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) || rollbackConfirmed(err) || read() != "ambiguous commit" {
			t.Fatalf("fact=%v actual=%s error=%v", rollbackConfirmed(err), read(), err)
		}
	})
	t.Run("failed_outer_settlement_masks_prior_rollback", func(t *testing.T) {
		prior := s.Update(func(*Tx) error { return refused })
		err := s.Update(func(tx *Tx) error {
			if err := write(tx, "outer committed"); err != nil {
				return err
			}
			if err := tx.Write("COMMIT", nil); err != nil {
				return err
			}
			return prior
		})
		if !errors.Is(err, refused) || rollbackConfirmed(err) || read() != "outer committed" {
			t.Fatalf("fact=%v actual=%s error=%v", rollbackConfirmed(err), read(), err)
		}
	})

}
