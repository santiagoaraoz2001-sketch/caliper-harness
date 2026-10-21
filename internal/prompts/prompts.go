// Package prompts owns the editable, immutable model-instruction catalog.
package prompts

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"
	"text/template/parse"

	"gopkg.in/yaml.v3"
)

//go:embed prompts.yaml
var shipped string

type document struct {
	Texts map[string]string `yaml:"texts"`
}

// Catalog has no mutation API. One snapshot is shared by a run and its roles.
type Catalog struct {
	Source    string
	texts     map[string]string
	templates map[string]*template.Template
	fields    map[string][]string
}

// Select keeps optional programmatic callers on the same shipped defaults.
func Select(catalogs ...*Catalog) *Catalog {
	if len(catalogs) > 0 && catalogs[0] != nil {
		return catalogs[0]
	}
	return Builtin()
}

var once sync.Once
var builtin *Catalog

func Builtin() *Catalog {
	once.Do(func() {
		var err error
		builtin, err = decode([]byte(shipped), "embedded prompts.yaml", nil)
		if err != nil {
			panic("invalid shipped prompt catalog: " + err.Error())
		}
	})
	return builtin
}

// Load rejects an invalid explicit file; there is no per-key fallback.
func Load(path string) (*Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("prompt catalog %s: %w", path, err)
	}
	return decode(raw, path, Builtin())
}

// Default first uses the shipped editable asset, then the source working folder.
// Only an absent default asset uses the disclosed embedded copy.
func Default() (*Catalog, error) {
	var paths []string
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "prompts.yaml"))
	}
	paths = append(paths, "prompts.yaml")
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return Load(path)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("prompt catalog %s: %w", path, err)
		}
	}
	return Builtin(), nil
}

// Legacy resolves an explicitly selected template directory to the SAME YAML.
func Legacy(dir string) (*Catalog, error) {
	if dir == "" || dir == "templates" {
		return Default()
	}
	path := dir
	if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
		path = filepath.Join(dir, "prompts.yaml")
	}
	c, err := Load(path)
	if err != nil {
		return nil, fmt.Errorf("templates.path/--templates-dir now selects prompts.yaml; standalone .tmpl files are no longer prompt sources: %w", err)
	}
	return c, nil
}

func decode(raw []byte, source string, expected *Catalog) (*Catalog, error) {
	// Decode the node first: map[string]string otherwise coerces bool/numeric
	// scalars, unlike the shared Python consumer. Prompt values must be strings.
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return nil, fmt.Errorf("prompt catalog %s: %w", source, err)
	}
	if len(node.Content) > 0 && node.Content[0].Kind == yaml.MappingNode {
		root := node.Content[0]
		for i := 0; i < len(root.Content); i += 2 {
			if root.Content[i].Value != "texts" || root.Content[i+1].Kind != yaml.MappingNode {
				continue // structural and unknown-key errors remain with KnownFields below
			}
			texts := root.Content[i+1]
			for j := 0; j < len(texts.Content); j += 2 {
				key, value := texts.Content[j], texts.Content[j+1]
				if key.Tag != "!!str" || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
					return nil, fmt.Errorf("prompt catalog %s key %s must have a string key and value", source, key.Value)
				}
				// PyYAML's YAML 1.1 implicit booleans must not diverge from yaml.v3.
				if value.Style == 0 {
					switch strings.ToLower(value.Value) {
					case "yes", "no", "on", "off":
						return nil, fmt.Errorf("prompt catalog %s key %s needs a quoted string for YAML-ambiguous %q", source, key.Value, value.Value)
					}
				}
			}
		}
	}
	var d document
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("prompt catalog %s: %w", source, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("prompt catalog %s must contain exactly one YAML document", source)
	}
	c := &Catalog{Source: source, texts: d.Texts, templates: map[string]*template.Template{}, fields: map[string][]string{}}
	if expected != nil {
		for key := range expected.texts {
			if _, ok := d.Texts[key]; !ok {
				// These later additions preserve older customized catalogs.
				// Their owners explicitly disclose a missing guide or fallback.
				if key == "setup.assisted" || key == "continuation.project_style" || key == "planning.answer.coverage" {
					continue
				}
				return nil, fmt.Errorf("prompt catalog %s missing key %s", source, key)
			}
		}
		for key := range d.Texts {
			if _, ok := expected.texts[key]; !ok {
				return nil, fmt.Errorf("prompt catalog %s unknown key %s", source, key)
			}
		}
	}
	if len(d.Texts) == 0 {
		return nil, fmt.Errorf("prompt catalog %s has no texts", source)
	}
	for key, text := range d.Texts {
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("prompt catalog %s empty key %s", source, key)
		}
		t, err := template.New(key).Option("missingkey=error").Parse(text)
		if err != nil {
			return nil, fmt.Errorf("prompt catalog %s key %s: %w", source, key, err)
		}
		fields, err := interpolationFields(t)
		if err != nil {
			return nil, fmt.Errorf("prompt catalog %s key %s: %w", source, key, err)
		}
		allowed := fields
		// Compact already supplies Range and Words as legacy fields. They are
		// available to an edited template even when the shipped text omits them.
		if expected == nil && key == "templates.compaction" {
			allowed = append(allowed, "Range", "Words")
		}
		if expected != nil {
			allowed = expected.fields[key]
		}
		for _, field := range fields {
			found := false
			for _, name := range allowed {
				found = found || name == field
			}
			if !found {
				return nil, fmt.Errorf("prompt catalog %s key %s unknown field .%s", source, key, field)
			}
		}
		data := map[string]any{}
		for _, field := range allowed {
			data[field] = "value"
			switch field {
			case "Attempt", "Children", "Words", "BashMaxBytes", "BashMaxLines", "SearchMaxBytes", "SearchMaxMatches", "MaxBytes", "MaxEntries", "MaxLines", "Offset", "Limit", "EndLine":
				data[field] = 1
			case "Qualified":
				data[field] = true
			}
		}
		// printf reports wrong operand types as text (%!d(...)), not errors.
		// The admitted subset has one value, so check its owning type directly.
		for _, node := range t.Tree.Root.Nodes {
			if action, ok := node.(*parse.ActionNode); ok {
				args := action.Pipe.Cmds[0].Args
				if len(args) == 3 {
					format := args[1].(*parse.StringNode).Text
					field := args[2].(*parse.FieldNode).Ident[0]
					if strings.HasPrefix(fmt.Sprintf(format, data[field]), "%!") {
						return nil, fmt.Errorf("prompt catalog %s key %s format %q is incompatible with owning field .%s", source, key, format, field)
					}
				}
			}
		}
		if err := t.Execute(io.Discard, data); err != nil {
			return nil, fmt.Errorf("prompt catalog %s key %s: %w", source, key, err)
		}
		c.templates[key], c.fields[key] = t, allowed
	}
	return c, nil
}

