package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agnivade/levenshtein"
	"github.com/vektah/gqlparser/v2/ast"
	api "specific-harness/internal/graph"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

func helpSchemaIndex(active *tool.Registry, pc *prompts.Catalog) string {
	text := pc.Text("help.schema.index")
	for _, name := range active.Names() {
		d, _ := active.Lookup(name)
		text += pc.Format("help.schema.index_entry", name, d.Desc)
	}
	return text
}

func helpSchema(topic string, active, registered *tool.Registry, pc *prompts.Catalog) (string, error) {
	if topic == "schema" {
		return helpSchemaIndex(active, pc), nil
	}
	name := strings.TrimPrefix(topic, "schema.")
	owner := name
	if name == "read.definition" || name == "read.outline" {
		owner = "read"
	} else if strings.HasPrefix(name, "graphql.") {
		owner = "graphql"
	}
	d, available := active.ResolveName(owner)
	if available {
		owner = d.Name
	}
	known := available || pc.Has("tools."+owner+".description")
	if registered != nil {
		_, found := registered.Lookup(owner)
		known = known || found
	}
	if !known {
		return "", schemaUnknown(topic, active.Names(), "schema.", pc)
	}
	if !available {
		return pc.Text("help.unavailable"), nil
	}
	if owner == "graphql" {
		return helpGraphQLSchema(strings.TrimPrefix(name, "graphql"), d, pc)
	}
	var slots strings.Builder
	for _, p := range d.Params {
		presence := pc.Text("help.schema.optional")
		if p.Required {
			presence = pc.Text("help.schema.required")
		}
		fmt.Fprintf(&slots, "- %s (%s, %s): %s", p.Name, p.Type, presence, p.Desc)
		if p.Default != "" {
			slots.WriteString(pc.Format("help.schema.default", p.Default))
		}
		if len(p.Enum) > 0 {
			slots.WriteString(pc.Format("help.schema.enum", strings.Join(p.Enum, ", ")))
		}
		slots.WriteByte('\n')
	}
	if len(d.Params) == 0 {
		slots.WriteString(pc.Text("tool.call.no_arguments"))
	}
	// Documentation aliases project a useful optional argument through the
	// same owning formatter; they do not change the executable definition.
	if name == "read.definition" {
		d.Example = map[string]any{"definition": "<definition>"}
	} else if name == "read.outline" {
		d.Example = map[string]any{"outline": true}
	}
	return pc.Format("help.schema.tool", d.Name, strings.TrimSpace(d.Desc+"\n"+d.Purpose), d.CallTemplate(), slots.String()), nil
}

func nativeTemplate(name string, args map[string]any) string {
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}{name, args})
	return strings.TrimSuffix(data.String(), "\n")
}

func schemaUnknown(topic string, names []string, prefix string, pc *prompts.Catalog) error {
	best := ""
	for _, name := range names {
		candidate := prefix + name
		if best == "" || levenshtein.ComputeDistance(topic, candidate) < levenshtein.ComputeDistance(topic, best) {
			best = candidate
		}
	}
	return fmt.Errorf("%s", pc.Format("guidance.help.schema_unknown", topic, best))
}

func helpGraphQLSchema(suffix string, d tool.Definition, pc *prompts.Catalog) (string, error) {
	names := api.ModelFields()
	if suffix == "" {
		text := pc.Text("help.schema.graphql.index")
		for _, name := range names {
			_, f := api.ModelOperation(name)
			details := ""
			if strings.TrimSpace(f.Description) != "" {
				details = " — " + strings.TrimSpace(f.Description)
			}
			text += pc.Format("help.schema.graphql.index_entry", name, details)
		}
		return text, nil
	}
	name := strings.TrimPrefix(suffix, ".")
	schema := api.ModelSchema()
	root, field := api.ModelOperation(name)
	if field == nil {
		if typ := schema.Types[name]; typ != nil && !strings.HasPrefix(name, "__") && name != "Query" && name != "Mutation" {
			var fields []string
			for _, f := range typ.Fields {
				fields = append(fields, "  "+graphSignature(f))
			}
			return pc.Format("help.schema.graphql.type", name, strings.Join(fields, "\n")), nil
		}
		return "", schemaUnknown("schema.graphql."+name, names, "schema.graphql.", pc)
	}
	operation := "query"
	if root.Name == "Mutation" {
		operation = "mutation"
	}
	var args []string
	for _, a := range field.Arguments {
		if a.Type.NonNull && a.DefaultValue == nil {
			args = append(args, a.Name+":"+graphInput(schema, a.Name, a.Type))
		}
	}
	call := field.Name
	if len(args) > 0 {
		call += "(" + strings.Join(args, ",") + ")"
	}
	if output := schema.Types[field.Type.Name()]; output.IsCompositeType() {
		call += " { " + graphSelection(schema, output) + " }"
	}
	text := pc.Format("help.schema.graphql.operation", graphSignature(field), nativeTemplate(d.Name, map[string]any{"query": operation + " { " + call + " }"}))
	if pc.Has("help.schema.graphql." + name) {
		text += "\n" + pc.Text("help.schema.graphql."+name)
	}
	return text, nil
}

func graphSelection(schema *ast.Schema, output *ast.Definition) string {
	if output.Fields.ForName("id") != nil {
		return "id"
	}
	for _, f := range output.Fields {
		if len(f.Arguments) == 0 && schema.Types[f.Type.Name()].IsLeafType() {
			return f.Name
		}
	}
	f := output.Fields[0]
	return f.Name + " { " + graphSelection(schema, schema.Types[f.Type.Name()]) + " }"
}

func graphSignature(field *ast.FieldDefinition) string {
	var args []string
	for _, a := range field.Arguments {
		value := a.Name + ": " + a.Type.String()
		if a.DefaultValue != nil {
			value += " = " + a.DefaultValue.String()
		}
		args = append(args, value)
	}
	name := field.Name
	if len(args) > 0 {
		name += "(" + strings.Join(args, ", ") + ")"
	}
	return name + ": " + field.Type.String()
}

func graphInput(schema *ast.Schema, name string, typ *ast.Type) string {
	if typ.Elem != nil {
		return "[" + graphInput(schema, name, typ.Elem) + "]"
	}
	def := schema.Types[typ.Name()]
	if def.Kind == ast.InputObject {
		var fields []string
		for _, f := range def.Fields {
			if f.Type.NonNull && f.DefaultValue == nil {
				fields = append(fields, f.Name+":"+graphInput(schema, f.Name, f.Type))
			}
		}
		return "{" + strings.Join(fields, ",") + "}"
	}
	if def.Kind == ast.Enum {
		return def.EnumValues[0].Name
	}
	switch typ.Name() {
	case "Int", "Float":
		return "1"
	case "Boolean":
		return "false"
	}
	return fmt.Sprintf("%q", "<"+name+">")
}
