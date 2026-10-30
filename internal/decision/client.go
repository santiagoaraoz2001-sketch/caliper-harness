// Package decision validates typed choices from native decision services or
// explicitly selected chat models using the shared LLM transport.
package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"strings"
	"time"
)

// Devices the sidecar understands. Auto lets it pick its best backend.
const (
	DeviceAuto = "auto"
	DeviceCPU  = "cpu"
	DeviceGPU  = "gpu"
)

// Client talks to the decision sidecar's /judge endpoint.
type Client struct {
	disabled   bool
	Prompts    *prompts.Catalog
	baseURL    string
	model      string
	device     string
	inputLimit int
	params     *llm.Params
	expected   *Selection
	http       *http.Client
}

// New returns a client for a sidecar base URL (e.g. http://127.0.0.1:8090),
// the checkpoint id it must serve, the wanted device, and a timeout.
func New(server, model, device string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(server, "/"),
		model:   model,
		device:  device,
		http:    &http.Client{Timeout: timeout},
	}
}

// WithDisabled keeps the selected model but prevents all decision network calls.
func (c *Client) WithDisabled(disabled bool) *Client { c.disabled = disabled; return c }

var ErrDisabled = errors.New("decision model is disabled; enable it in Settings (decision.disabled: false) for this operation")

// WithInputLimit sets the complete native judgment input budget in tokens.
func (c *Client) WithInputLimit(tokens int) *Client {
	c.inputLimit = tokens
	return c
}

// Request is one judgment: pick the option that best fits the context.
type Request struct {
	Context      string
	Instructions string // blank = sidecar default ("Pick the best option.")
	Options      []string
}

// Verdict is the sidecar's answer. Model/Device echo what actually judged.
type Verdict struct {
	Pick             int
	Option           string
	Confidence       float64
	Model            string
	Device           string
	Protocol         string `json:",omitempty"`
	ConfidenceSource string `json:",omitempty"`
	Usage            string `json:",omitempty"` // Optional provider accounting; nil means unknown, never zero.
}

type judgeRequest struct {
	MaxInputTokens int        `json:"max_input_tokens,omitempty"`
	DeadlineUnixMS int64      `json:"deadline_unix_ms,omitempty"`
	Model          string     `json:"model"`
	Device         string     `json:"device"`
	Context        string     `json:"context"`
	Instructions   string     `json:"instructions,omitempty"`
	Options        []string   `json:"options"`
	Expected       *Selection `json:"expected,omitempty"`
}

type judgeResponse struct {
	Pick       *int            `json:"pick"`
	Option     string          `json:"option"`
	Confidence *float64        `json:"confidence"`
	Model      string          `json:"model"`
	Device     string          `json:"device"`
	Usage      json.RawMessage `json:"usage,omitempty"`
}

