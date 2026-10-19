package tool

import "strings"

// ReconstructCall proposes a call from a captured native framing failure. It
// neither executes nor authorizes it: the decision model must select the
// candidate, and the caller must perform normal current-state admission.
// The caller retains raw as evidence; only delimiter spelling is repaired.
func (r *Registry) ReconstructCall(raw string) (Definition, Args, string, bool) {
	refuse := func() (Definition, Args, string, bool) { return Definition{}, nil, "", false }
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(s, "<tool_call>")), "<function=") {
		return r.reconstructQwen(s)
	}
	wrapped := strings.HasPrefix(s, "<tool_call>")
	if wrapped {
		s = strings.TrimPrefix(s, "<tool_call>")
		if !strings.HasSuffix(s, "</tool_call>") {
			return refuse()
		}
		s = strings.TrimSuffix(s, "</tool_call>")
	}
	if strings.Contains(s, "<tool_call>") || strings.Contains(s, "</tool_call>") {
		return refuse() // Never merge calls or reinterpret a value containing framing.
	}
	i := strings.Index(s, "<arg_key>")
	if i < 0 {
		if !wrapped {
			return refuse() // Plain prose or a tool name alone is not a failed call.
		}
		i = len(s)
	}
	d, ok := r.ResolveName(strings.TrimSpace(s[:i]))
	if !ok {
		return refuse()
	}
	body := strings.TrimSpace(s[i:])
	// These are empty argument-container artifacts, not authored arguments.
	// Only an owner-labelled, argument-free opening can discard them. The
	// second form is the captured unfinished empty-container pair.
	if d.Opening && len(d.Params) == 0 && (body == "<arg_key></arg_key><arg_value>{}</arg_value>" ||
		body == "<arg_key>argument_keys></arg_key><arg_value>{}</arg_value><arg_key>argument_values>") {
		return d, Args{}, "remove empty argument-container framing from an argument-free opening", true
	}
	slots := map[string]string{}
	seen := map[string]bool{}
	for body != "" {
		if !strings.HasPrefix(body, "<arg_key>") {
			return refuse()
		}
		body = strings.TrimPrefix(body, "<arg_key>")
		end := strings.Index(body, "</arg_key>")
		if end < 0 {
			return refuse()
		}
		key := body[:end]
		canonical := ArgumentName(d, key)
		if !d.hasParam(canonical) || seen[canonical] {
			return refuse() // Never drop unknown authored values or combine duplicates.
		}
		seen[canonical] = true
		body = strings.TrimSpace(body[end+len("</arg_key>"):])
		if !strings.HasPrefix(body, "<arg_value>") {
			return refuse()
		}
		body = strings.TrimPrefix(body, "<arg_value>")
		end = strings.Index(body, "</arg_value>")
		if end < 0 {
			return refuse() // Missing code, path or command bytes cannot be supplied.
		}
		value := body[:end]
		if strings.Contains(value, "<arg_key>") || strings.Contains(value, "<arg_value>") || strings.Contains(value, "</arg_key>") {
			return refuse()
		}
		slots[key] = value
		body = strings.TrimSpace(body[end+len("</arg_value>"):])
	}
	args, gap := Validate(d, slots)
	if gap != "" {
		return refuse()
	}
	return d, args, "reconstruct complete scalar arguments using the current tool schema", true
}

// reconstructQwen accepts complete authored parameter spans, even when the
// settled generation omitted the outer closing tags. It never fills a value
// or resolves duplicate fields; those need correction by the worker.
func (r *Registry) reconstructQwen(raw string) (Definition, Args, string, bool) {
	refuse := func() (Definition, Args, string, bool) { return Definition{}, nil, "", false }
	s := strings.TrimSpace(strings.TrimPrefix(raw, "<tool_call>"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "</tool_call>"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "</function>"))
	name, body, ok := strings.Cut(strings.TrimPrefix(s, "<function="), ">")
	if !ok {
		return refuse()
	}
	d, ok := r.ResolveName(name)
	if !ok {
		return refuse()
	}
	slots := map[string]string{}
	for body = strings.TrimSpace(body); body != ""; body = strings.TrimSpace(body) {
		if !strings.HasPrefix(body, "<parameter=") {
			return refuse()
		}
		key, rest, ok := strings.Cut(strings.TrimPrefix(body, "<parameter="), ">")
		if !ok {
			return refuse()
		}
		key = ArgumentName(d, key)
		if !d.hasParam(key) {
			return refuse()
		}
		if _, exists := slots[key]; exists {
			return refuse()
		}
		value, rest, ok := strings.Cut(rest, "</parameter>")
		if !ok {
			return refuse()
		}
		// Nested framing is ambiguous, not a basis for guessing argument ends.
		for _, marker := range []string{"<parameter=", "<function=", "</function>", "<tool_call>", "</tool_call>"} {
			if strings.Contains(value, marker) {
				return refuse()
			}
		}
		// Qwen treats one LF at either boundary as parameter framing.
		// Match both native runtimes; retain additional whitespace verbatim.
		value = strings.TrimPrefix(value, "\n")
		value = strings.TrimSuffix(value, "\n")
		slots[key], body = value, rest
	}
	args, gap := Validate(d, slots)
	if gap != "" {
		return refuse()
	}
	return d, args, "reconstruct complete Qwen parameter spans using the current tool schema", true
}
