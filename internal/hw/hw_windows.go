//go:build windows

package hw

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

var (
	kernel32                                = syscall.NewLazyDLL("kernel32.dll")
	procInstalledSystemMemory               = kernel32.NewProc("GetPhysicallyInstalledSystemMemory")
)

// probeOS fills memory, CPU and machine identity from Win32 APIs.
// UnifiedMemory stays false: discrete and integrated Windows GPUs do not
// share one managed pool the way Apple Silicon does.
func probeOS(p *Profile) error {
	mem, err := totalPhysicalMemory()
	if err != nil {
		return fmt.Errorf("cannot read memory size: %v", err)
	}
	p.MemTotalBytes = mem
	p.CPUModel, _ = cpuModel()         // best-effort; empty reads as "unknown"
	p.MachineModel, _ = machineModel() // best-effort; empty reads as "unknown"
	p.UnifiedMemory = false
	return nil
}

// totalPhysicalMemory reports installed RAM via
// GetPhysicallyInstalledSystemMemory (kilobytes out).
func totalPhysicalMemory() (uint64, error) {
	var kb uint64
	r, _, err := procInstalledSystemMemory.Call(uintptr(unsafe.Pointer(&kb)))
	if r == 0 {
		return 0, err
	}
	if kb == 0 {
		return 0, fmt.Errorf("kernel reported zero")
	}
	return kb << 10, nil
}

// cpuModel reads the processor brand from the registry, e.g.
// "AMD Ryzen 7 7840U w/ Radeon 780M Graphics".
func cpuModel() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	name, _, err := key.GetStringValue("ProcessorNameString")
	if err != nil {
		return "", err
	}
	return name, nil
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
