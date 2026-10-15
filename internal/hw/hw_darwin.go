//go:build darwin

package hw

import (
	"encoding/binary"
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

func probeOS(p *Profile) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("hardware autodetect does not support %s yet — darwin and linux only", runtime.GOOS)
	}
	// hw.memsize arrives as raw little-endian uint64 bytes, not text.
	mem, err := unix.SysctlRaw("hw.memsize")
	if err != nil {
		return fmt.Errorf("cannot read memory size: %v", err)
	}
	if len(mem) != 8 {
		return fmt.Errorf("cannot parse memory size: got %d bytes, want 8", len(mem))
	}
	p.MemTotalBytes = binary.LittleEndian.Uint64(mem)
	if p.MachineModel, err = unix.Sysctl("hw.model"); err != nil {
		return fmt.Errorf("cannot read machine model: %v", err)
	}
	if p.CPUModel, err = unix.Sysctl("machdep.cpu.brand_string"); err != nil {
		return fmt.Errorf("cannot read CPU model: %v", err)
	}
	p.UnifiedMemory = runtime.GOARCH == "arm64"
	return nil
}
