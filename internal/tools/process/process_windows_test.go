//go:build windows

package process

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsProcessHelper(t *testing.T) {
	mode := os.Getenv("CALIPER_PROCESS_TEST")
	if mode == "" {
		return
	}
	if mode == "child" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestWindowsProcessHelper$")
	child.Env = append(os.Environ(), "CALIPER_PROCESS_TEST=child")
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(os.Getenv("CALIPER_PROCESS_PID"), []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
		os.Exit(3)
	}
	if mode == "cancel" {
		time.Sleep(time.Hour)
	}
	os.Exit(0)
}

func TestWindowsOwnedDescendants(t *testing.T) {
	for _, mode := range []string{"exit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := Command(ctx, os.Args[0], "-test.run=^TestWindowsProcessHelper$")
			cmd.Env = append(os.Environ(), "CALIPER_PROCESS_TEST="+mode, "CALIPER_PROCESS_PID="+pidFile)
			done := make(chan error, 1)
			go func() { done <- Run(cmd) }()
			var pid int
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if b, err := os.ReadFile(pidFile); err == nil {
					pid, _ = strconv.Atoi(string(b))
					if pid > 0 {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pid == 0 {
				cancel()
				<-done
				t.Fatal("child never started")
			}
			if mode == "cancel" {
				cancel()
			}
			err := <-done
			if mode == "exit" && err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" && err == nil {
				t.Fatal("cancellation reported success")
			}
			handle, openErr := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
			if openErr == windows.ERROR_INVALID_PARAMETER {
				return
			} // already reaped
			if openErr != nil {
				t.Fatal(openErr)
			}
			defer windows.CloseHandle(handle)
			result, err := windows.WaitForSingleObject(handle, 3000)
			if err != nil || result != windows.WAIT_OBJECT_0 {
				t.Fatalf("child still alive: wait=%d error=%v", result, err)
			}
		})
	}
}

func TestWindowsRunRejectsUnownedCommand(t *testing.T) {
	if err := Run(exec.Command("cmd", "/c", "exit", "0")); err == nil {
		t.Fatal("unsuspended process admitted")
	}
}
