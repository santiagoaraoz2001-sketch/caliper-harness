// Package store is the embedded Kuzu backing. Kuzu allows a single write
// transaction at a time, so ALL writes run through one writer goroutine;
// reads open their own connections and run concurrently.
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kuzudb/go-kuzu"
	"specific-harness/internal/outcome"
)

// schemaStmts are the migrations, run in order on every open. Each is
// IF NOT EXISTS, so replays are clean and a crash between statements
// self-heals on the next open. Phase 2: memory. Phase 3: PAT skeleton
// (block-detail columns are T3.3's design to add, not this task's).
var schemaStmts = []string{
	`CREATE NODE TABLE IF NOT EXISTS MemoryEntry(
  id STRING,
  kind STRING,
  body STRING,
  created_at TIMESTAMP,
  session_ref STRING,
  PRIMARY KEY (id)
)`,
	`CREATE NODE TABLE IF NOT EXISTS Plan(
  id STRING,
  title STRING,
  goal STRING,
  state STRING,
  created_at TIMESTAMP,
  PRIMARY KEY (id)
)`,
	`CREATE NODE TABLE IF NOT EXISTS Approach(
  id STRING,
  title STRING,
  state STRING,
  created_at TIMESTAMP,
  PRIMARY KEY (id)
)`,
	`CREATE NODE TABLE IF NOT EXISTS Task(
  id STRING,
  title STRING,
  prompt STRING,
  state STRING,
  created_at TIMESTAMP,
  PRIMARY KEY (id)
)`,
	`CREATE REL TABLE IF NOT EXISTS HAS_APPROACH(FROM Plan TO Approach)`,
	`CREATE REL TABLE IF NOT EXISTS HAS_TASK(FROM Approach TO Task)`,
}

// schemaAlters add columns to existing tables. Kuzu ALTER has no IF NOT
// EXISTS, so each runs only when TABLE_INFO shows the column missing —
// replays stay clean without swallowing errors.
var schemaAlters = []struct{ table, column, ddl string }{
	{"MemoryEntry", "project_root", `ALTER TABLE MemoryEntry ADD project_root STRING`},
	{"MemoryEntry", "key", `ALTER TABLE MemoryEntry ADD key STRING`},
	{"MemoryEntry", "previous_id", `ALTER TABLE MemoryEntry ADD previous_id STRING`},
	{"MemoryEntry", "task_id", `ALTER TABLE MemoryEntry ADD task_id STRING`},
	{"MemoryEntry", "file_path", `ALTER TABLE MemoryEntry ADD file_path STRING`},
	{"Plan", "project_root", `ALTER TABLE Plan ADD project_root STRING`},
	{"Task", "check_command", `ALTER TABLE Task ADD check_command STRING`},
	{"Task", "acceptance_command", `ALTER TABLE Task ADD acceptance_command STRING`},
	// Edited paused briefs must pass readiness again; legacy NULL reads false.
	{"Approach", "needs_ready", `ALTER TABLE Approach ADD needs_ready BOOL`},
	// T3.3: task-block detail columns. Old rows read NULL → "".
	{"Task", "current", `ALTER TABLE Task ADD current STRING`},
	{"Task", "target", `ALTER TABLE Task ADD target STRING`},
	{"Task", "sketch", `ALTER TABLE Task ADD sketch STRING`},
	{"Task", "acceptance", `ALTER TABLE Task ADD acceptance STRING`},
	{"Task", "tests", `ALTER TABLE Task ADD tests STRING`},
	{"Task", "verify", `ALTER TABLE Task ADD verify STRING`},
	// T3.4: subagent result text (full; launch truncation only trims the parent feed).
	{"Task", "result", `ALTER TABLE Task ADD result STRING`},
	// T3.5: attempt counter for the retry loop.
	{"Task", "attempts", `ALTER TABLE Task ADD attempts INT64`},
	// R02/R03: additive outcome facts; legacy NULLs remain unknown/unverified.
	{"Task", "execution", `ALTER TABLE Task ADD execution STRING`},
	{"Task", "execution_reason", `ALTER TABLE Task ADD execution_reason STRING`},
	{"Task", "verification", `ALTER TABLE Task ADD verification STRING`},
	{"Task", "verification_reason", `ALTER TABLE Task ADD verification_reason STRING`},
	{"Task", "attempt_log", `ALTER TABLE Task ADD attempt_log STRING`},
	{"Task", "check_projection", `ALTER TABLE Task ADD check_projection STRING`},
	// T6.8: verification pass verdict on the task node.
	{"Task", "review", `ALTER TABLE Task ADD review STRING`},
}

// Store is an independently closable lease on a shared Kuzu database.
// Holding mu for an operation admits it before Close and keeps its native
// resources alive until the result has been copied out.
type Store struct {
	mu     sync.RWMutex
	closed bool
	owner  *database
}

