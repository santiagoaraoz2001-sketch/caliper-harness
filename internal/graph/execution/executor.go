package execution

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/introspection"
	gqlparser "github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"specific-harness/schema"
)

// gqlgen still owns validation, variables, collected fields, directives, error
// paths and middleware. This adapter replaces its per-field generated wrappers.
// Resolver interfaces retain compile-time checks for the handwritten root API.
type Config struct {
	Schema    *ast.Schema
	Resolvers ResolverRoot
}

type executableSchema struct{ Config }

var parsedSchema = gqlparser.MustLoadSchema(&ast.Source{Name: "schema/v1.graphqls", Input: schema.SDL})

func NewExecutableSchema(cfg Config) graphql.ExecutableSchema {
	return &executableSchema{cfg}
}

func (e *executableSchema) Schema() *ast.Schema {
	if e.Config.Schema != nil {
		return e.Config.Schema
	}
	return parsedSchema
}

func (e *executableSchema) Complexity(context.Context, string, string, int, map[string]any) (int, bool) {
	return 0, false
}

func (e *executableSchema) Exec(ctx context.Context) graphql.ResponseHandler {
	op := graphql.GetOperationContext(ctx)
	var root any
	var definition *ast.Definition
	switch op.Operation.Operation {
	case ast.Query:
		root, definition = e.Resolvers.Query(), e.Schema().Query
	case ast.Mutation:
		root, definition = e.Resolvers.Mutation(), e.Schema().Mutation
	default:
		return graphql.OneShot(graphql.ErrorResponse(ctx, "unsupported GraphQL operation"))
	}
	first := true
	return func(ctx context.Context) *graphql.Response {
		if !first {
			return nil
		}
		first = false
		data := e.object(ctx, definition.Name, op.Operation.SelectionSet, reflect.ValueOf(root), true)
		var buf bytes.Buffer
		data.MarshalGQL(&buf)
		return &graphql.Response{Data: buf.Bytes()}
	}
}

func (e *executableSchema) object(ctx context.Context, kind string, selection ast.SelectionSet, value reflect.Value, root bool) graphql.Marshaler {
	op := graphql.GetOperationContext(ctx)
	fields := graphql.CollectFields(op, selection, []string{kind})
	out := graphql.NewFieldSet(fields)
	invalid := false
	for i, field := range fields {
		if field.Name == "__typename" {
			out.Values[i] = graphql.MarshalString(kind)
			continue
		}
		resolve := func(ctx context.Context) graphql.Marshaler {
			return e.field(ctx, kind, field, value)
		}
		if root {
			fieldCtx := graphql.WithRootFieldContext(ctx, &graphql.RootFieldContext{Object: kind, Field: field})
			out.Values[i] = op.RootResolverMiddleware(fieldCtx, resolve)
		} else {
			out.Values[i] = resolve(ctx)
		}
		if out.Values[i] == graphql.RequiredNull || (field.Definition.Type.NonNull && out.Values[i] == graphql.Null) {
			invalid = true
		}
	}
	if invalid {
		return graphql.Null
	}
	return out
}

func (e *executableSchema) field(ctx context.Context, kind string, field graphql.CollectedField, value reflect.Value) graphql.Marshaler {
	op := graphql.GetOperationContext(ctx)
	return graphql.ResolveField[any](ctx, op, field,
		func(ctx context.Context, field graphql.CollectedField) (*graphql.FieldContext, error) {
			return e.fieldContext(ctx, kind, field), nil
		},
		func(ctx context.Context) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if field.Name == "__schema" {
				return introspection.WrapSchema(e.Schema()), nil
			}
			if field.Name == "__type" {
				name, _ := graphql.GetFieldContext(ctx).Args["name"].(string)
				return introspection.WrapTypeFromDef(e.Schema(), e.Schema().Types[name]), nil
			}
			return resolve(ctx, value, field, e.Schema())
		}, nil,
		func(ctx context.Context, selection ast.SelectionSet, result any) graphql.Marshaler {
			return e.marshal(ctx, field.Definition.Type, selection, reflect.ValueOf(result))
		}, true, field.Definition.Type.NonNull)
}

func (e *executableSchema) fieldContext(ctx context.Context, kind string, field graphql.CollectedField) *graphql.FieldContext {
	return &graphql.FieldContext{
		Object: kind, Field: field, Args: field.ArgumentMap(graphql.GetOperationContext(ctx).Variables),
		IsMethod:   kind == "Query" || kind == "Mutation" || strings.HasPrefix(kind, "__"),
		IsResolver: kind == "Query" || kind == "Mutation",
		Child: func(ctx context.Context, child graphql.CollectedField) (*graphql.FieldContext, error) {
			return e.fieldContext(ctx, field.Definition.Type.Name(), child), nil
		},
	}
}

