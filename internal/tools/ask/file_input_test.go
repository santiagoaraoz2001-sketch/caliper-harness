package ask

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestFileInputRedirectedStdin(t *testing.T) {
	if os.Getenv("CALIPER_STDIN_HELPER") == "1" {
		input, err := NewFileInput(os.Stdin)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		for _, want := range []string{"first\n", "second\n"} {
			if got, err := input.ReadLine(context.Background()); got != want || err != nil {
				t.Fatalf("piped stdin: %q, %v; want %q", got, err, want)
			}
		}
		if err := input.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stdin.Stat(); err != nil {
			t.Fatalf("caller stdin closed: %v", err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFileInputRedirectedStdin$")
	cmd.Env = append(os.Environ(), "CALIPER_STDIN_HELPER=1")
	cmd.Stdin = strings.NewReader("first\nsecond\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("redirected stdin subprocess: %v\n%s", err, out)
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
