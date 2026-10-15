package tool

import (
	"specific-harness/internal/prompts"
	"strings"
)

// Call is an extracted tool block: a name plus raw slot strings.
type Call struct {
	Name  string
	Slots map[string]string
}

// Extract accepts exactly one tool block. Literal contents are data,
// including fence-looking lines; a second actual block refuses all calls.
// found=false means plain chat, not a call.
func Extract(msg string, catalogs ...*prompts.Catalog) (call Call, found bool, repair string) {
	pc := prompts.Select(catalogs...)
	lines := strings.Split(msg, "\n")
	start, end := -1, -1
	literal, sourceFence := "", ""
	for i, l := range lines {
		if literal != "" {
			if LiteralEnd(l, literal) {
				literal = ""
			}
			continue
		}
		trimmed := strings.TrimSpace(l)
		fence, info := markdownFence(trimmed)
		if sourceFence != "" {
			if len(fence) >= len(sourceFence) && fence[0] == sourceFence[0] && info == "" {
				sourceFence = ""
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if start < 0 || end >= 0 {
			// Token-prefixed tool intent is malformed, never a plain final answer.
			if strings.HasPrefix(trimmed, "<|") {
				if tokenEnd := strings.IndexByte(trimmed, '>'); tokenEnd >= 0 {
					f, tag := markdownFence(strings.TrimLeft(trimmed[tokenEnd+1:], ": \t"))
					if f == "```" && (tag == "tool" || strings.HasPrefix(tag, "tool") && len(tag) > 4 && strings.ContainsRune(" \t:=<", rune(tag[4]))) {
						return Call{}, true, pc.Text("repair.text.token_prefixed_fence")
					}
				}
			}
			if fence == "```" && strings.HasPrefix(info, "tool") && len(info) > 4 && strings.ContainsRune(" \t:=<", rune(info[4])) {
				return Call{}, true, pc.Text("repair.text.malformed_fence")
			}
			if fence != "" && trimmed != "```tool" {
				sourceFence = fence
				continue
			}
		}
		switch trimmed {
		case "```tool":
			if start >= 0 {
				return Call{}, true, pc.Text("repair.text.multiple_calls")
			}
			start = i
		case "```":
			if start >= 0 && end < 0 {
				end = i
			}
		default:
			if start >= 0 && end < 0 {
				literal, _ = LiteralStart(l)
			}
		}
	}
	if start < 0 {
		return Call{}, false, ""
	}
	if literal != "" {
		return Call{}, true, pc.Format("repair.text.unclosed_literal_fence", literal)
	}
	if end < 0 {
		return Call{}, true, pc.Text("repair.text.unclosed_fence")
	}
	return parseBody(lines[start+1:end], pc)
}

// Ordinary Markdown fences protect source examples, including tool markers.
// The executable protocol still requires the exact standalone ```tool opener.
func markdownFence(line string) (fence, info string) {
	if len(line) < 3 || line[0] != '`' && line[0] != '~' {
		return "", ""
	}
	n := 1
	for n < len(line) && line[n] == line[0] {
		n++
	}
	if n < 3 {
		return "", ""
	}
	return line[:n], strings.TrimSpace(line[n:])
}

// parseBody keeps simple scalars and schema-only requests. Multiline values
// require <<LABEL; every content line, including its newline, is copied.
func parseBody(body []string, catalogs ...*prompts.Catalog) (Call, bool, string) {
	pc := prompts.Select(catalogs...)
	call := Call{Slots: map[string]string{}}
	toolSeen := false
	closedKey, closedLabel := "", ""
	// Bound diagnostic names to 24 runes; the ellipsis marks abbreviation.
	// Repairs describe the boundary without copying raw command data.
	shortName := func(s string) string {
		runes := []rune(s)
		if len(runes) > 24 {
			return string(runes[:24]) + "…"
		}
		return s
	}
	for i := 0; i < len(body); i++ {
		raw := body[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, val, ok := cutSlot(raw)
		if !ok {
			if closedLabel != "" {
				return Call{}, true, pc.Format("repair.text.after_literal_end", closedKey, closedLabel)
			}
			return Call{}, true, pc.Text("repair.text.slot_syntax")
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		if key == "tool" {
			if toolSeen {
				return Call{}, true, pc.Text("repair.text.duplicate_tool")
			}
			toolSeen = true
			if strings.HasPrefix(val, "<<") {
				return Call{}, true, pc.Text("repair.text.scalar_tool_name")
			}
			call.Name = val
			continue
		}
		if call.Name == "" {
			return Call{}, true, pc.Text("repair.text.missing_tool")
		}
		if _, duplicate := call.Slots[key]; duplicate {
			return Call{}, true, pc.Format("repair.text.duplicate_slot", key)
		}
		if strings.HasPrefix(val, "<<") {
			label, ok := literalLabel(val)
			if !ok {
				if strings.HasPrefix(val, "<<'") || strings.HasPrefix(val, "<<\"") {
					return Call{}, true, pc.Format("repair.text.quoted_tool_delimiter", shortName(key))
				}
				return Call{}, true, pc.Format("repair.text.invalid_literal_delimiter", key)
			}
			var text strings.Builder
			i++
			for ; i < len(body) && !LiteralEnd(body[i], label); i++ {
				text.WriteString(body[i])
				text.WriteByte('\n')
			}
			if i == len(body) {
				return Call{}, true, pc.Format("repair.text.unclosed_literal_slot", label)
			}
			closedKey, closedLabel = shortName(key), shortName(label)
			val = text.String()
		}
		call.Slots[key] = val
	}
	if call.Name == "" {
		return Call{}, true, pc.Text("repair.text.missing_tool")
	}
	return call, true, ""
}

// cutSlot uses the first separator, so colons/assignments in a scalar value
// cannot change its key. Unmarked continuation lines are not slot values.
func cutSlot(line string) (key, val string, ok bool) {
	i := strings.IndexAny(line, ":=")
	if i < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:i])
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	return key, line[i+1:], true
}

func literalLabel(val string) (string, bool) {
	if !strings.HasPrefix(val, "<<") || len(val) == 2 {
		return "", false
	}
	label := val[2:]
	for _, c := range label {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return "", false
		}
	}
	return label, true
}

// LiteralStart shares the tool protocol's slot and delimiter rules with
// display. Marked is true for an invalid << declaration too, allowing display
// to hide its ambiguous tail while the executable parser still refuses it.
// Blank lines and comments cannot arm a literal.
func LiteralStart(line string) (label string, marked bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	_, value, ok := cutSlot(line)
	value = strings.TrimSpace(value)
	if !ok || !strings.HasPrefix(value, "<<") {
		return "", false
	}
	label, _ = literalLabel(value)
	return label, true
}

// LiteralEnd requires the exact unindented label, with an optional CRLF CR.
func LiteralEnd(line, label string) bool {
	return label != "" && strings.TrimSuffix(line, "\r") == label
}

// MarkdownFence shares the executable parser's ordinary source-fence boundary.
func MarkdownFence(line string) (string, string) { return markdownFence(strings.TrimSpace(line)) }

// DisplayBodyLine reuses slot syntax without changing executable parsing.
// Unsupported multiline scalars and unmarked continuations have ambiguous tails.
func DisplayBodyLine(line string) (slot, ambiguous bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false, false
	}
	key, value, ok := cutSlot(line)
	if !ok {
		return false, true
	}
	slot = !strings.EqualFold(key, "tool")
	parts := strings.Fields(value)
	if len(parts) == 1 || len(parts) > 1 && strings.HasPrefix(parts[1], "#") {
		switch parts[0] {
		case "|", ">", "|-", "|+", ">-", ">+":
			return true, true // A marker in any field establishes display ambiguity.
		}
	}
	return slot, false
}
