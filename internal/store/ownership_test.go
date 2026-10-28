package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"specific-harness/internal/testfs"
	"strings"
	"sync"
	"testing"
)

func openLease(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func writeNote(s *Store, id string) error {
	return s.Write(`CREATE (m:MemoryEntry {id:$id, kind:'note', body:$id, session_ref:'session'})`, map[string]any{"id": id})
}

func countNodes(t *testing.T, s *Store, table string, want int) {
	t.Helper()
	rows, err := s.Query(`MATCH (n:`+table+`) RETURN count(*) AS n`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["n"] != int64(want) {
		t.Fatalf("%s: want %d acknowledged records, got %v", table, want, rows)
	}
}

func TestStoreIndependentLeasesPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	a, b := openLease(t, path), openLease(t, path)
	for i, s := range []*Store{a, b} {
		if err := writeNote(s, fmt.Sprintf("lease-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	countNodes(t, a, "MemoryEntry", 2)
	countNodes(t, b, "MemoryEntry", 2)
	a.Close()
	a.Close()
	if err := writeNote(b, "remaining-lease"); err != nil {
		t.Fatalf("closing a lease invalidated another: %v", err)
	}
	countNodes(t, b, "MemoryEntry", 3)
	b.Close()
	countNodes(t, openLease(t, path), "MemoryEntry", 3)
}

func TestStorePathAliases(t *testing.T) {
	dir := t.TempDir()
	// Windows CI keeps its checkout and temporary directory on different drives.
	// Resolve the relative alias from the fixture directory, on the same volume.
	t.Chdir(dir)
	path := filepath.Join(dir, "db")
	a := openLease(t, path)
	link := filepath.Join(dir, "alias")
	if err := testfs.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	hard := filepath.Join(dir, "hard-db")
	if err := os.Link(path, hard); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatal(err)
	}
	leases := []*Store{a, openLease(t, rel), openLease(t, filepath.Join(link, "db")), openLease(t, hard)}
	for i, s := range leases {
		if err := writeNote(s, fmt.Sprintf("alias-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range leases {
		countNodes(t, s, "MemoryEntry", len(leases))
	}
	for _, s := range leases {
		s.Close()
	}
	countNodes(t, openLease(t, path), "MemoryEntry", len(leases))
}

func TestStoreConcurrentLeasesMixedPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			s, err := Open(path)
			if err != nil {
				errs <- err
				return
			}
			defer s.Close()
			id := fmt.Sprintf("mixed-%d", i)
			if err := writeNote(s, id); err != nil {
				errs <- err
				return
			}
			if err := s.Write(`CREATE (p:Plan {id:$id,title:$id,goal:'owned',state:'draft'})`, map[string]any{"id": id}); err != nil {
				errs <- err
				return
			}
			if _, err := s.Query(`MATCH (m:MemoryEntry) RETURN m.session_ref AS ref`, nil); err != nil {
				errs <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	s := openLease(t, path)
	countNodes(t, s, "MemoryEntry", n)
	countNodes(t, s, "Plan", n)
}

func TestStoreClosedLeaseRefuses(t *testing.T) {
	s := openLease(t, filepath.Join(t.TempDir(), "db"))
	s.Close()
	var panicValue any
	var err error
	func() {
		defer func() { panicValue = recover() }()
		err = writeNote(s, "closed")
	}()
	if panicValue != nil {
		t.Fatalf("post-close Write panicked: %v", panicValue)
	}
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("post-close Write must name closed lease, got %v", err)
	}
	if _, err := s.Query(`RETURN 1 AS n`, nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("post-close Query must name closed lease, got %v", err)
	}
}

func TestStoreInMemoryRemainsIndependent(t *testing.T) {
	a, b := openLease(t, ":memory:"), openLease(t, ":memory:")
	name := ":memory:"
	if runtime.GOOS == "windows" {
		// A colon is not a legal Windows filename. Keep the independent persistent
		// store coverage here; Unix also proves a sentinel-looking basename persists.
		name = "memory.db"
	}
	path := filepath.Join(t.TempDir(), name)
	disk := openLease(t, path)
	if err := writeNote(disk, "disk-before"); err != nil {
		t.Fatal(err)
	}
	if err := writeNote(a, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	countNodes(t, a, "MemoryEntry", 1)
	countNodes(t, b, "MemoryEntry", 0)
	a.Close()
	if err := writeNote(b, "other-ephemeral"); err != nil {
		t.Fatal(err)
	}
	countNodes(t, b, "MemoryEntry", 1)
	b.Close()
	if err := writeNote(disk, "disk-after-ephemeral-close"); err != nil {
		t.Fatal(err)
	}
	countNodes(t, disk, "MemoryEntry", 2)
	disk.Close()
	countNodes(t, openLease(t, path), "MemoryEntry", 2)
}

// A subprocess proves native file locking; in-process sharing cannot stand in
// for exclusive ownership across separate harness processes.
func TestStoreSecondProcessLock(t *testing.T) {
	if path := os.Getenv("SPECIFIC_STORE_LOCK_TEST_PATH"); path != "" {
		s, err := Open(path)
		if os.Getenv("SPECIFIC_STORE_LOCK_TEST_EXPECT") == "locked" {
			if err == nil {
				s.Close()
				t.Fatal("second process unexpectedly opened an owned database")
			}
			if !strings.Contains(err.Error(), "cannot open database") {
				t.Fatalf("expected competing native open refusal, got %v", err)
			}
			t.Logf("native lock refused: %v", err)
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := writeNote(s, "second-process"); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "db")
	s := openLease(t, path)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"locked", "open"} {
		if expected == "open" {
			s.Close()
		}
		cmd := exec.Command(bin, "-test.run=^TestStoreSecondProcessLock$", "-test.v")
		cmd.Env = append(os.Environ(), "SPECIFIC_STORE_LOCK_TEST_PATH="+path, "SPECIFIC_STORE_LOCK_TEST_EXPECT="+expected)
		out, err := cmd.CombinedOutput()
		t.Logf("%s child: %s", expected, out)
		if err != nil {
			t.Fatal(err)
		}
	}
	countNodes(t, openLease(t, path), "MemoryEntry", 1)
}
