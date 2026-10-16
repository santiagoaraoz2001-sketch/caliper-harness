//go:build darwin

package serve

import (
	"context"
	"errors"
	"testing"
)

func TestListenerScanPreservesAddressesAndMultiplePorts(t *testing.T) {
	got := parseListeners("p12\ncserver\nn127.0.0.2:8080\nn192.168.1.1:9000\nn[::1]:8080\nn*:8090\np13\ncother\nn127.0.0.1:8000\n")
	if len(got) != 4 {
		t.Fatalf("listeners=%+v", got)
	}
	if got[0].Host != "127.0.0.2" || got[1].Host != "::1" || got[2].Host != "127.0.0.1" || got[0].PID != 12 || got[2].PID != 12 || got[3].PID != 13 || got[0].Image != "server" {
		t.Fatalf("lost listener identity: %+v", got)
	}
}

func TestListenerScanHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := loopbackListeners(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
