package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// SetOllamaResident never pulls: only an exact same-server catalog identity
// may be submitted. HTTP success is insufficient without resident readback.
func SetOllamaResident(ctx context.Context, client *http.Client, base, model string, load bool) (Health, error) {
	installed, err := ollamaTags(ctx, client, base)
	if err != nil {
		return Health{}, err
	}
	found := false
	for _, name := range installed {
		found = found || name == model
	}
	if !found {
		return Health{}, fmt.Errorf("model %q is not installed at %s; no load attempted", model, base)
	}
	body := map[string]any{"model": model, "prompt": "", "stream": false}
	if !load {
		body["keep_alive"] = 0
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, root(base)+"/api/generate", bytes.NewReader(b))
	if err != nil {
		return Health{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Health{}, err
	}
	defer resp.Body.Close()
	var result struct {
		Model string
		Done  bool
		Error string
	}
	if resp.StatusCode != http.StatusOK {
		return Health{}, fmt.Errorf("ollama residency refused: HTTP %d", resp.StatusCode)
	}
	if err := decodeWhole(resp.Body, &result); err != nil || !result.Done || result.Model != model {
		return Health{}, fmt.Errorf("ollama residency response incomplete/mismatched: %v", err)
	}
	if result.Error != "" {
		return Health{}, fmt.Errorf("ollama residency refused: %s", result.Error)
	}
	if err := ctx.Err(); err != nil {
		return Health{}, err
	}
	h := probeOllamaContext(ctx, client, base)
	resident := false
	for _, name := range h.Loaded {
		resident = resident || name == model
	}
	if !h.Up || resident != load {
		return h, fmt.Errorf("resident readback did not confirm load=%t for %q at %s: %s", load, model, base, h.Err)
	}
	return h, nil
}
