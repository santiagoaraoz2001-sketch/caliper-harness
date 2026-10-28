package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"specific-harness/internal/llm"
)

func chatRuntime(runtime string) bool {
	return runtime == llm.RuntimeOpenAI || runtime == llm.RuntimeMLX || runtime == llm.RuntimeOllama || runtime == llm.RuntimeRulith
}

// judgmentTransport admits the actual encoded body, not a second approximation
// of the LLM wire request. It adds no budget and never changes the shared client.
type judgmentTransport struct {
	base     http.RoundTripper
	maxBytes int
}

func (t judgmentTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.maxBytes < 0 || t.maxBytes > 0 && r.ContentLength < 0 {
		return nil, fmt.Errorf("judge complete request bytes %d exceed limit %d", r.ContentLength, t.maxBytes)
	}
	if t.maxBytes > 0 && r.ContentLength > int64(t.maxBytes) {
		return nil, &ContextCapacityError{Message: fmt.Sprintf("judge complete request bytes %d exceed limit %d", r.ContentLength, t.maxBytes), EncodedRequest: true}
	}
	return t.base.RoundTrip(r)
}

func (c *Client) judgeChat(ctx context.Context, r Request) (verdict Verdict, err error) {
	runtime := c.expected.Runtime
	if !chatRuntime(runtime) {
		return Verdict{}, fmt.Errorf("unsupported decision runtime %q", runtime)
	}
	payload, err := json.Marshal(struct {
		Context      string   `json:"context"`
		Instructions string   `json:"instructions"`
		Options      []string `json:"options"`
	}{r.Context, r.Instructions, r.Options})
	if err != nil {
		return Verdict{}, err
	}
	base := c.baseURL
	httpClient := *c.http
	transport := httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	httpClient.Transport = judgmentTransport{transport, r.MaxBytes}
	client := llm.New(base, c.model).WithRuntime(runtime).WithHTTPClient(&httpClient)
	if c.params != nil {
		client.WithParams(*c.params)
	}
	reply, err := client.ChatTools(ctx, []llm.Message{{Role: "system", Content: c.Prompts.Text("decision.chat.instructions")}, {Role: "user", Content: string(payload)}}, nil, nil)
	if err != nil {
		return Verdict{}, fmt.Errorf("chat judgment: %w", err)
	}
	if reply.OutputLimit || reply.ThinkingOnly || reply.FinishReason != "stop" || len(reply.Message.ToolCalls) > 0 {
		return Verdict{}, fmt.Errorf("chat judgment needs a complete answer without tool calls")
	}
	if reply.Served != c.model {
		return Verdict{}, fmt.Errorf("chat judgment model %q mismatches %q", reply.Served, c.model)
	}
	// Retain actionable output evidence for every malformed verdict, using the
	// existing diagnostic excerpt policy rather than inventing replacement values.
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w; received: %s", err, excerpt([]byte(reply.Message.Content)))
		}
	}()
	// Decode only verdict fields; do not impose a native verdict cap on reasoning.
	d := json.NewDecoder(strings.NewReader(reply.Message.Content))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return Verdict{}, fmt.Errorf("chat judgment must be a JSON object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return Verdict{}, err
		}
		name, ok := key.(string)
		if !ok {
			return Verdict{}, fmt.Errorf("invalid judgment key")
		}
		if _, exists := fields[name]; exists {
			return Verdict{}, fmt.Errorf("duplicate judgment field %q", name)
		}
		if name != "pick" && name != "option" && name != "confidence" {
			return Verdict{}, fmt.Errorf("unknown judgment field %q", name)
		}
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return Verdict{}, err
		}
		fields[name] = raw
	}
	if _, err = d.Token(); err != nil {
		return Verdict{}, err
	}
	if _, err = d.Token(); err != io.EOF {
		return Verdict{}, fmt.Errorf("trailing judgment data")
	}
	if len(fields) != 3 {
		return Verdict{}, fmt.Errorf("judgment requires pick, option and confidence")
	}
	var pick int
	var option string
	var confidence float64
	for name, dst := range map[string]any{"pick": &pick, "option": &option, "confidence": &confidence} {
		if bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return Verdict{}, fmt.Errorf("null judgment field %s", name)
		}
		if err = json.Unmarshal(fields[name], dst); err != nil {
			return Verdict{}, err
		}
	}
	if pick < 0 || pick >= len(r.Options) || option != r.Options[pick] {
		return Verdict{}, fmt.Errorf("judgment pick and option do not match supplied options")
	}
	if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 1 {
		return Verdict{}, fmt.Errorf("judgment confidence must be a finite probability")
	}
	usage := ""
	if reply.Usage.Reported {
		raw, _ := json.Marshal(reply.Usage)
		usage = string(raw)
	}
	return Verdict{Usage: usage, Pick: pick, Option: option, Confidence: confidence, Model: reply.Served, Device: "unknown", Protocol: runtime, ConfidenceSource: "self_reported"}, nil
}

// Catalog availability is not native readiness, inference, or GPU residency.
func (c *Client) chatHealth(ctx context.Context) (Health, error) {
	runtime := c.expected.Runtime
	if !chatRuntime(runtime) {
		return Health{}, fmt.Errorf("unsupported decision runtime %q", runtime)
	}
	url := c.baseURL + "/models"
	if runtime == llm.RuntimeOllama {
		url = strings.TrimSuffix(c.baseURL, "/v1") + "/api/tags"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Health{}, err
	}
	response, err := c.http.Do(req)
	if err != nil {
		return Health{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Health{}, fmt.Errorf("judge model catalog HTTP %d", response.StatusCode)
	}
	var catalog struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err = json.NewDecoder(response.Body).Decode(&catalog); err != nil {
		return Health{}, err
	}
	found := false
	for _, m := range catalog.Data {
		found = found || m.ID == c.model
	}
	for _, m := range catalog.Models {
		found = found || m.Name == c.model || m.Model == c.model
	}
	if !found {
		return Health{Status: "model_unavailable", Requested: c.expected}, fmt.Errorf("judge model %q is absent from selected server catalog", c.model)
	}
	model := c.model
	return Health{Status: "model_available", Model: &model, Requested: c.expected}, nil
}
