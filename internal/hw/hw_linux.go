//go:build linux

package hw

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func probeOS(p *Profile) error {
	memKB, err := memTotalKB()
	if err != nil {
		return err
	}
	p.MemTotalBytes = memKB << 10
	p.CPUModel, _ = cpuModel() // best-effort; empty reads as "unknown"
	return nil
}

func memTotalKB() (uint64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("cannot read memory size: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("cannot parse memory size %q: %v", fields[1], err)
		}
		return kb, nil
	}
	return 0, fmt.Errorf("MemTotal not found in /proc/meminfo")
}

func cpuModel() (string, error) {
	raw, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "model name"); ok {
			if _, name, found := strings.Cut(v, ":"); found {
				return strings.TrimSpace(name), nil
			}
		}
	}
	return "", fmt.Errorf("no model name in /proc/cpuinfo")
}
