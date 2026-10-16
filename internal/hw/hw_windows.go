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
	p.MachineModel, _ = machineModel()
	p.UnifiedMemory = false
	return nil
}

// machineModel joins board vendor and product, e.g. "LENOVO 21F9000BUS".
// Either half may be absent on VMs; then the model stays empty.
func machineModel() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\BIOS`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	vendor, _, _ := key.GetStringValue("SystemManufacturer")
	product, _, _ := key.GetStringValue("SystemProductName")
	switch {
	case vendor != "" && product != "":
		return vendor + " " + product, nil
	case product != "":
		return product, nil
	case vendor != "":
		return vendor, nil
	default:
		return "", fmt.Errorf("no system manufacturer or product name")
	}
}
