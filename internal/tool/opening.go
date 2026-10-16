package tool

import "sort"

// NormalizeOpening removes unsupported fields only from owner-declared harmless
// openings. Callers must parse a complete, unambiguous tool call first and disclose
// discarded names in its receipt. Supported fields retain their original spelling
// and value so ordinary validation still rejects conflicting aliases or bad types.
// This does not infer arguments or grant permission to execute a tool.
func NormalizeOpening(d Definition, input Args) (Args, []string) {
	if !d.Opening {
		return input, nil
	}
	out := make(Args, len(input))
	var discarded []string
	for name, value := range input {
		if d.hasParam(argumentName(d, name)) {
			out[name] = value
		} else {
			discarded = append(discarded, name)
		}
	}
	sort.Strings(discarded)
	return out, discarded
}
