package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionAppendReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, "abc", Event{Kind: "prompt", Text: "hi"}); err != nil {
		t.Fatalf("append: %s", err)
	}
	if err := Append(dir, "abc", Event{Kind: "reply", Text: "hello"}); err != nil {
		t.Fatalf("append: %s", err)
	}
	got, err := Read(dir, "abc")
	if err != nil {
		t.Fatalf("read: %s", err)
	}
	if len(got) != 2 || got[0].Kind != "prompt" || got[0].Text != "hi" ||
		got[1].Kind != "reply" || got[1].Text != "hello" {
		t.Fatalf("wrong events: %+v", got)
	}
	if got[0].Time.IsZero() || got[1].Time.IsZero() {
		t.Fatalf("zero time stamped nothing: %+v", got)
	}
	ids, err := List(dir)
	if err != nil {
		t.Fatalf("list: %s", err)
	}
	if len(ids) != 1 || ids[0] != "abc" {
		t.Fatalf("wrong ids: %v", ids)
	}
}

func TestSessionRejectsBadIDs(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"", "../x", "a/b", `a\b`, ".."} {
		if err := Append(dir, id, Event{Kind: "k", Text: "t"}); err == nil {
			t.Fatalf("id %q accepted, want rejection", id)
		}
		if _, err := Read(dir, id); err == nil {
			t.Fatalf("read id %q accepted, want rejection", id)
		}
	}
	// Nothing escaped the dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %s", err)
	}
	if len(entries) != 0 {
		t.Fatalf("bad ids wrote files: %v", entries)
	}
}

func TestSessionNewIDUsable(t *testing.T) {
	dir := t.TempDir()
	a, b := NewID(), NewID()
	if a == b {
		t.Fatalf("duplicate ids: %q", a)
	}
	if err := Append(dir, a, Event{Kind: "prompt", Text: "x"}); err != nil {
		t.Fatalf("generated id rejected: %s", err)
	}
}

func TestSessionMissingDirIsEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "none")
	ids, err := List(dir)
	if err != nil || len(ids) != 0 {
		t.Fatalf("missing dir must list empty, got %v %s", ids, err)
	}
	deleted, err := Sweep(dir, 3)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("missing dir must sweep nothing, got %v %s", deleted, err)
	}
}

func TestSessionRejectsEmptyKind(t *testing.T) {
	if err := Append(t.TempDir(), "a", Event{Text: "t"}); err == nil {
		t.Fatal("empty kind accepted, want rejection")
	}
}

func TestSessionSweep(t *testing.T) {
	dir := t.TempDir()
	mustAppend := func(id string) {
		t.Helper()
		if err := Append(dir, id, Event{Kind: "prompt", Text: "x"}); err != nil {
			t.Fatalf("append %s: %s", id, err)
		}
	}
	mustAppend("fresh")
	mustAppend("old")
	mustAppend("older")

	past := func(id string, months int) {
		t.Helper()
		when := time.Now().AddDate(0, -months, 0)
		path := filepath.Join(dir, id+".jsonl")
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatalf("chtimes %s: %s", id, err)
		}
	}
	past("old", 4)
	past("older", 13)

	deleted, err := Sweep(dir, 3)
	if err != nil {
		t.Fatalf("sweep: %s", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("want [old older] deleted, got %v", deleted)
	}
	ids, err := List(dir)
	if err != nil {
		t.Fatalf("list: %s", err)
	}
	if len(ids) != 1 || ids[0] != "fresh" {
		t.Fatalf("want [fresh] kept, got %v", ids)
	}
}

func TestSessionSweepCustomThreshold(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, "mid", Event{Kind: "prompt", Text: "x"}); err != nil {
		t.Fatalf("append: %s", err)
	}
	when := time.Now().AddDate(0, -7, 0)
	path := filepath.Join(dir, "mid.jsonl")
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes: %s", err)
	}

	// Custom 12-month threshold keeps a 7-month-old session.
	deleted, err := Sweep(dir, 12)
	if err != nil {
		t.Fatalf("sweep: %s", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("custom threshold deleted %v, want nothing", deleted)
	}
	// Default 3-month threshold deletes it.
	deleted, err = Sweep(dir, 3)
	if err != nil {
		t.Fatalf("sweep: %s", err)
	}
	if len(deleted) != 1 || deleted[0] != "mid" {
		t.Fatalf("want [mid] deleted, got %v", deleted)
	}
	// Zero disables sweeping entirely.
	if err := Append(dir, "keep", Event{Kind: "prompt", Text: "x"}); err != nil {
		t.Fatalf("append: %s", err)
	}
	if err := os.Chtimes(filepath.Join(dir, "keep.jsonl"), when, when); err != nil {
		t.Fatalf("chtimes: %s", err)
	}
	deleted, err = Sweep(dir, 0)
	if err != nil {
		t.Fatalf("sweep: %s", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("disabled sweep deleted %v, want nothing", deleted)
	}
}