func resolve(ctx context.Context, object reflect.Value, field graphql.CollectedField, schema *ast.Schema) (any, error) {
	if !object.IsValid() {
		return nil, nil
	}
	name := strings.ToUpper(field.Name[:1]) + field.Name[1:]
	method := object.MethodByName(name)
	if !method.IsValid() && object.Kind() != reflect.Pointer {
		copy := reflect.New(object.Type())
		copy.Elem().Set(object)
		method = copy.MethodByName(name)
	}
	if method.IsValid() {
		args := graphql.GetFieldContext(ctx).Args
		definitions := field.Definition.Arguments
		kind := graphql.GetFieldContext(ctx).Object
		if kind == "Query" || kind == "Mutation" {
			names, ok := rootArguments[kind+"."+field.Name]
			if !ok || len(names) != len(definitions) {
				return nil, fmt.Errorf("schema/resolver binding missing for %s.%s", kind, field.Name)
			}
			definitions = make(ast.ArgumentDefinitionList, len(names))
			for i, name := range names {
				definitions[i] = field.Definition.Arguments.ForName(name)
				if definitions[i] == nil {
					return nil, fmt.Errorf("schema/resolver argument missing: %s.%s", field.Name, name)
				}
			}
		}
		var values []reflect.Value
		if method.Type().NumIn() > 0 && method.Type().In(0) == reflect.TypeOf((*context.Context)(nil)).Elem() {
			values = append(values, reflect.ValueOf(ctx))
		}
		for _, arg := range definitions {
			index := len(values)
			if index >= method.Type().NumIn() {
				// The installed gqlgen introspection wrappers own their argument
				// support (for example InputFields takes no includeDeprecated).
				// This matches the generated executor for that library version.
				if strings.HasPrefix(graphql.GetFieldContext(ctx).Object, "__") {
					break
				}
				return nil, fmt.Errorf("schema/resolver argument mismatch for %s", field.Name)
			}
			v, err := input(args[arg.Name], arg.Type, method.Type().In(index), schema)
			if err != nil {
				return nil, fmt.Errorf("argument %s: %w", arg.Name, err)
			}
			values = append(values, v)
		}
		if len(values) != method.Type().NumIn() {
			return nil, fmt.Errorf("schema/resolver argument mismatch for %s", field.Name)
		}
		result := method.Call(values)
		if len(result) == 2 && !result[1].IsNil() {
			return nil, result[1].Interface().(error)
		}
		return result[0].Interface(), nil
	}
	for object.Kind() == reflect.Pointer || object.Kind() == reflect.Interface {
		if object.IsNil() {
			return nil, nil
		}
		object = object.Elem()
	}
	if object.Kind() == reflect.Struct {
		for i := range object.NumField() {
			f := object.Type().Field(i)
			if strings.Split(f.Tag.Get("json"), ",")[0] == field.Name || strings.EqualFold(f.Name, field.Name) {
				return object.Field(i).Interface(), nil
			}
		}
	}
	return nil, fmt.Errorf("schema/resolver field missing: %s", field.Name)
}

// Input conversion uses gqlgen's scalar coercion, including numeric GraphQL IDs.
// It preserves optional nil values and recursively converts typed input objects.
func input(value any, typ *ast.Type, target reflect.Type, schema *ast.Schema) (reflect.Value, error) {
	if value == nil {
		return reflect.Zero(target), nil
	}
	if target.Kind() == reflect.Pointer {
		v, err := input(value, typ, target.Elem(), schema)
		if err != nil {
			return reflect.Value{}, err
		}
		p := reflect.New(target.Elem())
		p.Elem().Set(v)
		return p, nil
	}
	if target.Kind() == reflect.Slice {
		items := graphql.CoerceList(value)
		out := reflect.MakeSlice(target, len(items), len(items))
		for i, item := range items {
			v, err := input(item, typ.Elem, target.Elem(), schema)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(v)
		}
		return out, nil
	}
	if target.Kind() == reflect.Struct {
		values, ok := value.(map[string]any)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected input object, got %T", value)
		}
		out := reflect.New(target).Elem()
		definition := schema.Types[typ.Name()]
		for i := range target.NumField() {
			name := strings.Split(target.Field(i).Tag.Get("json"), ",")[0]
			f := definition.Fields.ForName(name)
			if f == nil {
				return reflect.Value{}, fmt.Errorf("schema/input field missing: %s", name)
			}
			v, err := input(values[name], f.Type, target.Field(i).Type, schema)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Field(i).Set(v)
		}
		return out, nil
	}
	var converted any
	var err error
	switch typ.Name() {
	case "ID":
		converted, err = graphql.UnmarshalID(value)
	case "String":
		converted, err = graphql.UnmarshalString(value)
	case "Int":
		converted, err = graphql.UnmarshalInt(value)
	case "Boolean":
		converted, err = graphql.UnmarshalBoolean(value)
	case "Float":
		converted, err = graphql.UnmarshalFloat(value)
	default:
		return reflect.Value{}, fmt.Errorf("unsupported schema input %s", typ.Name())
	}
	if err != nil {
		return reflect.Value{}, err
	}
	return reflect.ValueOf(converted), nil
}

func (e *executableSchema) marshal(ctx context.Context, typ *ast.Type, selection ast.SelectionSet, value reflect.Value) graphql.Marshaler {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nullValue(ctx, typ)
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nullValue(ctx, typ)
	}
	if typ.Elem != nil {
		if value.IsNil() && !typ.NonNull {
			return graphql.Null
		}
		out := make(graphql.Array, value.Len())
		for i := range value.Len() {
			index := i
			itemCtx := graphql.WithFieldContext(ctx, &graphql.FieldContext{Index: &index})
			out[i] = e.marshal(itemCtx, typ.Elem, selection, value.Index(i))
			if out[i] == graphql.Null && typ.Elem.NonNull {
				return graphql.Null
			}
		}
		return out
	}
	switch typ.Name() {
	case "String", "ID":
		return graphql.MarshalString(value.String())
	case "Int":
		return graphql.MarshalInt(int(value.Int()))
	case "Boolean":
		return graphql.MarshalBoolean(value.Bool())
	case "Float":
		return graphql.MarshalFloat(value.Float())
	default:
		if e.Schema().Types[typ.Name()].Kind == ast.Enum {
			return graphql.MarshalString(value.String())
		}
		return e.object(ctx, typ.Name(), selection, value, false)
	}
}

func nullValue(ctx context.Context, typ *ast.Type) graphql.Marshaler {
	if typ.NonNull && !graphql.HasFieldError(ctx, graphql.GetFieldContext(ctx)) {
		graphql.AddErrorf(ctx, "the requested element is null which the schema does not allow")
	}
	return graphql.Null
}
