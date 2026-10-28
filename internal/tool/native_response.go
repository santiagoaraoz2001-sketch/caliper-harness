package tool

import (
	"encoding/json"
	"fmt"
	"io"
	"specific-harness/internal/llm"
	"strings"
	"unicode/utf8"
)

// ParseNativeResponse frames a completed raw native reply. It never executes or
// repairs calls: any rejected frame discards the entire message. Schema
// admission and correlated refusals remain owned by ordinary native dispatch. Transport owns
// completion/continuation; a truncated transfer must not be passed here.
func (r *Registry) ParseNativeResponse(raw, initialState, callPrefix string) (llm.Message, error) {
	msg := llm.Message{Role: "assistant"}
	if !utf8.ValidString(raw) || (initialState != "normal" && initialState != "reasoning") {
		return llm.Message{}, fmt.Errorf("invalid native response encoding or initial state")
	}
	s := raw
	reasoned := initialState == "reasoning"
	if initialState == "reasoning" {
		i := strings.Index(s, "</think>")
		if i < 0 {
			return llm.Message{}, fmt.Errorf("unclosed native reasoning")
		}
		msg.ReasoningContent, s = s[:i], s[i+len("</think>"):]
	}
	for s != "" {
		i := strings.Index(s, "<")
		if i < 0 {
			msg.Content += s
			break
		}
		msg.Content += s[:i]
		s = s[i:]
		switch {
		case strings.HasPrefix(s, "<think>"):
			if reasoned || strings.TrimSpace(msg.Content) != "" || len(msg.ToolCalls) != 0 {
				return llm.Message{}, fmt.Errorf("unexpected native reasoning boundary")
			}
			end := strings.Index(s[len("<think>"):], "</think>")
			if end < 0 {
				return llm.Message{}, fmt.Errorf("unclosed native reasoning")
			}
			end += len("<think>")
			msg.ReasoningContent, s = s[len("<think>"):end], s[end+len("</think>"):]
			reasoned = true
		case strings.HasPrefix(s, "<tool_call>"):
			if strings.TrimSpace(callPrefix) == "" {
				return llm.Message{}, fmt.Errorf("native calls require an owning ID prefix")
			}
			call, consumed, err := r.parseNativeCall(s)
			if err != nil {
				// Only a captured call draft can enter the existing framing repair.
				// Admission/schema refusals remain ordinary errors, not guesses.
				if framing, ok := err.(*llm.ToolFramingError); ok {
					framing.CallIndex = len(msg.ToolCalls)
				}
				return llm.Message{}, err
			}
			call.ID = fmt.Sprintf("%s-%d", callPrefix, len(msg.ToolCalls))
			msg.ToolCalls = append(msg.ToolCalls, call)
			s = s[consumed:]
		case strings.HasPrefix(s, "</think>") || strings.HasPrefix(s, "</tool_call>") ||
			strings.HasPrefix(s, "<function=") || strings.HasPrefix(s, "</function>") ||
			strings.HasPrefix(s, "<parameter=") || strings.HasPrefix(s, "</parameter>") ||
			strings.HasPrefix(s, "<tool_call") || strings.HasPrefix(s, "</tool_call") || strings.HasPrefix(s, "<think") || strings.HasPrefix(s, "</think"):
			return llm.Message{}, fmt.Errorf("unexpected native framing boundary")
		default:
			msg.Content += "<"
			s = s[1:]
		}
	}
	return msg, nil
}

func (r *Registry) parseNativeCall(raw string) (llm.ToolCall, int, error) {
	s := strings.TrimLeft(raw[len("<tool_call>"):], " \t\r\n")
	var name, arguments string
	var err error
	if strings.HasPrefix(s, "{") {
		var fields map[string]json.RawMessage
		fields, s, err = nativeEnvelope(s)
		if err == nil {
			if len(fields) != 2 || fields["name"] == nil || fields["arguments"] == nil {
				err = fmt.Errorf("native JSON call requires only name and arguments")
			} else if err = json.Unmarshal(fields["name"], &name); err == nil {
				arguments = string(fields["arguments"])
			}
		}
	} else if strings.HasPrefix(s, "<function=") {
		name, arguments, s, err = r.nativeXML(s)
	} else {
		err = nativeFraming(raw, "expected native function or JSON call")
	}
	if err != nil {
		if framing, ok := err.(*llm.ToolFramingError); ok {
			framing.Draft = raw
		}
		return llm.ToolCall{}, 0, err
	}
	s = strings.TrimLeft(s, " \t\r\n")
	if !strings.HasPrefix(s, "</tool_call>") {
		return llm.ToolCall{}, 0, nativeFraming(raw, "missing native tool closing boundary")
	}
	consumed := len(raw) - len(s) + len("</tool_call>")
	if err := nativeJSONUnambiguous(arguments); err != nil {
		return llm.ToolCall{}, 0, fmt.Errorf("native %s arguments: %w", name, err)
	}
	return llm.ToolCall{Name: name, Arguments: arguments}, consumed, nil
}

