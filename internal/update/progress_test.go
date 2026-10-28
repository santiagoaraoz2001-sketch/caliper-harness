package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrepareReportsActualPhasesAndBytesWithoutActivation(t *testing.T) {
	data := fixture(t)
	m, release := server(t, data, false)
	var events []Progress
	m.Progress = func(p Progress) { events = append(events, p) }
	s, err := m.Prepare(t.Context(), release)
	if err != nil || s == nil {
		t.Fatal(err)
	}
	want := []string{"Preparing update", "Fetching release checksum", "Downloading update", "Verifying archive checksum", "Extracting update", "Verifying installed files", "Storing verified update", "Ready for activation"}
	var phases []string
	var previous time.Duration
	for _, e := range events {
		if len(phases) == 0 || phases[len(phases)-1] != e.Phase {
			phases = append(phases, e.Phase)
		}
		if e.Elapsed < previous {
			t.Fatal("elapsed time moved backwards")
		}
		previous = e.Elapsed
		if e.Phase == "Verifying archive checksum" && e.Bytes != int64(len(data)) {
			t.Fatalf("wrong actual downloaded bytes %d", e.Bytes)
		}
	}
	if len(phases) != len(want) {
		t.Fatalf("phases: %v", phases)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phases: %v", phases)
		}
	}
	if events[len(events)-1].Files == 0 || events[len(events)-1].Files != events[len(events)-1].TotalFiles {
		t.Fatal("inventory completion unreported")
	}
	if _, err := os.Stat(filepath.Join(m.Home, "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("prepare activated update")
	}
}

func TestPrepareCancellationPreservesSelectionAndCleansStage(t *testing.T) {
	for _, phase := range []string{"Downloading update", "Extracting update", "Verifying installed files"} {
		t.Run(phase, func(t *testing.T) {
			m, release := server(t, fixture(t), false)
			old := []byte("untouched selected version")
			path := filepath.Join(m.Home, "current.json")
			if err := os.WriteFile(path, old, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			m.Progress = func(p Progress) {
				if p.Phase == phase && (phase != "Verifying installed files" || p.Files > 0) {
					cancel()
				}
			}
			if _, err := m.Prepare(ctx, release); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel %s: %v", phase, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, old) {
				t.Fatal("cancellation changed selection")
			}
			stages, _ := filepath.Glob(filepath.Join(m.Home, "versions", ".install-*"))
			if len(stages) != 0 {
				t.Fatal("abandoned staging files", stages)
			}
		})
	}
}

func TestProgressThrottlesChunksButPreservesPhaseBoundaries(t *testing.T) {
	var events []Progress
	m := Manager{Progress: func(p Progress) { events = append(events, p) }}
	m.beginProgress()
	if err := m.phase(t.Context(), "Downloading update"); err != nil {
		t.Fatal(err)
	}
	w := progressWriter{t.Context(), io.Discard, m.progress}
	for i := 0; i < 1000; i++ {
		if _, err := w.Write([]byte("1234")); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.phase(t.Context(), "Verifying archive checksum"); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Bytes != 4000 {
		t.Fatalf("chatty/inexact progress: events=%d bytes=%d", len(events), events[len(events)-1].Bytes)
	}
}

type cancelDuringRead struct {
	io.Reader
	cancel context.CancelFunc
}

func (r cancelDuringRead) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	r.cancel()
	return n, err
}

func TestExtractionCancellationIsCheckedInsideStream(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	input := cancelDuringRead{bytes.NewReader(archiveBytes(t, map[string]string{"Caliper/large": string(bytes.Repeat([]byte("payload"), 100000))})), cancel}
	if err := extractContext(ctx, input, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("extraction ignored cancellation: %v", err)
	}
}
