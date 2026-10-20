package ask

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileInputNullReturnsEOFWithoutClosingCaller(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	input, err := NewFileInput(file)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if line, err := input.ReadLine(ctx); line != "" || !errors.Is(err, io.EOF) {
		t.Fatalf("null input: %q, %v", line, err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Stat(); err != nil {
		t.Fatalf("caller file closed: %v", err)
	}
}

func TestFileInputRedirectedAnswersAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "answers")
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	input, err := NewFileInput(file)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := input.ReadLine(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
	readCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	for _, want := range []string{"first\n", "second\n"} {
		if got, err := input.ReadLine(readCtx); got != want || err != nil {
			t.Fatalf("redirected answer: %q, %v; want %q", got, err, want)
		}
	}
	if _, err := input.ReadLine(readCtx); !errors.Is(err, io.EOF) {
		t.Fatalf("redirected EOF: %v", err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("caller file closed: %v", err)
	}
}
