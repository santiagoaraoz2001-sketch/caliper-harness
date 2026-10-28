package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"specific-harness/internal/llm"
	"specific-harness/internal/prompts"
	"strings"
)

// ErrAmbiguousArguments marks argument bytes whose meaning cannot be
// settled (duplicate keys, trailing data, not one object). Those stay
// hard failures in native dispatch; unambiguous shape errors (nested
// values, wrong scalars) are repairable refusals instead.
var ErrAmbiguousArguments = errors.New("ambiguous arguments")

// JSONObject keeps scalar lexical numbers and refuses ambiguous input before effects.
// One respelling is spliced: a sole GraphQL-style {"input": {...}} wrapper,
// which models copy from the graph API. Everything else nested stays refused.
func JSONObject(raw string) (map[string]any, error) {
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("%w: arguments must be a JSON object", ErrAmbiguousArguments)
	}
	out := map[string]any{}
	if e := jsonMembers(d, out, true); e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, fmt.Errorf("%w: trailing JSON arguments", ErrAmbiguousArguments)
	}
	return out, nil
}

// jsonMembers reads object members after its opening brace into out.
// Only the top-level "input" member may itself be an object; its members
// splice into out under the same duplicate and scalar rules.
func jsonMembers(d *json.Decoder, out map[string]any, top bool) error {
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return e
		}
		k, ok := key.(string)
		if !ok {
			return fmt.Errorf("invalid argument key")
		}
		v, e := d.Token()
		if e != nil {
			return e
		}
		if delim, isDelim := v.(json.Delim); isDelim && delim == '{' && top && k == "input" {
			if e := jsonMembers(d, out, false); e != nil {
				return e
			}
			continue
		}
		switch v.(type) {
		case string, json.Number, bool:
		default:
			return fmt.Errorf("argument %q must be a scalar string, number or boolean", k)
		}
		if _, ok := out[k]; ok {
			return fmt.Errorf("%w: duplicate argument %q", ErrAmbiguousArguments, k)
		}
		out[k] = v
	}
	if _, e := d.Token(); e != nil { // closing brace
		return e
	}
	return nil
}

// ValidJSONObject reports whether raw is exactly one syntactically valid
// JSON object. Archive integrity uses it for saved model calls: refused
// calls must remain storable evidence even when their argument values are
// nested or otherwise unexecutable. Execution keeps the scalar rules in
// JSONObject/ValidateJSON.
func ValidJSONObject(raw string) bool {
	d := json.NewDecoder(strings.NewReader(raw))
	var obj map[string]json.RawMessage
	if err := d.Decode(&obj); err != nil || obj == nil {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}

func ValidateJSON(d Definition, raw string) (Args, string) {
	v, e := JSONObject(raw)
	if e != nil {
		return nil, e.Error() + d.Prompts.Text("repair.native.corrected_call_suffix")
	}
	return validate(d, nil, v)
}
func nativeValue(name string, p Param, v any, catalogs ...*prompts.Catalog) (any, string) {
	pc := prompts.Select(catalogs...)
	bad := func() (any, string) { return nil, fmt.Sprintf("%s needs a JSON %s for %s", name, p.Type, p.Name) }
	if s, ok := v.(string); ok && p.Type != TypeString {
		value, repair := coerce(name, p, s, pc)
		if repair != "" {
			return bad()
		}
		return value, ""
	}
	switch p.Type {
	case TypeString:
		s, ok := v.(string)
		if !ok {
			return bad()
		}
		if p.Required && !p.AllowEmpty && s == "" {
			return bad()
		}
		return coerce(name, p, s, pc)
	case TypeInt:
		n, ok := v.(json.Number)
		if !ok {
			return bad()
		}
		i, e := n.Int64()
		if e != nil {
			return bad()
		}
		return i, ""
	case TypeFloat:
		n, ok := v.(json.Number)
		if !ok {
			return bad()
		}
		f, e := n.Float64()
		if e != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return bad()
		}
		return f, ""
	case TypeBool:
		b, ok := v.(bool)
		if !ok {
			return bad()
		}
		return b, ""
	default:
		return bad()
	}
}

// NativeDefinitions is generated from the same owning primitive registry schema.
func (r *Registry) NativeDefinitions() ([]llm.ToolDefinition, error) {
	var out []llm.ToolDefinition
	for _, n := range r.Names() {
		d, ok := r.Lookup(n)
		if !ok || n == "" || d.Name != n || strings.TrimSpace(n) != n {
			return nil, fmt.Errorf("invalid tool declaration %q", n)
		}
		props := map[string]any{}
		required := []string{}
		for _, p := range d.Params {
			if p.Name == "" || strings.TrimSpace(p.Name) != p.Name {
				return nil, fmt.Errorf("invalid parameter name %q", p.Name)
			}
			if _, duplicate := props[p.Name]; duplicate {
				return nil, fmt.Errorf("duplicate parameter %q", p.Name)
			}
			typ := map[string]string{TypeString: "string", TypeInt: "integer", TypeFloat: "number", TypeBool: "boolean"}[p.Type]
			if typ == "" {
				return nil, fmt.Errorf("invalid parameter type %s", p.Type)
			}
			v := map[string]any{"type": typ}
			if p.Desc != "" {
				v["description"] = p.Desc
			}
			if len(p.Enum) > 0 {
				if p.Type != TypeString {
					return nil, fmt.Errorf("nonstring enum %s", p.Name)
				}
				v["enum"] = p.Enum
			}
			if p.Required {
				required = append(required, p.Name)
			} else if p.Default != "" {
				value, q := coerce(d.Name, p, p.Default, d.Prompts)
				if q != "" {
					return nil, fmt.Errorf("invalid default: %s", q)
				}
				v["default"] = value
			}
			props[p.Name] = v
		}
		var def llm.ToolDefinition
		def.Type = "function"
		def.Function.Name = d.Name
		def.Function.Description = d.Desc + " " + d.Purpose
		if d.Name == "plan" || len(d.Example) > 0 || len(d.Params) == 0 {
			def.Function.Description += "\n" + d.CallTemplate()
		}
		def.Function.Parameters = map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
		out = append(out, def)
	}
	return out, nil
}
