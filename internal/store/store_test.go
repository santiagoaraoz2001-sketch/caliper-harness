package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStoreWriteReadRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	err = s.Write(
		`CREATE (m:MemoryEntry {id: $id, kind: $kind, body: $body, created_at: $now, session_ref: $ref})`,
		map[string]any{"id": "e1", "kind": "note", "body": "hello", "now": now, "ref": "s1"},
	)
	if err != nil {
		t.Fatalf("write: %s", err)
	}

	rows, err := s.Query(
		`MATCH (m:MemoryEntry) WHERE m.id = $id RETURN m.id AS id, m.kind AS kind, m.body AS body, m.created_at AS created_at, m.session_ref AS ref`,
		map[string]any{"id": "e1"},
	)
	if err != nil {
		t.Fatalf("query: %s", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r["id"] != "e1" || r["kind"] != "note" || r["body"] != "hello" || r["ref"] != "s1" {
		t.Fatalf("wrong row: %v", r)
	}
	got, ok := r["created_at"].(time.Time)
	if !ok || !got.Equal(now) {
		t.Fatalf("wrong created_at: %v (%T), want %v", r["created_at"], r["created_at"], now)
	}
}

func TestStoreReopenPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	err = s.Write(
		`CREATE (m:MemoryEntry {id: $id, kind: $kind, body: $body, created_at: $now, session_ref: $ref})`,
		map[string]any{"id": "keep", "kind": "note", "body": "persist", "now": time.Now().UTC(), "ref": "s"},
	)
	if err != nil {
		t.Fatalf("write: %s", err)
	}
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %s", err)
	}
	defer s2.Close()
	rows, err := s2.Query(`MATCH (m:MemoryEntry) WHERE m.id = "keep" RETURN m.body AS body`, nil)
	if err != nil {
		t.Fatalf("query: %s", err)
	}
	if len(rows) != 1 || rows[0]["body"] != "persist" {
		t.Fatalf("lost row on reopen: %v", rows)
	}
}

func TestStorePATMigrationChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	// A pre-existing memory row must survive every migration replay.
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.Write(
		`CREATE (m:MemoryEntry {id: $id, kind: $kind, body: $body, created_at: $now, session_ref: $ref})`,
		map[string]any{"id": "keep", "kind": "note", "body": "persist", "now": now, "ref": "s"},
	); err != nil {
		t.Fatalf("seed memory: %s", err)
	}
	s.Close()

	// Second open replays the migration over the existing DB.
	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %s", err)
	}
	rows, err := s.Query(`MATCH (m:MemoryEntry) WHERE m.id = "keep" RETURN m.body AS body`, nil)
	if err != nil {
		t.Fatalf("memory check: %s", err)
	}
	if len(rows) != 1 || rows[0]["body"] != "persist" {
		t.Fatalf("memory lost on migrate: %v", rows)
	}

	// Plan→approach→task chain with rels.
	writes := []struct {
		q string
		p map[string]any
	}{
		{`CREATE (p:Plan {id: $id, title: $t, goal: $g, state: $s, created_at: $now})`,
			map[string]any{"id": "p1", "t": "plan", "g": "goal", "s": "draft", "now": now}},
		{`CREATE (a:Approach {id: $id, title: $t, state: $s, created_at: $now})`,
			map[string]any{"id": "a1", "t": "approach", "s": "draft", "now": now}},
		{`CREATE (x:Task {id: $id, title: $t, prompt: $p, state: $s, created_at: $now})`,
			map[string]any{"id": "t1", "t": "task", "p": "do it", "s": "queued", "now": now}},
		{`MATCH (p:Plan), (a:Approach) WHERE p.id = "p1" AND a.id = "a1" CREATE (p)-[:HAS_APPROACH]->(a)`, nil},
		{`MATCH (a:Approach), (x:Task) WHERE a.id = "a1" AND x.id = "t1" CREATE (a)-[:HAS_TASK]->(x)`, nil},
	}
	for _, w := range writes {
		if err := s.Write(w.q, w.p); err != nil {
			t.Fatalf("write %s: %s", w.q, err)
		}
	}
	rows, err = s.Query(`MATCH (p:Plan)-[:HAS_APPROACH]->(a:Approach)-[:HAS_TASK]->(x:Task) RETURN p.id AS p, a.id AS a, x.id AS x`, nil)
	if err != nil {
		t.Fatalf("traverse: %s", err)
	}
	if len(rows) != 1 || rows[0]["p"] != "p1" || rows[0]["a"] != "a1" || rows[0]["x"] != "t1" {
		t.Fatalf("wrong chain: %v", rows)
	}
	s.Close()

	// Third open: pure idempotence, counts unchanged.
	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen2: %s", err)
	}
	defer s.Close()
	rows, err = s.Query(`MATCH (p:Plan) RETURN count(*) AS n`, nil)
	if err != nil {
		t.Fatalf("count: %s", err)
	}
	if n, _ := rows[0]["n"].(int64); n != 1 {
		t.Fatalf("want 1 plan after replay, got %v", rows[0]["n"])
	}
}

func TestStoreConcurrentMixedLoad(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	defer s.Close()

	const writers, readers, perG = 4, 4, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*perG+readers*perG)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				err := s.Write(
					`CREATE (m:MemoryEntry {id: $id, kind: $kind, body: $body, created_at: $now, session_ref: $ref})`,
					map[string]any{
						"id":   fmt.Sprintf("w%d-%d", w, i),
						"kind": "note",
						"body": "load",
						"now":  time.Now().UTC(),
						"ref":  "load",
					},
				)
				if err != nil {
					errs <- fmt.Errorf("write: %w", err)
				}
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if _, err := s.Query(`MATCH (m:MemoryEntry) RETURN count(*) AS n`, nil); err != nil {
					errs <- fmt.Errorf("read: %w", err)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("mixed load error: %s", err)
	}

	rows, err := s.Query(`MATCH (m:MemoryEntry) RETURN count(*) AS n`, nil)
	if err != nil {
		t.Fatalf("count: %s", err)
	}
	if n, _ := rows[0]["n"].(int64); n != writers*perG {
		t.Fatalf("want %d rows, got %v", writers*perG, rows[0]["n"])
	}
}
