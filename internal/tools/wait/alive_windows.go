//go:build windows

package wait

import "syscall"

const processQueryLimitedInformation = 0x1000
const stillActive = 259

func processAlive(pid int) (bool, error) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		if errno, ok := err.(syscall.Errno); ok && errno == 87 {
			return false, nil
		}
		return false, err
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false, err
	}
	return code == stillActive, nil
}