// Identity is the canonical persistent owner, never an ephemeral resume identity.
func (s *Store) Identity() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.owner == nil || s.owner.path == ":memory:" {
		return ""
	}
	return s.owner.path
}

type database struct {
	path            string
	bufferPoolBytes uint64 // immutable construction policy, guarded by databases.Mutex
	db              *kuzu.Database
	writes          chan writeReq
	wg              sync.WaitGroup
	refs            int // guarded by databases.Mutex
}

// Registry locking serializes initial migration and final close as well as
// acquisition. No new owner can open a path while its previous owner closes.
var databases = struct {
	sync.Mutex
	byPath map[string]*database
}{byPath: make(map[string]*database)}

// ErrClosed rejects operations submitted after a lease has been released.
var ErrClosed = errors.New("store lease is closed")

type writeReq struct {
	ctx    context.Context
	update func(*Tx) error
	query  string
	params map[string]any
	done   chan error
}

// Open shares one database and writer within this process. Kuzu's native file
// lock remains authoritative across processes: competing opens fail, never
// fall back to an independent snapshot or another database.
func Open(path string) (*Store, error) {
	return OpenWithBufferPool(path, 0)
}

// OpenWithBufferPool selects an immutable native pool policy, not an RSS cap.
// All leases for the same database must request the same policy.
func OpenWithBufferPool(path string, bufferPoolBytes uint64) (*Store, error) {
	if bufferPoolBytes != 0 && bufferPoolBytes < 4096 {
		return nil, fmt.Errorf("buffer pool bytes must be 0 (automatic) or at least 4096")
	}
	// Kuzu's in-memory sentinel creates an independent ephemeral database;
	// it is not a filesystem path and must never become a persisted filename.
	memory := path == ":memory:"
	if !memory {
		var err error
		path, err = canonicalPath(path)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve database path: %w", err)
		}
	}
	databases.Lock()
	defer databases.Unlock()
	owner := databases.byPath[path]
	if owner == nil && !memory {
		// Existing case aliases and hardlinks can identify the same file even
		// after symlinks and relative components have been resolved.
		info, err := os.Stat(path)
		if err == nil {
			for _, other := range databases.byPath {
				otherInfo, err := os.Stat(other.path)
				if err == nil && os.SameFile(info, otherInfo) {
					owner = other
					break
				}
			}
		}
	}
	if owner != nil {
		if owner.bufferPoolBytes != bufferPoolBytes {
			return nil, fmt.Errorf("database %s already uses buffer_pool_bytes=%d, requested %d; close all owners before changing policy", owner.path, owner.bufferPoolBytes, bufferPoolBytes)
		}
		owner.refs++
		return &Store{owner: owner}, nil
	}
	native := kuzu.DefaultSystemConfig()
	native.BufferPoolSize = bufferPoolBytes
	db, err := kuzu.OpenDatabase(path, native)
	if err != nil {
		return nil, fmt.Errorf("cannot open database at %s: %s; check the path and close any other process using it (Kuzu requires exclusive process ownership)", path, err)
	}
	owner = &database{path: path, bufferPoolBytes: bufferPoolBytes, db: db, writes: make(chan writeReq), refs: 1}
	s := &Store{owner: owner}
	owner.wg.Add(1)
	go owner.writer()
	for _, stmt := range schemaStmts {
		if err := s.Write(stmt, nil); err != nil {
			owner.close()
			return nil, fmt.Errorf("cannot migrate schema: %s", err)
		}
	}
	if err := s.ensureColumns(); err != nil {
		owner.close()
		return nil, err
	}
	if err := s.backfillCheckProjection(); err != nil {
		owner.close()
		return nil, fmt.Errorf("cannot migrate check projection: %w", err)
	}
	if !memory {
		databases.byPath[path] = owner
	}
	return s, nil
}

// Resolve the parent before a new database exists, and the full path on
// reopen. Only create the parent; a pre-existing empty database directory is
// not a valid Kuzu database.
func canonicalPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	path = filepath.Join(parent, filepath.Base(path))
	resolved, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	return resolved, err
}

// ensureColumns runs each pending ALTER whose column is still missing.
func (s *Store) ensureColumns() error {
	for _, alt := range schemaAlters {
		have, err := s.tableColumns(alt.table)
		if err != nil {
			return fmt.Errorf("cannot inspect table %s: %s", alt.table, err)
		}
		if have[alt.column] {
			continue
		}
		if err := s.Write(alt.ddl, nil); err != nil {
			return fmt.Errorf("cannot migrate schema: %s", err)
		}
	}
	return nil
}

// tableColumns lists a table's property names. The table name
// interpolates, but names come only from schemaAlters above.
func (s *Store) tableColumns(table string) (map[string]bool, error) {
	rows, err := s.Query(`CALL TABLE_INFO('`+table+`') RETURN name`, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		if name, ok := r["name"].(string); ok {
			out[name] = true
		}
	}
	return out, nil
}

