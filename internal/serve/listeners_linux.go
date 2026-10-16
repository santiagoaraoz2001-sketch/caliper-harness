//go:build linux

package serve

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// loopbackListeners reads /proc/net/tcp{,6} for LISTEN sockets and
// resolves each socket inode to its owning process. Stdlib only.
func loopbackListeners(ctx context.Context) ([]Listener, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type sock struct {
		host  string
		port  int
		inode string
	}
	var socks []sock
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		raw, err := os.ReadFile(table)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" { // LISTEN
				continue
			}
			local := fields[1]
			colon := strings.LastIndex(local, ":")
			if colon < 0 {
				continue
			}
			addr, portHex := local[:colon], local[colon+1:]
			// 127.0.0.0/8 is 0100007F.. in v4 hex (low byte 7F last);
			// wildcard and ::1 also answer on loopback.
			loopback := addr == "00000000" || addr == "00000000000000000000000000000000" ||
				addr == "00000000000000000000000001000000" || strings.HasSuffix(addr, "7F") && len(addr) == 8
			if !loopback {
				continue
			}
			port, err := strconv.ParseInt(portHex, 16, 32)
			if err != nil || port == 0 {
				continue
			}
			host := "::1"
			if len(addr) == 8 {
				raw, _ := hex.DecodeString(addr)
				host = net.IP{raw[3], raw[2], raw[1], raw[0]}.String()
				if addr == "00000000" {
					host = "127.0.0.1"
				}
			}
			socks = append(socks, sock{host: host, port: int(port), inode: fields[9]})
		}
	}
	if len(socks) == 0 {
		return nil, nil
	}
	byInode := map[string]sock{}
	for _, s := range socks {
		byInode[s.inode] = s
	}
	procs, _ := os.ReadDir("/proc")
	var out []Listener
	for _, p := range procs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", p.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join("/proc", p.Name(), "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			socket, ok := byInode[inode]
			if !ok {
				continue
			}
			l := Listener{PID: pid, Port: socket.port, Host: socket.host}
			if exe, err := os.Readlink(filepath.Join("/proc", p.Name(), "exe")); err == nil {
				l.Image = exe
			}
			out = append(out, l)
		}
	}
	if len(out) == 0 && len(socks) > 0 {
		return nil, fmt.Errorf("found %d loopback listeners but could not resolve owners (permissions?)", len(socks))
	}
	return out, nil
}
