//go:build !windows

package serve

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

// A real listening child proves cleanup releases resources, not merely its launcher.
func TestResidentListenerHelper(t *testing.T) {
	path := os.Getenv("CALIPER_RESIDENT_TEST_ADDRESS")
	if path == "" {
		return
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer l.Close()
	if os.WriteFile(path+".pid", []byte(strconv.Itoa(os.Getpid())), 0600) != nil {
		os.Exit(4)
	}
	if os.WriteFile(path, []byte(l.Addr().String()), 0600) != nil {
		os.Exit(3)
	}
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		c.Close()
	}
}

func TestResidentTreeCleanup(t *testing.T) {
	for _, leaderExits := range []bool{false, true} {
		name := "stop"
		if leaderExits {
			name = "leader_exit"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			addressFile := filepath.Join(dir, "address")
			release := filepath.Join(dir, "release")
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			script := `"$1" -test.run=^TestResidentListenerHelper$ &
while [ ! -f "$2" ]; do sleep 0.02; done
`
			if !leaderExits {
				script += "wait\n"
			}
			p, err := Start(Recipe{Name: "tree-fixture", Argv: []string{"sh", "-c", script, "fixture", binary, release}, Env: []string{"CALIPER_RESIDENT_TEST_ADDRESS=" + addressFile}}, filepath.Join(dir, "server.log"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = p.Stop() })
			var address string
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				b, _ := os.ReadFile(addressFile)
				if len(b) > 0 {
					address = string(b)
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if b, err := os.ReadFile(addressFile + ".pid"); err == nil {
				childPID, err := strconv.Atoi(string(b))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })
			}
			if address == "" {
				b, _ := os.ReadFile(filepath.Join(dir, "server.log"))
				t.Fatalf("child did not start: %s", b)
			}
			conn, err := net.DialTimeout("tcp", address, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
			// An unrelated server must survive cleanup.
			external, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer external.Close()
			if leaderExits {
				if err := os.WriteFile(release, nil, 0600); err != nil {
					t.Fatal(err)
				}
				select {
				case <-p.done:
				case <-time.After(5 * time.Second):
					t.Fatal("leader did not settle")
				}
			} else {
				var wg sync.WaitGroup
				for i := 0; i < 8; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if err := p.Stop(); err != nil {
							t.Error(err)
						}
					}()
				}
				wg.Wait()
			}
			if err := p.Stop(); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(5 * time.Second)
			for {
				conn, err = net.DialTimeout("tcp", address, 50*time.Millisecond)
				if err != nil {
					break
				}
				conn.Close()
				if time.Now().After(deadline) {
					t.Fatal("owned descendant still listening after cleanup")
				}
				time.Sleep(10 * time.Millisecond)
			}
			conn, err = net.DialTimeout("tcp", external.Addr().String(), time.Second)
			if err != nil {
				t.Fatalf("unowned listener affected: %v", err)
			}
			conn.Close()
		})
	}
}
