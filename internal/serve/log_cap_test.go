package serve

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestServiceLogCapDrainsWithoutGrowingStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := &boundedServiceLog{file: f, remaining: 32}
	data := bytes.Repeat([]byte("output"), 100)
	for range 100 {
		if n, err := w.Write(data); err != nil || n != len(data) {
			t.Fatalf("did not drain %d %v", n, err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) != 32 || !bytes.Equal(raw, data[:32]) {
		t.Fatalf("unbounded/corrupted log %d %v", len(raw), err)
	}
}