// Judge sends one request and validates the verdict: pick present and in
// range, option echo matching our text, confidence a real probability.
// Anything else is a loud error — a verdict that fails validation is
// never defaulted.
func (c *Client) Judge(ctx context.Context, r Request) (Verdict, error) {
	if c.disabled {
		return Verdict{}, ErrDisabled
	}
	if r.Instructions == "" {
		r.Instructions = c.Prompts.Text("decision.default.instructions")
	}
	// Identity admission and judgment share the configured request budget.
	if c.http.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.http.Timeout)
		defer cancel()
	}
	if strings.TrimSpace(r.Context) == "" {
		return Verdict{}, fmt.Errorf("judge needs a context to decide over")
	}
	if len(r.Options) < 2 {
		return Verdict{}, fmt.Errorf("judge needs at least 2 options, got %d", len(r.Options))
	}
	seen := map[string]bool{}
	for i, o := range r.Options {
		if strings.TrimSpace(o) == "" {
			return Verdict{}, fmt.Errorf("judge option %d is blank", i)
		}
		if seen[o] {
			return Verdict{}, fmt.Errorf("judge options must be distinct")
		}
		seen[o] = true
	}
	if c.expected != nil && !NativeRuntime(c.expected.Runtime) {
		if c.expected.Runtime == "ollama" && c.expected.Protocol == "system_one" {
			return c.judgeOllamaSystemOne(ctx, r)
		}
		return c.judgeChat(ctx, r)
	}
	var deadlineUnixMS int64
	if deadline, ok := ctx.Deadline(); ok {
		deadlineUnixMS = deadline.UnixMilli()
	}
	buf, err := json.Marshal(judgeRequest{
		DeadlineUnixMS: deadlineUnixMS, MaxInputTokens: c.inputLimit,
		Model: c.model, Device: c.device,
		Context: r.Context, Instructions: r.Instructions, Options: r.Options, Expected: c.expected,
	})
	if err != nil {
		return Verdict{}, fmt.Errorf("judge cannot encode request: %v", err)
	}
	if c.expected != nil {
		h, err := c.Health(ctx)
		if err != nil {
			return Verdict{}, fmt.Errorf("decision identity unavailable: %w", err)
		}
		if !h.Matches(*c.expected) {
			return Verdict{}, fmt.Errorf("requested decision selection pending activation")
		}
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/judge", bytes.NewReader(buf))
	if err != nil {
		return Verdict{}, fmt.Errorf("judge cannot build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return Verdict{}, fmt.Errorf("decision judgment at %s timed out: %w", c.baseURL, err)
		}
		if errors.Is(err, context.Canceled) {
			return Verdict{}, fmt.Errorf("decision judgment at %s canceled: %w", c.baseURL, err)
		}
		return Verdict{}, fmt.Errorf("decision sidecar at %s unreachable: %w — is it running?", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
		if readErr != nil {
			return Verdict{}, fmt.Errorf("decision sidecar refused HTTP %d: %w", resp.StatusCode, readErr)
		}
		if resp.StatusCode == http.StatusBadRequest && nativeCapacityRefusal(body) {
			return Verdict{}, &ContextCapacityError{Message: excerpt(body)}
		}
		return Verdict{}, fmt.Errorf("decision sidecar refused: HTTP %d: %s", resp.StatusCode, excerpt(body))
	}
	var jr judgeResponse
	if err := decode(resp.Body, &jr); err != nil {
		return Verdict{}, fmt.Errorf("decision sidecar answered %w", err)
	}
	if jr.Model != c.model {
		return Verdict{}, fmt.Errorf("decision sidecar model %q mismatches %q", jr.Model, c.model)
	}
	if jr.Device != DeviceCPU && jr.Device != DeviceGPU || c.device != DeviceAuto && jr.Device != c.device {
		return Verdict{}, fmt.Errorf("decision sidecar device %q mismatches %q", jr.Device, c.device)
	}
	if jr.Pick == nil {
		return Verdict{}, fmt.Errorf("decision sidecar answered without a pick")
	}
	if *jr.Pick < 0 || *jr.Pick >= len(r.Options) {
		return Verdict{}, fmt.Errorf("decision sidecar picked %d for %d options", *jr.Pick, len(r.Options))
	}
	if jr.Option != r.Options[*jr.Pick] {
		return Verdict{}, fmt.Errorf("decision sidecar echo %q mismatches option %d %q", jr.Option, *jr.Pick, r.Options[*jr.Pick])
	}
	if jr.Confidence == nil {
		return Verdict{}, fmt.Errorf("decision sidecar answered without a confidence")
	}
	if *jr.Confidence < 0 || *jr.Confidence > 1 {
		return Verdict{}, fmt.Errorf("decision sidecar confidence %v is not a probability", *jr.Confidence)
	}
	return Verdict{Pick: *jr.Pick, Option: jr.Option, Confidence: *jr.Confidence, Model: jr.Model, Device: jr.Device, Usage: optionalUsage(jr.Usage)}, nil
}

// ContextCapacityError is an explicit capacity refusal before inference, from
// actual encoded request size or native token admission. It grants no retry
// permission for identity, transport or other request errors.
type ContextCapacityError struct {
	Message        string
	EncodedRequest bool
}

// EncodedCapacity reports a local size refusal: the encoded request was
// too large and no inference ran. The decision server may be healthy.
// Callers must not describe this case as an unreachable server.
// IsCapacity reports any capacity refusal, local or provider-returned.
func IsCapacity(err error) bool {
	var capacity *ContextCapacityError
	return errors.As(err, &capacity)
}

func EncodedCapacity(err error) bool {
	var capacity *ContextCapacityError
	return errors.As(err, &capacity) && capacity.EncodedRequest
}

func (e *ContextCapacityError) Error() string {
	if e.EncodedRequest {
		return e.Message
	}
	return "decision sidecar refused: HTTP 400: " + e.Message
}

func nativeCapacityRefusal(body []byte) bool {
	var response struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &response) != nil {
		return false
	}
	if response.Error == "complete context does not fit the selected Laya model; shorten context/options" {
		return true
	}
	for _, format := range []string{
		"request has %d tokens; model context limit is %d; shorten context/options",
		"request has %d tokens; decision input limit is %d; shorten context/options",
		"request (%d tokens) exceeds the available context size (%d tokens), try increasing it",
	} {
		var count, maximum int
		n, err := fmt.Sscanf(response.Error, format, &count, &maximum)
		if err == nil && n == 2 && maximum > 0 && count > maximum && response.Error == fmt.Sprintf(format, count, maximum) {
			return true
		}
	}
	return false
}

func excerpt(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "empty body"
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func (c *Client) WithPrompts(pc *prompts.Catalog) *Client { c.Prompts = pc; return c }

// WithParams applies existing configured generation controls to ordinary chat judges only.
func (c *Client) WithParams(p llm.Params) *Client { c.params = &p; return c }

// Preserve reported usage without inventing a cross-provider unit conversion.
func optionalUsage(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return string(raw)
}