// The catalog supports direct named fields and one-value printf formatting.
// It deliberately admits no indirect lookups or control flow that could hide
// an invalid owning field from preflight or fail only after model effects.
func interpolationFields(t *template.Template) ([]string, error) {
	if len(t.Templates()) != 1 {
		return nil, fmt.Errorf("template definitions are unsupported; use direct .Field interpolation")
	}
	var fields []string
	for _, node := range t.Tree.Root.Nodes {
		if _, text := node.(*parse.TextNode); text {
			continue
		}
		action, ok := node.(*parse.ActionNode)
		if !ok || len(action.Pipe.Decl) != 0 || len(action.Pipe.Cmds) != 1 {
			return nil, fmt.Errorf("unsupported template expression; use direct .Field or printf with one named field")
		}
		args := action.Pipe.Cmds[0].Args
		if len(args) == 3 {
			fn, fnOK := args[0].(*parse.IdentifierNode)
			format, formatOK := args[1].(*parse.StringNode)
			if !fnOK || fn.Ident != "printf" || !formatOK || (format.Text != "%q" && format.Text != "%d" && format.Text != "%t" && format.Text != "%s") {
				return nil, fmt.Errorf("unsupported template format; use printf %%q, %%d, %%t or %%s with one named field")
			}
			args = args[2:]
		}
		if len(args) != 1 {
			return nil, fmt.Errorf("unsupported template expression; use direct .Field interpolation")
		}
		field, ok := args[0].(*parse.FieldNode)
		if !ok || len(field.Ident) != 1 {
			return nil, fmt.Errorf("unsupported template lookup; use direct .Field interpolation")
		}
		name, seen := field.Ident[0], false
		for _, old := range fields {
			seen = seen || old == name
		}
		if !seen {
			fields = append(fields, name)
		}
	}
	return fields, nil
}

func (c *Catalog) Render(key string, data any) (string, error) {
	if c == nil {
		c = Builtin()
	}
	t := c.templates[key]
	if t == nil {
		return "", fmt.Errorf("unknown prompt key %s", key)
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("prompt key %s: %w", key, err)
	}
	return b.String(), nil
}

// Text/Format use fields validated at load and fixed caller arguments. Errors
// here are programming errors, rather than silent fallback instructions.
func (c *Catalog) Text(key string) string { return c.Format(key) }
func (c *Catalog) Format(key string, values ...any) string {
	if c == nil {
		c = Builtin()
	}
	names := c.fields[key]
	if len(names) != len(values) {
		panic(fmt.Sprintf("prompt %s needs %d owning values, got %d", key, len(names), len(values)))
	}
	data := map[string]any{}
	for i, name := range names {
		data[name] = values[i]
	}
	text, err := c.Render(key, data)
	if err != nil {
		panic(err)
	}
	return text
}

func (c *Catalog) Has(key string) bool {
	if c == nil {
		c = Builtin()
	}
	_, ok := c.texts[key]
	return ok
}

// Keys selects named source texts without rendering or injecting their bodies.
func (c *Catalog) Keys(prefix string) []string {
	if c == nil {
		c = Builtin()
	}
	keys := []string{}
	for key := range c.texts {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func (c *Catalog) YAML() string {
	if c == nil {
		c = Builtin()
	}
	bytes, err := yaml.Marshal(document{Texts: c.texts})
	if err != nil {
		panic(err)
	}
	return string(bytes)
}

// CompatibleWith validates an edited catalog against the next release's keys
// and template inputs before an updater activates that release. No prompt text
// is merged or replaced automatically.
func CompatibleWith(path, nextPath string) error {
	next, err := os.ReadFile(nextPath)
	if err != nil {
		return err
	}
	expected, err := decode(next, nextPath, nil)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = decode(raw, path, expected)
	return err
}
