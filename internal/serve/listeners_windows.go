//go:build windows

package serve

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
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
	afInet                   = 2
	tcpTableOwnerPidListener = 3
	tcpStateListen           = 2
	processQueryLimitedInfo  = 0x1000
)

// loopbackListeners enumerates IPv4 TCP listeners via iphlpapi and
// reports each owner image without launching a shell.
func loopbackListeners(ctx context.Context) ([]Listener, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
		host := net.IP(addr).String()
		if any {
			host = "127.0.0.1"
		}
		out = append(out, Listener{Host: host, PID: pid, Port: port, Image: processImage(pid)})
	}
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
