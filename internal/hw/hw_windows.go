//go:build windows

package hw

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func probeOS(p *Profile) error {
	// MEMORYSTATUSEX has two DWORDs followed by seven DWORDLONG values.
	memory := struct {
		Length, Load                                             uint32
		TotalPhys, AvailablePhys, TotalPage, AvailablePage       uint64
		TotalVirtual, AvailableVirtual, AvailableExtendedVirtual uint64
	}{}
	memory.Length = uint32(unsafe.Sizeof(memory))
	ok, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx").Call(uintptr(unsafe.Pointer(&memory)))
	if ok == 0 {
		return fmt.Errorf("cannot read memory size: %w", err)
	}
	p.MemTotalBytes = memory.TotalPhys
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err == nil {
		defer key.Close()
		p.CPUModel, _, _ = key.GetStringValue("ProcessorNameString")
	}
	return nil
}
