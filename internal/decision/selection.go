package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"specific-harness/internal/config"
)

// Selection names downloaded assets and an explicit native typed-choice runtime.
type Selection struct {
	Model           string  `json:"model"`
	Runtime         string  `json:"runtime"`
	Device          string  `json:"device"`
	Path            *string `json:"path"`
	Revision        *string `json:"revision"`
	TrustRemoteCode bool    `json:"trust_remote_code"`
}

func SelectionFor(c config.DecisionConfig) Selection {
	s := Selection{Model: c.Model, Runtime: c.Runtime, Device: c.Device, TrustRemoteCode: c.TrustRemoteCode}
	if s.Runtime == "" {
		s.Runtime = "laya"
	}
	if c.Path != "" {
		v := c.Path
		s.Path = &v
	}
	if c.Revision != "" {
		v := c.Revision
		s.Revision = &v
	}
	return s
}
func sameString(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func (s Selection) sameAssets(a Selection) bool {
	return s.Model == a.Model && s.Runtime == a.Runtime && sameString(s.Path, a.Path) && sameString(s.Revision, a.Revision) && s.TrustRemoteCode == a.TrustRemoteCode
}

// Identity distinguishes the admitted selection from resolved assets and device.
type Identity struct {
	Model        string    `json:"model"`
	Runtime      string    `json:"runtime"`
	Source       string    `json:"source"`
	Path         string    `json:"path"`
	Revision     *string   `json:"revision"`
	Device       string    `json:"device"`
	NativeDevice string    `json:"native_device"`
	Architecture string    `json:"architecture"`
	Parameters   int64     `json:"parameters"`
	ContextLimit *int      `json:"context_limit"`
	Selection    Selection `json:"selection"`
}
type Health struct {
	Status    string     `json:"status"`
	PID       int        `json:"pid"`
	Loaded    *bool      `json:"loaded"`
	Ready     *bool      `json:"ready"`
	Loading   *bool      `json:"loading"`
	Model     *string    `json:"model"`
	Active    *Identity  `json:"active"`
	Requested *Selection `json:"requested"`
	Error     *string    `json:"error"`
	Backends  []string   `json:"backends"`
}

func (h Health) IsReady() bool { return h.Ready != nil && *h.Ready }
func (h Health) Matches(s Selection) bool {
	return h.IsReady() && h.Active != nil && s.sameAssets(h.Active.Selection) && (s.Device == DeviceAuto || s.Device == h.Active.Device)
}

// Decode consumes the complete body. 64KiB bounds metadata/verdicts, not model input.
func decode(r io.Reader, dst any) error {
	b, err := io.ReadAll(io.LimitReader(r, (64<<10)+1))
	if err != nil {
		return fmt.Errorf("read decision response: %w", err)
	}
	if len(b) > 64<<10 {
		return fmt.Errorf("decision response exceeds 64KiB")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err = d.Decode(dst); err != nil {
		return fmt.Errorf("malformed JSON: %w", err)
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("decision response contains trailing data")
	}
	return nil
}
func DecodeHealth(r io.Reader) (h Health, err error) {
	if err = decode(r, &h); err != nil {
		return
	}
	if h.PID <= 0 || h.Loaded == nil || h.Ready == nil || h.Loading == nil || *h.Loaded != h.IsReady() {
		return h, fmt.Errorf("invalid decision health/readiness shape")
	}
	if !h.IsReady() {
		if h.Status != "not_ready" || h.Active != nil || h.Model != nil || len(h.Backends) != 0 {
			return h, fmt.Errorf("invalid unloaded decision identity")
		}
		return
	}
	a := h.Active
	if h.Status != "ok" || a == nil || h.Model == nil || *h.Model != a.Model || strings.TrimSpace(a.Model) == "" || a.Source == "" || a.Path == "" || a.Architecture == "" || a.Parameters <= 0 || a.Selection.Model != a.Model || a.Selection.Runtime != a.Runtime {
		return h, fmt.Errorf("incomplete active decision identity")
	}
	if a.Selection.Device != DeviceAuto && a.Selection.Device != a.Device || a.ContextLimit != nil && *a.ContextLimit <= 0 || a.Selection.Path != nil && strings.TrimSpace(*a.Selection.Path) == "" || a.Selection.Revision != nil && strings.TrimSpace(*a.Selection.Revision) == "" {
		return h, fmt.Errorf("invalid admitted selection/context limit")
	}
	if a.Runtime != "laya" && a.Runtime != "system_one" {
		return h, fmt.Errorf("unknown native decision runtime")
	}
	// Torch MPS reports its single GPU as mps:0; retain that actual identity.
	gpu := a.NativeDevice == "mps" || a.NativeDevice == "mps:0" || a.NativeDevice == "cuda"
	if strings.HasPrefix(a.NativeDevice, "cuda:") {
		index, err := strconv.Atoi(strings.TrimPrefix(a.NativeDevice, "cuda:"))
		gpu = err == nil && index >= 0 && strconv.Itoa(index) == strings.TrimPrefix(a.NativeDevice, "cuda:")
	}
	if !(a.Device == DeviceCPU && a.NativeDevice == "cpu" || a.Device == DeviceGPU && gpu) || len(h.Backends) != 1 || h.Backends[0] != a.Device {
		return h, fmt.Errorf("invalid active decision device")
	}
	return
}
func (c *Client) WithSelection(s Selection) *Client {
	c.model, c.device = s.Model, s.Device
	c.expected = &s
	return c
}
func (c *Client) Health(ctx context.Context) (Health, error) {
	if c.expected != nil && !nativeRuntime(c.expected.Runtime) {
		return Health{}, fmt.Errorf("unsupported decision runtime; use laya or system_one")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	if err != nil {
		return Health{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Health{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Health{}, fmt.Errorf("decision health HTTP %d", resp.StatusCode)
	}
	return DecodeHealth(resp.Body)
}
func (c *Client) Activate(ctx context.Context, s Selection) (Health, error) {
	if !nativeRuntime(s.Runtime) || c.expected != nil && !nativeRuntime(c.expected.Runtime) {
		return Health{}, fmt.Errorf("unsupported decision runtime; use laya or system_one")
	}
	b, err := json.Marshal(s)
	if err != nil {
		return Health{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/activate", strings.NewReader(string(b)))
	if err != nil {
		return Health{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Health{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
		if readErr != nil {
			return Health{}, fmt.Errorf("activation response read: %w", readErr)
		}
		return Health{}, fmt.Errorf("decision activation HTTP %d: %s", resp.StatusCode, excerpt(body))
	}
	h, err := DecodeHealth(resp.Body)
	if err == nil && !h.Matches(s) {
		err = fmt.Errorf("activation has not established requested decision identity")
	}
	return h, err
}

func nativeRuntime(runtime string) bool {
	return runtime == "" || runtime == "laya" || runtime == "system_one"
}
