//go:build darwin

package serve

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"specific-harness/internal/tools/process"
	"strconv"
	"strings"
)

func loopbackListeners(ctx context.Context) ([]Listener, error) {
	var output bytes.Buffer
	cmd := process.Command(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "pcn")
	cmd.Stdout = &output
	err := process.Run(cmd)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if !(errors.As(err, &exit) && exit.ExitCode() == 1 && output.Len() == 0) {
			return nil, fmt.Errorf("lsof listener scan: %w", err)
		}
	}
	return parseListeners(output.String()), nil
}

func parseListeners(raw string) []Listener {
	var out []Listener
	var cur Listener
	for _, line := range strings.Split(raw, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			cur = Listener{}
			cur.PID, _ = strconv.Atoi(line[1:])
		case 'c':
			cur.Image = line[1:]
		case 'n':
			colon := strings.LastIndex(line, ":")
			if colon < 0 || cur.PID == 0 {
				continue
			}
			host := strings.Trim(line[1:colon], "[]")
			if host == "*" || host == "0.0.0.0" || host == "localhost" {
				host = "127.0.0.1"
			}
			if host == "::" {
				host = "::1"
			}
			if host != "::1" && !strings.HasPrefix(host, "127.") {
				continue
			}
			port, err := strconv.Atoi(line[colon+1:])
			if err == nil && port > 0 {
				l := cur
				l.Host = host
				l.Port = port
				out = append(out, l)
			}
		}
	}
	return out
}
