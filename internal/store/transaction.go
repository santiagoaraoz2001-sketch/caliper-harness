package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/kuzudb/go-kuzu"
)

// Tx uses the writer connection only during Update. Callbacks must use Tx,
// never Store methods: a nested queued write cannot finish until they return.
type Tx struct {
	conn *kuzu.Connection
	ctx  context.Context
	used bool // callback made a native call; Query can also carry a mutation
}

// TransactionError retains the owning transaction's settlement fact. A failed
// COMMIT remains uncertain even if the subsequent cleanup ROLLBACK succeeds.
type TransactionError struct {
	err            error
	rolledBack     bool
	maintenanceErr error
}

func (e *TransactionError) Error() string           { return e.err.Error() }
func (e *TransactionError) Unwrap() error           { return e.err }
func (e *TransactionError) RollbackConfirmed() bool { return e.rolledBack }

func (t *Tx) Query(query string, params map[string]any) ([]map[string]any, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	if t.conn == nil {
		return nil, ErrClosed
	}
	t.used = true
	setTimeout(t.conn, t.ctx)
	rows, err := queryRows(t.conn, query, params)
	return rows, errors.Join(err, t.ctx.Err())
}
func (t *Tx) Write(query string, params map[string]any) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	if t.conn == nil {
		return ErrClosed
	}
	t.used = true
	setTimeout(t.conn, t.ctx)
	res, err := run(t.conn, query, params)
	if res != nil {
		res.Close()
	}
	if err != nil {
		return errors.Join(err, t.ctx.Err())
	}
	return nil
}

// Update serializes reads, validation and writes in one native transaction.
// The admitted lease remains alive until commit/rollback and result delivery.
func (s *Store) Update(fn func(*Tx) error) error {
	return s.UpdateContext(context.Background(), fn)
}
func (s *Store) UpdateContext(ctx context.Context, fn func(*Tx) error) error {
	if fn == nil {
		return fmt.Errorf("store update needs a transaction callback")
	}
	return s.submit(ctx, writeReq{update: fn})
}
func (d *database) transactContext(conn *kuzu.Connection, ctx context.Context, fn func(*Tx) error) (err error) {
	tx := &Tx{conn: conn, ctx: ctx}
	if err = tx.Write("BEGIN TRANSACTION", nil); err != nil {
		return err
	}
	tx.used = false // BEGIN is owned here, not a callback statement.
	committed := false
	commitAttempted := false
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("transaction callback panicked: %v", p)
		}
		if !committed {
			tx.ctx = context.Background()
			conn.SetTimeout(0)
			used := tx.used
			rollbackErr := tx.Write("ROLLBACK", nil)
			var maintenanceErr error
			if used {
				// Kuzu0.11.3 leaves a stale next-version pointer after rolling
				// back an updated row. CHECKPOINT retires that native chain;
				// replacing a connection does not. An errored statement may have
				// auto-rolled back already, so cleanup also covers that case.
				// Readers must settle first:
				// its checkpoint waits while holding their cleanup mutex.
				maintenanceErr = d.checkpointAfterRollback(conn)
			}
			err = &TransactionError{err: errors.Join(err, rollbackErr, maintenanceErr), rolledBack: !commitAttempted && rollbackErr == nil, maintenanceErr: maintenanceErr}
		}
		tx.conn = nil
	}()
	if err = fn(tx); err != nil {
		return err
	}
	commitAttempted = true
	err = tx.Write("COMMIT", nil)
	committed = err == nil
	return err
}

// Called only by the serialized writer after native statement settlement.
func (d *database) checkpointAfterRollback(conn *kuzu.Connection) error {
	d.readers.Lock()
	defer d.readers.Unlock()
	conn.SetTimeout(0)
	result, err := run(conn, "CHECKPOINT", nil)
	if result != nil {
		result.Close()
	}
	return err
}

func (d *database) writeStatement(conn *kuzu.Connection, query string, params map[string]any) error {
	result, err := run(conn, query, params)
	if result != nil {
		result.Close()
	}
	if err == nil {
		return nil
	}
	// Kuzu can abort a single statement after it has updated earlier rows.
	// Retire that chain too; its implicit rollback is not an owning receipt.
	maintenanceErr := d.checkpointAfterRollback(conn)
	return &TransactionError{err: errors.Join(err, maintenanceErr), maintenanceErr: maintenanceErr}
}