// Close drains this lease's admitted writes AND reads, then releases it.
// Other leases stay usable. Final release closes the native database before
// another Open can acquire it. Repeated/concurrent Close calls are safe.
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	databases.Lock()
	defer databases.Unlock()
	s.owner.refs--
	if s.owner.refs == 0 {
		s.owner.close()
		// Ephemeral owners were never registered; release only this owner.
		if databases.byPath[s.owner.path] == s.owner {
			delete(databases.byPath, s.owner.path)
		}
	}
}

func (d *database) close() {
	close(d.writes)
	d.wg.Wait()
	d.db.Close()
}

// Write runs one write statement through the single writer queue.
func (s *Store) Write(query string, params map[string]any) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrClosed
	}
	req := writeReq{query: query, params: params, done: make(chan error, 1)}
	s.owner.writes <- req
	return <-req.done
}

// Query runs a read on its own connection and returns rows as column-name
// maps. Concurrent Queries are safe.
func (s *Store) Query(query string, params map[string]any) ([]map[string]any, error) {
	return s.QueryContext(context.Background(), query, params)
}

// QueryContext owns its private connection through cancellation and native settlement.
func (s *Store) QueryContext(ctx context.Context, query string, params map[string]any) ([]map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	conn, err := kuzu.OpenConnection(s.owner.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	setTimeout(conn, ctx)
	stop, settled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(settled)
		select {
		case <-ctx.Done():
			conn.Interrupt()
		case <-stop:
		}
	}()
	rows, err := queryRows(conn, query, params)
	close(stop)
	<-settled
	return rows, errors.Join(err, ctx.Err())
}

func queryRows(conn *kuzu.Connection, query string, params map[string]any) ([]map[string]any, error) {
	res, err := run(conn, query, params)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	var rows []map[string]any
	for res.HasNext() {
		t, err := res.Next()
		if err != nil {
			return nil, err
		}
		m, err := t.GetAsMap()
		t.Close()
		if err != nil {
			return nil, err
		}
		rows = append(rows, m)
	}
	return rows, nil
}

// writer owns one connection and executes queued writes serially.
func (s *database) writer() {
	defer s.wg.Done()
	conn, err := kuzu.OpenConnection(s.db)
	if err != nil {
		for req := range s.writes {
			req.done <- err
		}
		return
	}
	defer conn.Close()
	for req := range s.writes {
		ctx := req.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if err := ctx.Err(); err != nil {
			req.done <- err
			continue
		}
		setTimeout(conn, ctx)
		if req.update != nil {
			err := transactContext(conn, ctx, req.update)
			conn.SetTimeout(0)
			req.done <- err
			continue
		}
		res, err := run(conn, req.query, req.params)
		if err == nil {
			res.Close()
		}
		conn.SetTimeout(0)
		req.done <- err
	}
}

// run executes one statement, preparing first when params are given.
// An empty map behaves as no params, so callers never branch on it.
func run(conn *kuzu.Connection, query string, params map[string]any) (*kuzu.QueryResult, error) {
	if len(params) == 0 {
		return conn.Query(query)
	}
	stmt, err := conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	return conn.Execute(stmt, params)
}

// Enqueue is cancellable only before writer admission; admitted work always settles.
func (s *Store) WriteContext(ctx context.Context, query string, params map[string]any) error {
	return s.submit(ctx, writeReq{query: query, params: params})
}
func (s *Store) submit(ctx context.Context, req writeReq) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	req.ctx, req.done = ctx, make(chan error, 1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.owner.writes <- req:
	}
	return <-req.done
}
func setTimeout(conn *kuzu.Connection, ctx context.Context) {
	conn.SetTimeout(0)
	if until, ok := ctx.Deadline(); ok {
		remaining := time.Until(until)
		ms := remaining.Milliseconds()
		if remaining%time.Millisecond > 0 {
			ms++
		}
		if ms < 1 {
			ms = 1
		}
		conn.SetTimeout(uint64(ms))
	}
}

// Backfill one legacy archive at a time before exposing the store. Each NULL
// record commits independently; reopening resumes without changing raw receipts.
func (s *Store) backfillCheckProjection() error {
	for {
		rows, err := s.Query(`MATCH (t:Task) WHERE t.check_projection IS NULL RETURN t.id AS id,t.attempt_log AS log LIMIT 1`, nil)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		log, _ := rows[0]["log"].(string)
		if err := s.Write(`MATCH (t:Task) WHERE t.id=$id AND t.check_projection IS NULL SET t.check_projection=$projection`, map[string]any{"id": rows[0]["id"], "projection": outcome.CheckProjection(log)}); err != nil {
			return err
		}
	}
}
