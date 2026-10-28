package store

import (
	"github.com/kuzudb/go-kuzu"
	"path/filepath"
	"testing"
)

func TestProjectKnowledgeAdditiveLegacyReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old")
	db, e := kuzu.OpenDatabase(path, kuzu.DefaultSystemConfig())
	if e != nil {
		t.Fatal(e)
	}
	c, e := kuzu.OpenConnection(db)
	if e != nil {
		db.Close()
		t.Fatal(e)
	}
	// Real old tables, written and closed before the current Store migrations.
	for _, q := range []string{`CREATE NODE TABLE MemoryEntry(id STRING,kind STRING,body STRING,created_at TIMESTAMP,session_ref STRING,PRIMARY KEY(id))`, `CREATE NODE TABLE Plan(id STRING,title STRING,goal STRING,state STRING,created_at TIMESTAMP,PRIMARY KEY(id))`, `CREATE(m:MemoryEntry {id:'old-note',kind:'note',body:'historical α🙂',created_at:TIMESTAMP('2026-01-01 00:00:00'),session_ref:'old-session'})`, `CREATE(p:Plan {id:'old-plan',title:'old',goal:'same goal',state:'draft',created_at:TIMESTAMP('2026-01-01 00:00:00')})`} {
		result, e := c.Query(q)
		if e != nil {
			c.Close()
			db.Close()
			t.Fatal(e)
		}
		result.Close()
	}
	c.Close()
	db.Close()
	for i := 0; i < 2; i++ {
		s, e := Open(path)
		if e != nil {
			t.Fatal(e)
		}
		rows, e := s.Query(`MATCH(m:MemoryEntry) RETURN m.body AS body,m.session_ref AS ref,m.project_root AS root,m.key AS key,m.previous_id AS previous,m.task_id AS task,m.file_path AS file,m.file_sha256 AS sha`, nil)
		if e != nil || len(rows) != 1 || rows[0]["body"] != "historical α🙂" || rows[0]["ref"] != "old-session" || rows[0]["root"] != nil || rows[0]["key"] != nil || rows[0]["sha"] != nil {
			s.Close()
			t.Fatalf("legacy note %v %v", rows, e)
		}
		rows, e = s.Query(`MATCH(p:Plan) RETURN p.goal AS goal,p.project_root AS root`, nil)
		s.Close()
		if e != nil || len(rows) != 1 || rows[0]["goal"] != "same goal" || rows[0]["root"] != nil {
			t.Fatalf("legacy plan %v %v", rows, e)
		}
	}
}
