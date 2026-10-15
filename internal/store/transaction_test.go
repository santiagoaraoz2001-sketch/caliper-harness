package store

import (
	"errors"
	"fmt"
	"github.com/kuzudb/go-kuzu"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateCommitRollbackAndLifetime(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Write(`CREATE(p:Plan{id:'tx',title:'before',goal:'g',state:'draft',created_at:$now})`, map[string]any{"now": time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		t.Helper()
		rows, e := s.Query(`MATCH(p:Plan) WHERE p.id='tx' RETURN p.title AS title`, nil)
		if e != nil || len(rows) != 1 {
			t.Fatalf("read %v %v", rows, e)
		}
		return rows[0]["title"].(string)
	}
	refused := errors.New("guard refused")
	var expired *Tx
	err = s.Update(func(tx *Tx) error {
		expired = tx
		if e := tx.Write(`MATCH(p:Plan) WHERE p.id='tx' SET p.title='partial'`, nil); e != nil {
			return e
		}
		rows, e := tx.Query(`MATCH(p:Plan) WHERE p.id='tx' RETURN p.title AS title`, nil)
		if e != nil || len(rows) != 1 || rows[0]["title"] != "partial" {
			return fmt.Errorf("same-connection read %v %v", rows, e)
		}
		return refused
	})
	if !errors.Is(err, refused) || read() != "before" {
		t.Fatalf("rollback %v %s", err, read())
	}
	if _, err = expired.Query(`RETURN 1`, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("escaped Tx active %v", err)
	}
	if err = expired.Write(`RETURN 1`, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("escaped write active %v", err)
	}
	err = s.Update(func(tx *Tx) error {
		if e := tx.Write(`MATCH(p:Plan) WHERE p.id='tx' SET p.title='panic'`, nil); e != nil {
			return e
		}
		panic("controlled")
	})
	if err == nil || !strings.Contains(err.Error(), "controlled") || read() != "before" {
		t.Fatalf("panic rollback %v %s", err, read())
	}
	if err = s.Update(func(tx *Tx) error { return tx.Write(`MATCH(p:Plan) WHERE p.id='tx' SET p.title='committed'`, nil) }); err != nil || read() != "committed" {
		t.Fatalf("writer recovery %v %s", err, read())
	}
	s.Close()
	called := false
	if err = s.Update(func(*Tx) error { called = true; return nil }); !errors.Is(err, ErrClosed) || called {
		t.Fatalf("closed lease admits callback %v", err)
	}
}

func TestReadinessColumnMigratesLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old-db")
	db, err := kuzu.OpenDatabase(path, kuzu.DefaultSystemConfig())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := kuzu.OpenConnection(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	for _, q := range []string{`CREATE NODE TABLE Approach(id STRING,title STRING,state STRING,created_at TIMESTAMP,PRIMARY KEY(id))`, `CREATE(a:Approach{id:'legacy',title:'retained café 😀',state:'complete',created_at:timestamp('2026-10-09 00:00:00')})`} {
		res, e := conn.Query(q)
		if e != nil {
			conn.Close()
			db.Close()
			t.Fatal(e)
		}
		res.Close()
	}
	conn.Close()
	db.Close()
	for i := 0; i < 2; i++ {
		s, e := Open(path)
		if e != nil {
			t.Fatal(e)
		}
		rows, e := s.Query(`MATCH(a:Approach) WHERE a.id='legacy' RETURN a.title AS title,a.state AS state,a.needs_ready AS dirty`, nil)
		s.Close()
		if e != nil || len(rows) != 1 || rows[0]["title"] != "retained café 😀" || rows[0]["state"] != "complete" || rows[0]["dirty"] != nil {
			t.Fatalf("legacy migration%d %v %v", i, rows, e)
		}
	}
}
