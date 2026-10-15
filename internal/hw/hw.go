// Package hw detects the machine's hardware profile and derives harness
// limits from it: context window, concurrency, and MLX server knobs.
// The tiers in LimitsFor are documented heuristics; everything they set
// stays overridable through config.
package hw

import (
	"fmt"
	"runtime"
)

// Profile describes the detected machine.
type Profile struct {
	OS            string
	Arch          string
	MachineModel  string // e.g. Mac15,14 (darwin); empty where unknown
	CPUModel      string
	CPUs          int
	MemTotalBytes uint64
	UnifiedMemory bool   // GPU shares the RAM pool (Apple Silicon)
	NPU           string // none or ane (amd lands with T4.4 hardware)
}

// Known NPU kinds. Amd arrives when 395 hardware does (T4.4).
const (
	NPUNone  = "none"
	NPUApple = "ane"
)

// Limits derived from a Profile.
type Limits struct {
	ContextWindow     int
	MaxConcurrency    int
	DecodeConcurrency int // MLX --decode-concurrency
	PromptConcurrency int // MLX --prompt-concurrency
	ModelClassHint    string
}

// Detect probes the local hardware. Unsupported platforms are a hard,
// plain-language error — never a silent conservative guess.
func Detect() (Profile, error) {
	p := Profile{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU()}
	if err := probeOS(&p); err != nil {
		return Profile{}, err
	}
	p.NPU = npuFor(p.OS, p.Arch)
	return p, nil
}

// npuFor maps platform to NPU kind. Assumption: every Apple Silicon chip
// ships a Neural Engine, so darwin/arm64 means ane; everything else is
// none until the AMD probe lands in T4.4.
func npuFor(os, arch string) string {
	if os == "darwin" && arch == "arm64" {
		return NPUApple
	}
	return NPUNone
}

// LimitsFor maps memory size to harness limits.
func LimitsFor(p Profile) Limits {
	gib := p.MemTotalBytes >> 30
	var l Limits
	switch {
	case gib >= 128:
		l = Limits{ContextWindow: 131072, MaxConcurrency: 8, ModelClassHint: "up to ~70B class"}
	case gib >= 64:
		l = Limits{ContextWindow: 131072, MaxConcurrency: 4, ModelClassHint: "up to ~30B class"}
	case gib >= 32:
		l = Limits{ContextWindow: 65536, MaxConcurrency: 4, ModelClassHint: "up to ~14B class"}
	case gib >= 16:
		l = Limits{ContextWindow: 65536, MaxConcurrency: 2, ModelClassHint: "up to ~8B class"}
	default:
		l = Limits{ContextWindow: 32768, MaxConcurrency: 1, ModelClassHint: "tiny models only (<=3B)"}
	}
	l.DecodeConcurrency = l.MaxConcurrency
	l.PromptConcurrency = max(1, l.MaxConcurrency/2)
	return l
}

// ResolveWindow returns the effective context window: an explicit
// setting wins, otherwise hardware autodetect, otherwise 0 (no cap)
// with the detect error for the caller to disclose.
func ResolveWindow(want int) (int, error) {
	if want > 0 {
		return want, nil
	}
	p, err := Detect()
	if err != nil {
		return 0, err
	}
	return LimitsFor(p).ContextWindow, nil
}

// MaxSubagents autodetects a conservative concurrent-subagent cap: subagents
// share one server model, so the cap guards client-side headroom, not model
// copies. Floor 2 keeps concurrent mode meaningful on small machines.
// Total (not free) memory keeps it to one probe; always overridable.
func MaxSubagents(p Profile) int {
	gib := int(p.MemTotalBytes >> 30)
	n := p.CPUs / 2
	if m := gib / 8; m < n {
		n = m
	}
	return max(2, n)
}

// String renders the profile and limits for humans.
func (p Profile) String() string {
	uni := "no"
	if p.UnifiedMemory {
		uni = "yes"
	}
	return fmt.Sprintf("os=%s arch=%s model=%s cpu=%s cpus=%d mem=%.1fGiB unified-mem=%s npu=%s",
		p.OS, p.Arch, orUnknown(p.MachineModel), orUnknown(p.CPUModel),
		p.CPUs, float64(p.MemTotalBytes)/(1<<30), uni, p.NPU)
}

func (l Limits) String() string {
	return fmt.Sprintf("context=%d concurrency=%d mlx-decode=%d mlx-prompt=%d (MLX advisory; not applied) models=%s",
		l.ContextWindow, l.MaxConcurrency, l.DecodeConcurrency, l.PromptConcurrency, l.ModelClassHint)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