// Check only unambiguous JSON, not the advertised argument schema. Ordinary
// native admission owns unknown names, missing fields, shape and type refusals.
func nativeJSONUnambiguous(raw string) error {
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	if err := nativeJSONValue(d); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing native arguments")
	}
	return nil
}

func nativeJSONValue(d *json.Decoder) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	delim, container := tok.(json.Delim)
	if !container {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			tok, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := tok.(string)
			if !ok || keys[key] {
				return fmt.Errorf("duplicate or invalid native argument key")
			}
			keys[key] = true
			if err := nativeJSONValue(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := nativeJSONValue(d); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON closing delimiter")
	}
	_, err = d.Token()
	return err
}

// Decode one JSON envelope, leaving framing bytes untouched. RawMessage keeps
// argument string escapes and lexical numbers; duplicate members never win.
func nativeEnvelope(s string) (map[string]json.RawMessage, string, error) {
	d := json.NewDecoder(strings.NewReader(s))
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return nil, s, fmt.Errorf("invalid native JSON envelope")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return nil, s, err
		}
		key, ok := tok.(string)
		if !ok || fields[key] != nil {
			return nil, s, fmt.Errorf("duplicate or invalid native envelope key")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, s, err
		}
		fields[key] = value
	}
	if tok, err := d.Token(); err != nil || tok != json.Delim('}') {
		return nil, s, fmt.Errorf("unclosed native JSON envelope")
	}
	return fields, s[d.InputOffset():], nil
}

func (r *Registry) nativeXML(s string) (string, string, string, error) {
	name, s, ok := strings.Cut(strings.TrimPrefix(s, "<function="), ">")
	if !ok || name == "" {
		return "", "", s, nativeFraming(s, "invalid native function opening")
	}
	d, _ := r.Lookup(name)
	values := Args{}
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		if strings.HasPrefix(s, "</function>") {
			data, err := json.Marshal(values)
			return name, string(data), s[len("</function>"):], err
		}
		if !strings.HasPrefix(s, "<parameter=") {
			return "", "", s, nativeFraming(s, "expected native parameter or function close")
		}
		key, rest, ok := strings.Cut(s[len("<parameter="):], ">")
		if !ok || key == "" {
			return "", "", s, nativeFraming(s, "invalid native parameter opening")
		}
		if _, exists := values[key]; exists {
			return "", "", s, fmt.Errorf("duplicate native parameter %q", key)
		}
		end := nativeParameterEnd(rest)
		if end < 0 {
			return "", "", s, nativeFraming(s, "missing native parameter closing boundary")
		}
		// Qwen's framing consumes one LF on each side; all other bytes belong
		// to the argument, including indentation, Unicode and marker literals.
		value := strings.TrimSuffix(strings.TrimPrefix(rest[:end], "\n"), "\n")
		values[key] = value
		for _, p := range d.Params {
			if p.Name == key {
				v, gap := coerce(name, p, value, d.Prompts)
				if gap == "" {
					values[key] = v
				}
				break
			}
		}
		s = rest[end+len("</parameter>"):]
	}
}

func nativeParameterEnd(s string) int {
	for offset := 0; offset < len(s); {
		i := strings.Index(s[offset:], "</parameter>")
		if i < 0 {
			return -1
		}
		i += offset
		rest := strings.TrimLeft(s[i+len("</parameter>"):], " \t\r\n")
		if strings.HasPrefix(rest, "<parameter=") || strings.HasPrefix(rest, "</function>") {
			return i
		}
		offset = i + len("</parameter>")
	}
	return -1
}

func nativeFraming(raw, reason string) error {
	// At a malformed frame its end is not trustworthy (a closing marker may
	// be argument data). Keep the complete remaining captured draft at the
	// transport's clean stop instead of inventing a repaired tool boundary.
	return &llm.ToolFramingError{Draft: raw, Reason: reason, Boundary: "stop"}
}
