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
}

// TransactionError retains the owning transaction's settlement fact. A failed
// COMMIT remains uncertain even if the subsequent cleanup ROLLBACK succeeds.
type TransactionError struct {
	err        error
	rolledBack bool
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
func transact(conn *kuzu.Connection, fn func(*Tx) error) (err error) {
	return transactContext(conn, context.Background(), fn)
}
func transactContext(conn *kuzu.Connection, ctx context.Context, fn func(*Tx) error) (err error) {
	tx := &Tx{conn: conn, ctx: ctx}
	if err = tx.Write("BEGIN TRANSACTION", nil); err != nil {
		return err
	}
	committed := false
	commitAttempted := false
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("transaction callback panicked: %v", p)
		}
		if !committed {
			tx.ctx = context.Background()
			conn.SetTimeout(0)
			rollbackErr := tx.Write("ROLLBACK", nil)
			err = &TransactionError{err: errors.Join(err, rollbackErr), rolledBack: !commitAttempted && rollbackErr == nil}
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
