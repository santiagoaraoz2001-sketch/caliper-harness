package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestStoreCloseWhileWritingPersistsAcknowledged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s := openLease(t, path)
	const n = 24
	start := make(chan struct{})
	ack := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() {
				if p := recover(); p != nil {
					ack <- fmt.Errorf("close/write panic: %v", p)
				}
			}()
			<-start
			ack <- writeNote(s, fmt.Sprintf("close-%d", i))
		}(i)
	}
	close(start)
	// At least one actual write must settle before starting concurrent closes.
	if err := <-ack; err != nil {
		t.Fatal(err)
	}
	var closes sync.WaitGroup
	for i := 0; i < 3; i++ {
		closes.Add(1)
		go func() { defer closes.Done(); s.Close() }()
	}
	wg.Wait()
	closes.Wait()
	close(ack)
	acknowledged := 1
	for err := range ack {
		if err == nil {
			acknowledged++
		} else if !errors.Is(err, ErrClosed) {
			t.Error(err)
		}
	}
	t.Logf("concurrent close: %d acknowledged, %d refused", acknowledged, n-acknowledged)
	countNodes(t, openLease(t, path), "MemoryEntry", acknowledged)
}

func TestStoreCloseDrainsAdmittedQuery(t *testing.T) {
	s := openLease(t, filepath.Join(t.TempDir(), "db"))
	type answer struct {
		rows []map[string]any
		err  error
	}
	result := make(chan answer, 1)
	go func() {
		rows, err := s.Query(`UNWIND range(1, 50000) AS n RETURN n`, nil)
		result <- answer{rows, err}
	}()
	// Observe admission without stalling or substituting native execution.
	// Only the Query goroutine can hold this lease lock at this point.
	deadline := time.Now().Add(5 * time.Second)
	for s.mu.TryLock() {
		s.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("query was not observed admitted")
		}
		runtime.Gosched()
	}
	s.Close()
	a := <-result
	if a.err != nil || len(a.rows) != 50000 || a.rows[49999]["n"] != int64(50000) {
		t.Fatalf("admitted query must finish before native close: rows=%d err=%v", len(a.rows), a.err)
	}
	if _, err := s.Query(`RETURN 1 AS n`, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("later query must refuse, got %v", err)
	}
}
