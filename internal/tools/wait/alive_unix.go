//go:build !windows

package wait

import (
	"errors"
	"os"
	"syscall"
)

func processAlive(pid int) (bool, error) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrProcessDone) {
		return false, nil
	}
	// Permission errors mean the process exists.
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if errno == syscall.ESRCH {
			return false, nil
		}
		if errno == syscall.EPERM {
			return true, nil
		}
	}
	return false, err
}
