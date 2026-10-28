package tools

import (
	"fmt"
	"strings"

	"github.com/agnivade/levenshtein"
	"github.com/vektah/gqlparser/v2/ast"
	api "specific-harness/internal/graph"
	"specific-harness/internal/memory"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

func helpSchemaIndex(active *tool.Registry, pc *prompts.Catalog) string {
	text := pc.Text("help.schema.index")
	if graph, ok := active.Lookup("graphql"); ok && graph.GraphQLFields != nil {
		text = pc.Text("help.schema.role_index")
	}
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
	if d.GraphQLFields != nil {
		names = d.GraphQLFields
	}
	if suffix == "" {
		text := pc.Text("help.schema.graphql.index")
		if d.GraphQLFields != nil {
			text = pc.Text("help.schema.graphql.role_index")
		}
		for _, name := range names {
			root, f := api.ModelOperation(name)
			required := *f
			required.Arguments = nil
			for _, arg := range f.Arguments {
				if arg.Type.NonNull && arg.DefaultValue == nil {
					required.Arguments = append(required.Arguments, arg)
				}
			}
			details := " — " + strings.ToLower(root.Name) + " " + graphSignature(&required)
			if strings.TrimSpace(f.Description) != "" {
				details += " — " + strings.TrimSpace(f.Description)
			}
			text += pc.Format("help.schema.graphql.index_entry", name, details)
		}
		return text, nil
	}
	name := strings.TrimPrefix(suffix, ".")
	schema := api.ModelSchema()
	root, field := api.ModelOperation(name)
	if d.GraphQLFields != nil {
		allowed := false
		for _, candidate := range names {
			allowed = allowed || candidate == name
		}
		if field != nil && !allowed {
			return pc.Text("help.unavailable"), nil
		}
		if field == nil && !graphTypeReachable(schema, names, name) {
			return pc.Text("help.unavailable"), nil
		}

	}
	if field == nil {
		if typ := schema.Types[name]; typ != nil && !strings.HasPrefix(name, "__") && name != "Query" && name != "Mutation" {
			var fields []string
			for _, f := range typ.Fields {
				text := "  " + graphSignature(f)
				if name == "KnowledgeReferenceInput" && f.Name == "relation" {
					text += " (allowed: " + strings.Join(memory.KnowledgeRelations(), ", ") + ")"
				}
				fields = append(fields, text)
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
			value := graphInput(schema, a.Name, a.Type)
			if a.Name == "offset" {
				value = "0"
			}
			args = append(args, a.Name+":"+value)
		}
	}
	call := field.Name
	if len(args) > 0 {
		call += "(" + strings.Join(args, ",") + ")"
	}
	if output := schema.Types[field.Type.Name()]; output.IsCompositeType() {
		selection := graphSelection(schema, output)
		if output.Name == "EvidencePage" {
			selection = "historical nextOffset unsettledOffset groups { reference offset callIds records { kind source } }"
		}
		call += " { " + selection + " }"
	}
	if name == "knowledge" {
		call = `knowledge(text:"<keyword>",limit:3) { key sections { id title } }`
	}
	text := pc.Format("help.schema.graphql.operation", graphSignature(field), (tool.Definition{Name: d.Name, Prompts: pc, Example: tool.Args{"query": operation + " { " + call + " }"}}).CallTemplate())
	if typ := schema.Types[field.Type.Name()]; typ != nil && typ.IsCompositeType() {
		text += "\nReturned fields: help(topic=\"schema.graphql." + typ.Name + "\")."
	}
	if pc.Has("help.schema.graphql." + name) {
		if name == "knowledge" {
			text += "\n" + pc.Text("help.graphql.readonly")
		} else {
			text += "\n" + pc.Text("help.schema.graphql."+name)
		}
	}
	if name == "appendKnowledge" || name == "putKnowledge" {
		// The advertised write-schema route must also expose the prerequisite
		// first-use read, from the same recipe as ordinary and worker help.
		text += "\n" + pc.Text("help.graphql.readonly")
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

// Reachable type help mirrors the same allowed roots, including nested fields
// and inputs. Host-only schema types never become a discovery side channel.
func graphTypeReachable(schema *ast.Schema, roots []string, target string) bool {
	seen := map[string]bool{}
	var visit func(string) bool
	visit = func(name string) bool {
		if seen[name] || strings.HasPrefix(name, "__") {
			return false
		}
		seen[name] = true
		if name == target {
			return true
		}
		t := schema.Types[name]
		if t == nil {
			return false
		}
		for _, f := range t.Fields {
			if visit(f.Type.Name()) {
				return true
			}
			for _, a := range f.Arguments {
				if visit(a.Type.Name()) {
					return true
				}
			}
		}
		return false
	}
	for _, name := range roots {
		_, f := api.ModelOperation(name)
		if f == nil {
			continue
		}
		if visit(f.Type.Name()) {
			return true
		}
		for _, a := range f.Arguments {
			if visit(a.Type.Name()) {
				return true
			}
		}
	}
	return false
}
