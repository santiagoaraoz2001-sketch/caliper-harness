package store

import (
	"path/filepath"
	"runtime/debug"
	"testing"
)

// Native Prepare/Query/Execute return owned handles even on error. Exercise all
// error paths and immediate reuse without relying on a Go finalizer to release
// native resources. This is not a deterministic reproduction of GRAPH-014 OOM.
func TestNativeFailedHandlesRecoverWithoutGarbageCollection(t *testing.T) {
	s, err := OpenWithBufferPool(filepath.Join(t.TempDir(), "db"), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	prior := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(prior)
	for i := 0; i < 40; i++ {
		for _, tc := range []struct {
			query  string
			params map[string]any
		}{
			{`MATCH(n:MissingAuditTable) WHERE n.id=$id RETURN n`, map[string]any{"id": "absent"}},
			{`RETURN CAST($value AS INT64) AS n`, map[string]any{"value": "not-an-integer"}},
			{`RETURN CAST('not-an-integer' AS INT64) AS n`, nil},
		} {
			if rows, err := s.Query(tc.query, tc.params); err == nil || len(rows) != 0 {
				t.Fatal("native failure fixture did not fail", rows, err)
			}
		}
		if err := s.Update(func(tx *Tx) error {
			return tx.Write(`CREATE(m:MemoryEntry{id:$id,kind:'note',body:'after native error'})`, map[string]any{"id": string(rune('a' + i))})
		}); err != nil {
			t.Fatal("next owned transaction failed", err)
		}
		rows, err := s.Query(`MATCH(m:MemoryEntry) RETURN count(m) AS n`, nil)
		if err != nil || len(rows) != 1 || rows[0]["n"] != int64(i+1) {
			t.Fatal("actual state lost or extra effects", rows, err)
		}
	}
}
