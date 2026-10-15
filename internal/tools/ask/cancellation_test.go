package ask

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type borrowedInput struct{ reads, closes atomic.Int32 }

func (b *borrowedInput) Read([]byte) (int, error) { b.reads.Add(1); return 0, io.EOF }
func (b *borrowedInput) Close() error             { b.closes.Add(1); return nil }

func TestCancellationInputOwnershipAndNextAnswer(t *testing.T) {
	borrowed := &borrowedInput{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := Lines(borrowed)(ctx); err == nil || borrowed.reads.Load() != 0 || borrowed.closes.Load() != 0 {
		t.Fatalf("borrowed reader was consumed/closed or silently unsupported: %v", err)
	}
	owned := NewInput(borrowed)
	_ = owned.Close()
	_ = owned.Close()
	if borrowed.closes.Load() != 1 {
		t.Fatal("owned close is not idempotent")
	}
	rd, wr := io.Pipe()
	defer wr.Close()
	input := NewInput(rd)
	done := make(chan error, 1)
	go func() { _, err := input.ReadLine(ctx); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("owned reader did not settle")
	}
	// A fresh invocation's answer cannot be stolen by an abandoned old reader.
	next := NewInput(io.NopCloser(strings.NewReader("yes\nnext answer\n")))
	for _, want := range []string{"yes\n", "next answer\n"} {
		got, err := next.ReadLine(context.Background())
		if err != nil || got != want {
			t.Fatalf("got %q, %v; want %q", got, err, want)
		}
	}
}

func TestCancellationNativeFileDoesNotCloseOrStealNextInput(t *testing.T) {
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	defer wr.Close()
	input, err := NewFileInput(rd)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := input.ReadLine(ctx); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("native read did not settle")
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := wr.Write([]byte("NEXT\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(rd, buf); err != nil || string(buf) != "NEXT\n" {
		t.Fatalf("original input closed or stolen: %q, %v", buf, err)
	}
}

func TestCancellationPartialAnswerIsNotAnAnswer(t *testing.T) {
	rd, wr := io.Pipe()
	defer wr.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &Prompter{In: NewInput(rd), Out: io.Discard}
	done := make(chan error, 1)
	go func() { _, err := p.AskQuestions(ctx, []Question{{Text: "question"}}); done <- err }()
	if _, err := wr.Write([]byte("incomplete")); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("partial answer accepted: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("partial reader did not settle")
	}
}
