package store

import (
	"path/filepath"
	"testing"

	"github.com/kuzudb/go-kuzu"
)

func TestStoreOutcomeMigrationPreservesLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy")
	db, err := kuzu.OpenDatabase(path, kuzu.DefaultSystemConfig())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := kuzu.OpenConnection(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	// Exact pre-R02 Task schema after its historical nine additive columns.
	statements := []string{`CREATE NODE TABLE Task(id STRING,title STRING,prompt STRING,state STRING,created_at TIMESTAMP,current STRING,target STRING,sketch STRING,acceptance STRING,tests STRING,verify STRING,result STRING,attempts INT64,review STRING,PRIMARY KEY(id))`,
		`CREATE(x:Task{id:'legacy',title:'old',prompt:'original prompt',state:'done',created_at:TIMESTAMP('2026-01-01 00:00:00'),current:'current',target:'target',sketch:'sketch',acceptance:'exit code 0',tests:'TestOld',verify:'verify',result:'original result',attempts:3,review:'PASS historical prose'})`}
	for _, q := range statements {
		result, err := conn.Query(q)
		if err != nil {
			conn.Close()
			db.Close()
			t.Fatal(err)
		}
		result.Close()
	}
	conn.Close()
	db.Close()
	for i := 0; i < 2; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := s.Query(`MATCH(x:Task) RETURN x.state AS state,x.result AS result,x.review AS review,x.attempts AS attempts,x.execution AS execution,x.verification AS verification,x.execution_reason AS er,x.verification_reason AS vr,x.attempt_log AS log,x.acceptance_command AS acceptance_command`, nil)
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0]["state"] != "done" || rows[0]["result"] != "original result" || rows[0]["review"] != "PASS historical prose" || rows[0]["attempts"] != int64(3) {
			t.Fatalf("legacy facts changed: %+v", rows)
		}
		for _, key := range []string{"execution", "verification", "er", "vr", "log", "acceptance_command"} {
			if rows[0][key] != nil {
				t.Errorf("legacy %s invented: %+v", key, rows[0])
			}
		}
	}
}
