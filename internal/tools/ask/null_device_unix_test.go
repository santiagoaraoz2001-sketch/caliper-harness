//go:build linux || darwin

package ask

import (
	"os"
	"syscall"
	"testing"
)

type deviceInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (i deviceInfo) Sys() any { return &i.stat }

func TestFileInputNullOnAnotherMount(t *testing.T) {
	null, err := os.Stat(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	alias := deviceInfo{null, *null.Sys().(*syscall.Stat_t)}
	alias.stat.Dev++
	alias.stat.Ino++
	if !sameNullDevice(alias, null) {
		t.Fatal("inherited null device on another mount must use the EOF reader")
	}
	alias.stat.Rdev++
	if sameNullDevice(alias, null) {
		t.Fatal("different character device must retain its cancellable reader")
	}
}
