//go:build darwin

package serve

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PrepareMLX preserves the native recipe's interpreter; unknown wrappers refuse.
// Completeness admits only local metadata, not runtime compatibility/readiness.
func PrepareMLX(r Recipe, bootstrap, path string) (Recipe, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err == nil {
		canonical, err = filepath.Abs(canonical)
	}
	if err != nil {
		return Recipe{}, err
	}
	if err := localComplete(canonical); err != nil {
		return Recipe{}, err
	}
	script, err := filepath.Abs(bootstrap)
	if err != nil {
		return Recipe{}, err
	}
	if st, err := os.Stat(script); err != nil || !st.Mode().IsRegular() {
		return Recipe{}, fmt.Errorf("native bootstrap missing: %s", script)
	}
	if len(r.Argv) == 0 {
		return Recipe{}, fmt.Errorf("MLX recipe has no native executable")
	}
	argv := append([]string(nil), r.Argv...)
	prefix := 1
	interpreter := argv[0]
	if len(argv) > 2 && argv[1] == "-m" && argv[2] == "mlx_lm.server" {
		prefix = 3
	} else {
		executable, err := exec.LookPath(argv[0])
		if err != nil {
			return Recipe{}, err
		}
		if filepath.Base(executable) != "mlx_lm.server" {
			return Recipe{}, fmt.Errorf("unsupported MLX wrapper; use native mlx_lm.server or python -m mlx_lm.server")
		}
		f, err := os.Open(executable)
		if err != nil {
			return Recipe{}, err
		}
		head, err := io.ReadAll(io.LimitReader(f, 1024))
		f.Close()
		if err != nil {
			return Recipe{}, err
		}
		line := strings.SplitN(string(head), "\n", 2)[0]
		interpreter = strings.TrimPrefix(line, "#!")
		if !strings.HasPrefix(line, "#!") || !filepath.IsAbs(interpreter) || strings.ContainsAny(interpreter, " \t\r") {
			return Recipe{}, fmt.Errorf("native executable needs an explicit absolute interpreter shebang")
		}
	}
	args := argv[prefix:]
	found := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--model" {
			if found || i+1 >= len(args) {
				return Recipe{}, fmt.Errorf("ambiguous/missing --model")
			}
			args[i+1] = canonical
			found = true
			i++
		}
	}
	if !found {
		return Recipe{}, fmt.Errorf("native recipe requires --model")
	}
	r.Argv = append([]string{interpreter, script}, args...)
	r.Env = append(append([]string(nil), r.Env...), "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1")
	r.Runtime = "mlx"
	r.localPath = canonical
	return r, nil
}

// OwnedMLXReady requires native facts from the exact process we started.
// API inventory/PID alone never establishes loaded weights or generation readiness.
func OwnedMLXReady(ctx context.Context, client *http.Client, p *Proc) (Health, error) {
	if p == nil || p.localPath == "" || p.PID() == 0 {
		return Health{}, fmt.Errorf("no live owned native MLX worker")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root(p.url)+"/health", nil)
	if err != nil {
		return Health{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Health{}, err
	}
	defer resp.Body.Close()
	var state struct {
		PID                           int    `json:"pid"`
		Path                          string `json:"path"`
		Loaded, Tokenizer, Generation bool
	}
	if resp.StatusCode != http.StatusOK {
		return Health{}, fmt.Errorf("native MLX unready: HTTP %d", resp.StatusCode)
	}
	if err := decodeWhole(resp.Body, &state); err != nil {
		return Health{}, err
	}
	if p.PID() != state.PID || state.Path != p.localPath || !state.Loaded || !state.Tokenizer || !state.Generation {
		return Health{}, fmt.Errorf("native MLX identity/load/generation not confirmed")
	}
	h := probeOpenAIContext(ctx, client, p.url)
	if !h.Up {
		return h, fmt.Errorf("native MLX API: %s", h.Err)
	}
	if p.PID() == 0 || p.PID() != state.PID {
		return Health{}, fmt.Errorf("owned MLX exited/changed during API readback")
	}
	h.Runtime = "mlx"
	h.Loaded = []string{p.localPath}
	return h, nil
}
