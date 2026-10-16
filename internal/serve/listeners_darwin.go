//go:build darwin

package serve

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// loopbackListeners uses lsof (ships with macOS): the stdlib has no
// listener-table API on darwin. Image and argv come from ps for the
// owning PID — best-effort, like every platform here.
func loopbackListeners() ([]Listener, error) {
	raw, err := exec.Command("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "pcn").Output()
	if err != nil {
		return nil, fmt.Errorf("lsof listener scan: %w", err)
	}
	var out []Listener
	var cur *Listener
	flush := func() {
		if cur != nil && cur.Port != 0 {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			flush()
			pid, err := strconv.Atoi(line[1:])
			if err == nil {
				cur = &Listener{PID: pid}
			}
		case 'n':
			if cur == nil {
				continue
			}
			name := line[1:]
			colon := strings.LastIndex(name, ":")
			if colon < 0 {
				continue
			}
			addr, portStr := name[:colon], name[colon+1:]
			if addr != "127.0.0.1" && addr != "[::1]" && addr != "*" && addr != "localhost" && !strings.HasPrefix(addr, "127.") {
				cur = nil
				continue
			}
			if port, err := strconv.Atoi(portStr); err == nil {
				cur.Port = port
			}
		}
	}
	flush()
	for i := range out {
		args, err := exec.Command("ps", "-p", strconv.Itoa(out[i].PID), "-o", "args=").Output()
		if err != nil {
			continue
		}
		argv := strings.Fields(strings.TrimSpace(string(args)))
		if len(argv) > 0 {
			out[i].Cmdline = argv
			out[i].Image = argv[0]
		}
	}
	return out, nil
}
