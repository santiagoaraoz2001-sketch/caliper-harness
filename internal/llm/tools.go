package llm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// ToolCall retains original decoded argument JSON, never float-rounded slots.
type ToolCall struct{ ID, Name, Arguments string }
type ToolDefinition struct {
	// TailPublished is set only by the host after checking the final retained
	// current planning publication. It changes Rulith placement, not authority.
	TailPublished bool   `json:"-"`
	Type          string `json:"type"`
	Function      struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

// LatePlanningSchema reports the one transport with harness-owned raw rendering.
func (c *Client) LatePlanningSchema() bool {
	return c.runtime == RuntimeRulith && c.ToolProtocol() == "structured"
}

type Reply struct {
	// RawNative is a native completion, parsed by the owning registry before
	// any tool can execute. It is never promoted to Message.Content here.
	RawNative          string
	NativeInitialState string
	RawContentStreamed string          // exact leading normal prose already delivered to onDelta
	Prefix             *ResponsePrefix // declared native prefix; never reconstructed from parsed message fields
	OutputLimit        bool            // untainted length terminal; transport/cancellation failures never set it
	Message            Message
	Served             string
	Usage              Usage
	FinishReason       string
	ThinkingOnly       bool // validated normal completion; no answer or function call
}

// AuxDelta is original decoded streamed text: content or an auxiliary field.
type AuxDelta struct{ Field, Delta string }
type AuxObserver func(context.Context, AuxDelta) error
type auxObserverKey struct{}

// WithAuxObserver observes content and auxiliary text within this request, not the Client.
// Returning an error stops local consumption; remote settlement remains unknown.
func WithAuxObserver(ctx context.Context, observer AuxObserver) context.Context {
	return context.WithValue(ctx, auxObserverKey{}, observer)
}

func observeAux(ctx context.Context, field, delta string) error {
	observer, _ := ctx.Value(auxObserverKey{}).(AuxObserver)
	if observer == nil || delta == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := observer(ctx, AuxDelta{field, delta}); err != nil {
		return fmt.Errorf("stream text observer: %w", err)
	}
	return ctx.Err()
}

func (m Message) Clone() Message { m.ToolCalls = append([]ToolCall(nil), m.ToolCalls...); return m }
func (m Message) Size() int {
	n := len(m.Content) + len(m.Name) + len(m.ToolCallID) + len(m.Reasoning) + len(m.ReasoningContent) + len(m.Thinking)
	for _, c := range m.ToolCalls {
		n += len(c.ID) + len(c.Name) + len(c.Arguments) + 32
	}
	return n
}
func (c ToolCall) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"id": c.ID, "type": "function", "function": map[string]string{"name": c.Name, "arguments": c.Arguments}})
}
func (c *ToolCall) UnmarshalJSON(b []byte) error {
	var v struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	if v.Type != "function" {
		return fmt.Errorf("unsupported call type %q", v.Type)
	}
	*c = ToolCall{v.ID, v.Function.Name, v.Function.Arguments}
	return nil
}
func (c *Client) WithToolProtocol(p string) *Client { c.toolProtocol = p; return c }
func (c *Client) ToolProtocol() string              { return ToolProtocol(c.toolProtocol) }
func ToolProtocol(protocol string) string {
	if protocol == "" {
		return "structured"
	}
	return protocol
}

