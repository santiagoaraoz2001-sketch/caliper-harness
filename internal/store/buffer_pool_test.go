package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBufferPoolOwnerPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db")
	s, err := OpenWithBufferPool(path, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	alias := filepath.Join(t.TempDir(), "alias")
	if err = os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	shared, err := OpenWithBufferPool(filepath.Join(alias, "db"), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	if shared.owner != s.owner {
		t.Fatal("alias did not share writer")
	}
	for _, policy := range []uint64{0, 256 << 20} {
		if got, err := OpenWithBufferPool(filepath.Join(alias, "db"), policy); err == nil {
			got.Close()
			t.Fatalf("conflicting policy %d accepted", policy)
		} else if !strings.Contains(err.Error(), "close all owners") {
			t.Fatal(err)
		}
	}
	if _, err = shared.Query("RETURN 7 AS n", nil); err != nil {
		t.Fatal("conflict disturbed owner", err)
	}
	s.Close()
	if _, err = shared.Query("RETURN 8 AS n", nil); err != nil {
		t.Fatal("remaining lease closed", err)
	}
	shared.Close()
	next, err := OpenWithBufferPool(path, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
	legacy, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	auto, err := OpenWithBufferPool(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	auto.Close()
	invalid := filepath.Join(t.TempDir(), "not-created")
	if _, err = OpenWithBufferPool(invalid, 4095); err == nil {
		t.Fatal("invalid native floor accepted")
	}
	if _, err = os.Stat(invalid); !os.IsNotExist(err) {
		t.Fatalf("invalid policy created database: %v", err)
	}
	a, err := OpenWithBufferPool(":memory:", 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenWithBufferPool(":memory:", 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if a.owner == b.owner {
		t.Fatal("independent memory stores shared")
	}
}
