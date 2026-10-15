package hw

import (
	"runtime"
	"testing"
)

func TestLimitsTiers(t *testing.T) {
	cases := []struct {
		gib       uint64
		ctx, conc int
		model     string
	}{
		{256, 131072, 8, "up to ~70B class"},
		{128, 131072, 8, "up to ~70B class"},
		{64, 131072, 4, "up to ~30B class"},
		{32, 65536, 4, "up to ~14B class"},
		{16, 65536, 2, "up to ~8B class"},
		{8, 32768, 1, "tiny models only (<=3B)"},
	}
	for _, tc := range cases {
		l := LimitsFor(Profile{MemTotalBytes: tc.gib << 30})
		if l.ContextWindow != tc.ctx || l.MaxConcurrency != tc.conc || l.ModelClassHint != tc.model {
			t.Errorf("%dGiB: got ctx=%d conc=%d hint=%q", tc.gib, l.ContextWindow, l.MaxConcurrency, l.ModelClassHint)
		}
		if l.DecodeConcurrency != l.MaxConcurrency || l.PromptConcurrency < 1 {
			t.Errorf("%dGiB: bad MLX knobs decode=%d prompt=%d", tc.gib, l.DecodeConcurrency, l.PromptConcurrency)
		}
	}
}

func TestMaxSubagents(t *testing.T) {
	cases := []struct {
		cpus int
		gib  uint64
		want int
	}{
		{32, 128, 16},
		{16, 64, 8},
		{12, 32, 4},
		{8, 16, 2},
		{4, 16, 2},
		{2, 4, 2}, // floor holds on tiny machines
	}
	for _, tc := range cases {
		got := MaxSubagents(Profile{CPUs: tc.cpus, MemTotalBytes: tc.gib << 30})
		if got != tc.want {
			t.Errorf("cpus=%d gib=%d: got %d, want %d", tc.cpus, tc.gib, got, tc.want)
		}
	}
}

func TestDetectSmoke(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("unsupported platform %s errors by design", runtime.GOOS)
	}
	p, err := Detect()
	if err != nil {
		t.Fatalf("detect failed: %v", err)
	}
	if p.CPUs <= 0 {
		t.Errorf("cpus=%d, want >0", p.CPUs)
	}
	if p.MemTotalBytes == 0 {
		t.Error("mem=0, want >0")
	}
	if p.OS == "" || p.Arch == "" {
		t.Errorf("os/arch unset: %+v", p)
	}
	if p.NPU != NPUNone && p.NPU != NPUApple {
		t.Errorf("npu=%q, want none or ane", p.NPU)
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" && p.NPU != NPUApple {
		t.Errorf("darwin/arm64 npu=%q, want ane", p.NPU)
	}
}

func TestNPUFor(t *testing.T) {
	cases := []struct{ os, arch, want string }{
		{"darwin", "arm64", NPUApple},
		{"darwin", "amd64", NPUNone},
		{"linux", "amd64", NPUNone},
		{"linux", "arm64", NPUNone},
	}
	for _, tc := range cases {
		if got := npuFor(tc.os, tc.arch); got != tc.want {
			t.Errorf("npuFor(%s,%s)=%q, want %q", tc.os, tc.arch, got, tc.want)
		}
	}
}
