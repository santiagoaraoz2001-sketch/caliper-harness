package tool

import (
	"math"
	"sort"
	"specific-harness/internal/prompts"
	"strconv"
	"strings"
)

// Validate checks every independent slot deterministically before dispatch.
// Invalid calls return no Args, with all discoverable repairs in one response.
func Validate(d Definition, raw map[string]string) (Args, string) { return validate(d, raw, nil) }
func validate(d Definition, raw map[string]string, native map[string]any) (Args, string) {
	input := native
	if native == nil {
		input = make(map[string]any, len(raw))
		for key, value := range raw {
			input[key] = value
		}
	}
	normalized, repair := normalizeFields(d, input)
	if repair != "" {
		return nil, repair
	}
	if native != nil {
		native = normalized
	} else {
		raw = make(map[string]string, len(normalized))
		for key, value := range normalized {
			raw[key] = value.(string)
		}
	}
	var repairs, unknown []string
	for k := range native {
		if !d.hasParam(k) {
			unknown = append(unknown, k)
		}
	}
	for k := range raw {
		if !d.hasParam(k) {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	names := strings.Join(d.slotNames(), ", ")
	if len(d.Params) == 0 {
		names = d.Prompts.Text("tool.call.no_arguments")
	}
	if native != nil && len(unknown) > 0 {
		for i := range unknown {
			unknown[i] = strconv.Quote(unknown[i])
		}
		repairs = append(repairs, d.Prompts.Format("repair.arguments.unknown_native", strings.Join(unknown, ", "), d.Name, names))
	} else {
		for _, k := range unknown {
			repairs = append(repairs, d.Prompts.Format("repair.arguments.unknown_text", d.Name, k, names, shapeHint(d)))
		}
	}
	args := Args{}
	for _, p := range d.Params {
		_, textPresent := raw[p.Name]
		_, nativePresent := native[p.Name]
		if !p.Required && p.Default == "" && !textPresent && !nativePresent {
			continue
		}
		if native != nil {
			if v, ok := native[p.Name]; ok {
				if p.Required && !p.AllowEmpty && p.Type == TypeString && v == "" {
					repairs = append(repairs, d.Prompts.Format("repair.arguments.nonempty", d.Name, p.Name))
					continue
				}
				value, q := nativeValue(d.Name, p, v, d.Prompts)
				if q != "" {
					repairs = append(repairs, q+"; correct these arguments against the advertised schema")
				} else {
					args[p.Name] = value
				}
				continue
			}
			if p.Required {
				repairs = append(repairs, d.Prompts.Format("repair.arguments.required_native", d.Name, p.Name, p.Desc))
				continue
			}
		}
		val, present := raw[p.Name]
		if !present || val == "" && !p.AllowEmpty {
			if p.Required {
				repairs = append(repairs, d.Prompts.Format("repair.arguments.required_text", d.Name, p.Name, p.Desc, needHint(p, d.Prompts), shapeHint(d)))
				continue
			}
			val = p.Default
		}
		coerced, q := coerce(d.Name, p, val, d.Prompts)
		if q != "" {
			repairs = append(repairs, q+shapeHint(d))
		} else {
			args[p.Name] = coerced
		}
	}
	if len(repairs) > 0 {
		repairs = append(repairs, d.Prompts.Format("repair.arguments.current_call", d.CallTemplate()))
		return nil, strings.Join(repairs, "\n")
	}
	return args, ""
}

// shapeHint restates the executable reply shape on every repair: bare
// lines are chat, only a fenced block runs.
func shapeHint(d Definition) string {
	return d.Prompts.Format("repair.text.complete_block_hint", d.Name)
}

func needHint(p Param, catalogs ...*prompts.Catalog) string {
	pc := prompts.Select(catalogs...)
	if len(p.Enum) > 0 {
		return pc.Format("repair.arguments.enum_hint", strings.Join(p.Enum, ", "))
	}
	return pc.Format("repair.arguments.type_hint", p.Type)
}

func coerce(tool string, p Param, val string, catalogs ...*prompts.Catalog) (any, string) {
	pc := prompts.Select(catalogs...)
	if len(p.Enum) > 0 && !inEnum(val, p.Enum, p.ExactEnum) {
		return nil, pc.Format("repair.arguments.enum", p.Name, strings.Join(p.Enum, ", "), val)
	}
	switch p.Type {
	case TypeString:
		return val, ""
	case TypeInt:
		n, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		if err != nil {
			return nil, pc.Format("repair.arguments.integer", p.Name, val)
		}
		return n, ""
	case TypeFloat:
		f, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, pc.Format("repair.arguments.number", p.Name, val)
		}
		return f, ""
	case TypeBool:
		b, ok := parseBool(val)
		if !ok {
			return nil, pc.Format("repair.arguments.boolean", p.Name, val)
		}
		return b, ""
	default:
		return nil, pc.Format("repair.arguments.harness_unknown_type", tool, p.Type, p.Name)
	}
}

func parseBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "y", "1":
		return true, true
	case "false", "no", "n", "0":
		return false, true
	}
	return false, false
}

func inEnum(val string, enum []string, exact bool) bool {
	for _, e := range enum {
		if val == e || !exact && strings.EqualFold(strings.TrimSpace(val), e) {
			return true
		}
	}
	return false
}

func (d Definition) hasParam(name string) bool {
	for _, p := range d.Params {
		if p.Name == name {
			return true
		}
	}
	return false
}

func (d Definition) slotNames() []string {
	names := make([]string, 0, len(d.Params))
	for _, p := range d.Params {
		names = append(names, p.Name)
	}
	return names
}
