//go:build windows

package serve

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

var (
	iphlpapi                  = syscall.NewLazyDLL("iphlpapi.dll")
	modkernel32Listeners      = syscall.NewLazyDLL("kernel32.dll")
	procGetExtendedTcpTable   = iphlpapi.NewProc("GetExtendedTcpTable")
	procQueryFullProcessImage = modkernel32Listeners.NewProc("QueryFullProcessImageNameW")
	procOpenProcess           = modkernel32Listeners.NewProc("OpenProcess")
	procCloseHandle           = modkernel32Listeners.NewProc("CloseHandle")
)

const (
	afInet                    = 2
	tcpTableOwnerPidListener  = 3
	tcpStateListen            = 2
	processQueryLimitedInfo   = 0x1000
)

// loopbackListeners enumerates IPv4 TCP listeners via iphlpapi and
// enriches each owner with its image path and captured command line.
func loopbackListeners() ([]Listener, error) {
	var size uint32
	r, _, _ := procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPidListener, 0)
	if size == 0 {
		return nil, fmt.Errorf("GetExtendedTcpTable sizing failed (code %d)", r)
	}
	buf := make([]byte, size)
	r, _, callErr := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPidListener, 0)
	if r != 0 {
		return nil, fmt.Errorf("GetExtendedTcpTable failed: %v (code %d)", callErr, r)
	}
	if len(buf) < 4 {
		return nil, nil
	}
	count := int(binary.LittleEndian.Uint32(buf[0:4]))
	const rowSize = 24 // MIB_TCPROW_OWNER_PID: 6 x uint32
	var out []Listener
	for i := 0; i < count; i++ {
		off := 4 + i*rowSize
		if off+rowSize > len(buf) {
			break
		}
		row := buf[off : off+rowSize]
		state := binary.LittleEndian.Uint32(row[0:4])
		if state != tcpStateListen {
			continue
		}
		addr := row[4:8]
		loopback := addr[0] == 127 && addr[1] == 0 && addr[2] == 0 && addr[3] == 1
		any := addr[0] == 0 && addr[1] == 0 && addr[2] == 0 && addr[3] == 0
		loopbackRange := addr[0] == 127 // 127.0.0.0/8 answers on loopback
		if !loopback && !any && !loopbackRange {
			continue
		}
		port := int(binary.BigEndian.Uint16(row[8:10]))
		pid := int(binary.LittleEndian.Uint32(row[20:24]))
		if port == 0 || pid <= 0 {
			continue
		}
		out = append(out, Listener{PID: pid, Port: port, Image: processImage(pid)})
	}
	enrichCmdlines(out)
	return out, nil
}

func processImage(pid int) string {
	h, _, _ := procOpenProcess.Call(processQueryLimitedInfo, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)
	buf := make([]uint16, 512)
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImage.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 || size == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:size])
}

// enrichCmdlines captures argv for relaunch in one CIM query. Best
// effort: a missing command line only loses relaunch, never discovery.
func enrichCmdlines(listeners []Listener) {
	if len(listeners) == 0 {
		return
	}
	seen := map[int]bool{}
	var filter []string
	for _, l := range listeners {
		if !seen[l.PID] {
			seen[l.PID] = true
			filter = append(filter, fmt.Sprintf("ProcessId=%d", l.PID))
		}
	}
	script := "Get-CimInstance Win32_Process -Filter '" + strings.Join(filter, " OR ") + "' | Select-Object ProcessId,CommandLine | ConvertTo-Json -Compress"
	raw, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
	if err != nil || len(raw) == 0 {
		return
	}
	type row struct {
		ProcessID   int    `json:"ProcessId"`
		CommandLine string `json:"CommandLine"`
	}
	var rows []row
	if err := json.Unmarshal(raw, &rows); err != nil {
		var one row
		if err := json.Unmarshal(raw, &one); err != nil {
			return
		}
		rows = []row{one}
	}
	byPID := map[int][]string{}
	for _, r := range rows {
		if argv := splitCmdline(r.CommandLine); len(argv) > 0 {
			byPID[r.ProcessID] = argv
		}
	}
	for i := range listeners {
		listeners[i].Cmdline = byPID[listeners[i].PID]
	}
}

// splitCmdline splits a Windows command line respecting double quotes.
func splitCmdline(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, c := range s {
		switch {
		case c == '"':
			inQuotes = !inQuotes
		case (c == ' ' || c == '\t') && !inQuotes:
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return out
}
