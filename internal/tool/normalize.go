package tool

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"specific-harness/internal/prompts"
	"strings"
)

// ResolveName accepts only known spellings and unambiguous closing-call decoration.
// Compatibility names resolve through the canonical tool's current view and policy.
func (r *Registry) ResolveName(name string) (Definition, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexByte(key, '>'); i >= 0 {
		base, tail := strings.TrimSpace(key[:i]), strings.TrimSpace(key[i+1:])
		if tail != "" && tail != "</"+base+">" {
			return Definition{}, false
		}
		key = base
	}
	if key == "find" {
		_, canonical := r.tools["find_file"]
		if canonical {
			key = "find_file"
		}
	}
	return r.Lookup(key)
}

// Only these owner-known fields have compatibility spellings; unknown meaning
// remains an error. String data is never trimmed, decoded or rewritten here.
func argumentName(d Definition, name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if d.hasParam(key) {
		return key
	}
	if key == "command" && (d.Name == "bash" || d.Name == "check") && d.hasParam("cmd") {
		return "cmd"
	}
	if key == "file_path" && d.hasParam("path") {
		switch d.Name {
		case "read", "read_outside", "edit", "write", "grep", "find", "find_file", "find_dir", "find_outside":
			return "path"
		}
	}
	if d.Name == "plan" {
		if key == "method" && d.hasParam("execution") {
			return "execution"
		}
		if key == "goal" && d.hasParam("outcome") {
			return "outcome"
		}
	}
	return name
}

// planningVocabulary mirrors the data arguments the plan tool advertises
// across its per-question schemas (run.planningDefinition) plus captured
// entry spellings. The current question changes each turn, so a field from
// an earlier question is stale context, not an unknown meaning. The invoke
// and more discriminators are excluded: an action or branch choice is
// never stale-safe to drop.
var planningVocabulary = map[string]bool{
	"title": true, "outcome": true, "rules": true, "articles": true,
	"proof": true, "strategy": true, "objective": true, "approach": true,
	"task": true, "current": true, "target": true, "acceptance": true,
	"tests": true, "acceptance_command": true, "check_command": true,
	"execution": true, "validation": true, "verify": true,
	"field": true, "value": true, "feedback": true,
	"prompt": true, "method": true, "goal": true,
}

func normalizeFields(d Definition, input map[string]any) (map[string]any, string) {
	out := make(map[string]any, len(input))
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		canonical, value := argumentName(d, key), input[key]
		if d.Name == "plan" && !d.hasParam(canonical) && planningVocabulary[canonical] {
			continue // Stale field from another planning question; required checks still govern.
		}
		if prior, present := out[canonical]; present {
			equal := reflect.DeepEqual(prior, value)
			for _, p := range d.Params {
				if p.Name == canonical {
					a, qa := nativeValue(d.Name, p, prior, d.Prompts)
					b, qb := nativeValue(d.Name, p, value, d.Prompts)
					equal = qa == "" && qb == "" && reflect.DeepEqual(a, b)
					break
				}
			}
			if !equal {
				return nil, d.Prompts.Format("repair.arguments.conflicting_alias", key, canonical, d.Name) + "\n" + d.Prompts.Format("repair.arguments.current_call", d.CallTemplate())
			}
			continue
		}
		out[canonical] = value
	}
	return out, ""
}

// CallTemplate supplies one current native argument example from this definition.
// Required enum choices come from the schema; placeholders are examples, not effects.
func (d Definition) CallTemplate() string {
	args := map[string]any{}
	for _, p := range d.Params {
		if !p.Required {
			continue
		}
		switch p.Type {
		case TypeString:
			args[p.Name] = "<" + p.Name + ">"
			if len(p.Enum) > 0 {
				args[p.Name] = p.Enum[0]
			}
		case TypeInt, TypeFloat:
			args[p.Name] = 1
		case TypeBool:
			args[p.Name] = true
		}
	}
	for name, value := range d.Example {
		args[name] = value
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(args)
	pc := prompts.Select(d.Prompts)
	return pc.Format("tool.call.template", d.Name, strings.TrimSuffix(raw.String(), "\n"))
}