// Tool turns always stream, independent of display. Direct Chat retains its policy.
func (c *Client) ChatTools(ctx context.Context, msgs []Message, tools []ToolDefinition, onDelta func(string)) (reply Reply, err error) {
	started := time.Now()
	defer func() { c.observeRequest(started, reply.Served, reply.Usage, err) }()
	previous, continuing := ctx.Value(responseContinuationKey{}).(Reply)
	if continuing && c.runtime != RuntimeMLX && c.runtime != RuntimeRulith {
		return reply, fmt.Errorf("selected runtime cannot continue an unfinished native response; an explicit native prefix contract is required")
	}
	if c.ToolProtocol() != "structured" && c.ToolProtocol() != "text" {
		return reply, fmt.Errorf("invalid model.tool_protocol %q", c.ToolProtocol())
	}
	rawContinuation := continuing && previous.Prefix != nil && previous.Prefix.Strategy == RulithRawPrefix
	if rawContinuation && c.runtime != RuntimeRulith {
		return reply, fmt.Errorf("unfinished Rulith response cannot change runtime")
	}
	if rawContinuation && c.ToolProtocol() != "structured" {
		return reply, fmt.Errorf("unfinished native response cannot change tool protocol")
	}
	if c.ToolProtocol() == "text" {
		for _, m := range msgs {
			if len(m.ToolCalls) > 0 || m.Role == "tool" {
				return reply, fmt.Errorf("native call history cannot be sent through text compatibility; start a new chat or select structured (raw session retained)")
			}
		}
		tools = nil
	}
	idle := c.idleTimeout
	if idle <= 0 {
		idle = 5 * time.Minute
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	guard := &idleGuard{cancel: cancel}
	guard.timer = time.AfterFunc(idle, func() { cancel(fmt.Errorf("model stream inactive for %s; generation settlement unknown", idle)) })
	defer guard.stop()
	if c.runtime == RuntimeOllama && len(tools) > 0 {
		meta := c.modelMetadata(context.WithValue(ctx, idleKey{}, guard))
		if meta != nil && meta.tools != nil && !*meta.tools {
			return reply, fmt.Errorf("selected Ollama model does not advertise tools; select explicit text compatibility or a compatible model")
		}
	}
	reply.Message.Role = "assistant"
	// Judge/compaction callers use this method for streaming without tools and
	// consume ordinary parsed messages. Only structured tool generation uses raw
	// framing. A retained raw prefix still validates identity if tools disappear;
	// it must never fall through into a fresh chat request.
	if c.runtime == RuntimeRulith && (len(tools) > 0 || rawContinuation) {
		reply, err = c.chatRulithRaw(context.WithValue(ctx, idleKey{}, guard), msgs, tools, onDelta)
	} else {
		_, _, _, err = c.chatStream(context.WithValue(ctx, idleKey{}, guard), msgs, onDelta, tools, &reply)
	}
	if err == nil && context.Cause(ctx) != nil {
		err = context.Cause(ctx)
	}
	if err != nil && context.Cause(ctx) != nil {
		reply.OutputLimit = false
		err = fmt.Errorf("%w: %w", err, context.Cause(ctx))
	}
	reply.ThinkingOnly = err == nil && reply.thinkingOnly()
	return reply, err
}

func (r Reply) thinkingOnly() bool {
	return r.FinishReason == "stop" && strings.TrimSpace(r.Message.Content) == "" && len(r.Message.ToolCalls) == 0 &&
		(r.Message.Reasoning != "" || r.Message.ReasoningContent != "" || r.Message.Thinking != "")
}

type idleKey struct{}
type idleGuard struct {
	mu     sync.Mutex
	timer  *time.Timer
	cancel context.CancelCauseFunc
}

func (g *idleGuard) stop() { g.mu.Lock(); defer g.mu.Unlock(); g.timer.Stop() }

type activeReader struct {
	io.ReadCloser
	g    *idleGuard
	idle time.Duration
}

func (r activeReader) Read(b []byte) (int, error) {
	n, e := r.ReadCloser.Read(b)
	if n > 0 {
		r.g.mu.Lock()
		r.g.timer.Reset(r.idle)
		r.g.mu.Unlock()
	}
	return n, e
}

// isTimeoutError reports client/deadline timeouts, which must surface
// as-is rather than being replayed by the transport retry below.
func isTimeoutError(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func (c *Client) do(req *http.Request, capture *Reply) (*http.Response, error) {
	client := c.http
	if capture != nil || req.Context().Value(idleKey{}) != nil {
		copy := *client
		copy.Timeout = 0
		client = &copy
	}
	resp, err := client.Do(req)
	if err != nil && resp == nil && req.GetBody != nil && req.Context().Err() == nil && !isTimeoutError(err) {
		// One retry for a transport failure before any response bytes
		// (server restart / dropped keep-alive): the request never
		// started generation, so replaying the identical body is safe.
		if body, bodyErr := req.GetBody(); bodyErr == nil {
			retry := req.Clone(req.Context())
			retry.Body = body
			resp, err = client.Do(retry)
		}
	}
	if err == nil {
		if g, ok := req.Context().Value(idleKey{}).(*idleGuard); ok {
			idle := c.idleTimeout
			if idle <= 0 {
				idle = 5 * time.Minute
			}
			g.mu.Lock()
			g.timer.Reset(idle)
			g.mu.Unlock()
			resp.Body = activeReader{resp.Body, g, idle}
		}
	}
	return resp, err
}

type callDelta struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func assembleCalls(dst map[int]ToolCall, deltas []callDelta) error {
	for _, d := range deltas {
		if d.Index == nil || *d.Index < 0 {
			return fmt.Errorf("tool delta has no valid index")
		}
		if d.Type != "" && d.Type != "function" {
			return fmt.Errorf("unsupported tool delta type")
		}
		v := dst[*d.Index]
		if d.ID != "" {
			if v.ID != "" && v.ID != d.ID {
				return fmt.Errorf("tool delta changed ID")
			}
			v.ID = d.ID
		}
		v.Name += d.Function.Name
		v.Arguments += d.Function.Arguments
		dst[*d.Index] = v
	}
	return nil
}
func orderedCalls(src map[int]ToolCall) ([]ToolCall, error) {
	keys := make([]int, 0, len(src))
	for k := range src {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var out []ToolCall
	for i, k := range keys {
		if k != i {
			return out, fmt.Errorf("noncontiguous call indices")
		}
		out = append(out, src[k])
	}
	return out, nil
}
func nativeCalls(raw json.RawMessage) ([]ToolCall, error) {
	var wire []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	}
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	out := make([]ToolCall, 0, len(wire))
	for _, v := range wire {
		args := strings.TrimSpace(string(v.Function.Arguments))
		if !strings.HasPrefix(args, "{") {
			return nil, fmt.Errorf("native call arguments must be an object")
		}
		out = append(out, ToolCall{v.ID, v.Function.Name, string(v.Function.Arguments)})
	}
	return out, nil
}
func assignNativeIDs(calls []ToolCall) error {
	for i := range calls {
		if calls[i].ID == "" {
			var b [12]byte
			if _, err := rand.Read(b[:]); err != nil {
				return err
			}
			calls[i].ID = "local-" + hex.EncodeToString(b[:])
		}
	}
	return nil
}
func nativeMessages(msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		v := map[string]any{"role": m.Role, "content": m.Content}
		if m.Thinking != "" {
			v["thinking"] = m.Thinking
		}
		if m.Reasoning != "" {
			v["reasoning"] = m.Reasoning
		}
		if m.ReasoningContent != "" {
			v["reasoning_content"] = m.ReasoningContent
		}
		if m.ToolCallID != "" {
			v["tool_call_id"] = m.ToolCallID
			v["tool_name"] = m.Name
		}
		if len(m.ToolCalls) > 0 {
			calls := []any{}
			for _, call := range m.ToolCalls {
				calls = append(calls, map[string]any{"id": call.ID, "function": map[string]any{"name": call.Name, "arguments": json.RawMessage(call.Arguments)}})
			}
			v["tool_calls"] = calls
		}
		out = append(out, v)
	}
	return out
}
