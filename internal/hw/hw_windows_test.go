//go:build windows

package hw

import "testing"

func TestWindowsPhysicalMemory(t *testing.T) {
	var p Profile
	if err := probeOS(&p); err != nil {
		t.Fatal(err)
	}
	if p.MemTotalBytes == 0 {
		t.Fatal("GlobalMemoryStatusEx returned zero physical memory")
	}
	if p.UnifiedMemory {
		t.Fatal("Windows memory was labelled Apple unified memory")
	}
}
