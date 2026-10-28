package plan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// DisplayText keeps ordinary paragraphs readable and makes terminal control
// bytes literal. It is a display only; stored contracts remain byte exact.
func DisplayText(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DisplayStructured renders existing JSON document fields without exposing JSON
// quoting as prose or dropping unknown fields. Plain-text legacy values survive.
func DisplayStructured(value string) string {
	if !json.Valid([]byte(value)) {
		return DisplayText(value)
	}
	var v any
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if decoder.Decode(&v) != nil {
		return DisplayText(value)
	}
	if _, ok := v.(map[string]any); !ok {
		return DisplayText(value)
	}
	var b strings.Builder
	var render func(any, string)
	render = func(value any, indent string) {
		switch v := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&b, "%s%s: ", indent, DisplayText(k))
				switch v[k].(type) {
				case map[string]any, []any:
					b.WriteByte('\n')
					render(v[k], indent+"  ")
				default:
					render(v[k], "")
				}
			}
		case []any:
			for _, item := range v {
				b.WriteString(indent + "- ")
				render(item, indent+"  ")
			}
		case string:
			b.WriteString(DisplayText(v))
			b.WriteByte('\n')
		default:
			fmt.Fprintln(&b, v)
		}
	}
	render(v, "")
	return strings.TrimSuffix(b.String(), "\n")
}
