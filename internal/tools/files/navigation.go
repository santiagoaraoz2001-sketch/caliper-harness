package files

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

func (w *Workspace) navigationParams() []tool.Param {
	pc := prompts.Select(w.Prompts)
	return []tool.Param{
		{Name: "definition", Type: tool.TypeString, Desc: pc.Text("tools.read.parameters.definition")},
		{Name: "outline", Type: tool.TypeBool, Default: "false", Desc: pc.Text("tools.read.parameters.outline")},
	}
}

func navigationRequested(a tool.Args) bool {
	_, definition := a["definition"]
	outline, _ := a["outline"].(bool)
	return definition || outline
}

type declaration struct {
	name, qualified, kind string
	start, end            int64
}

// Syntax locations only: no package loading, name binding or dependency traversal.
func goDeclarations(ctx context.Context, path string, source []byte) ([]declaration, error) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("Go syntax parse failed; use ordinary read/grep: %w", err)
	}
	var declarations []declaration
	for _, node := range file.Decls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		start, end := node.Pos(), node.End()
		add := func(name, qualified, kind string) {
			declarations = append(declarations, declaration{name, qualified, kind, int64(set.PositionFor(start, false).Line), int64(set.PositionFor(end-1, false).Line)})
		}
		switch n := node.(type) {
		case *ast.FuncDecl:
			if n.Doc != nil {
				start = n.Doc.Pos()
			}
			name, kind := n.Name.Name, "function"
			if n.Recv != nil && len(n.Recv.List) > 0 {
				name, kind = receiverName(n.Recv.List[0].Type)+"."+name, "method"
			}
			add(n.Name.Name, name, kind)
		case *ast.GenDecl:
			if n.Doc != nil {
				start = n.Doc.Pos()
			}
			// Retain the declaration group: const iota and inherited values/types
			// can depend on earlier specs. Each named member has the same safe span.
			for _, spec := range n.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.Name != "_" {
						add(s.Name.Name, s.Name.Name, "type")
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.Name != "_" {
							add(name.Name, name.Name, n.Tok.String())
						}
					}
				}
			}
		}
	}
	return declarations, ctx.Err()
}

func receiverName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.StarExpr:
		return receiverName(n.X)
	case *ast.IndexExpr:
		return receiverName(n.X)
	case *ast.IndexListExpr:
		return receiverName(n.X)
	case *ast.ParenExpr:
		return receiverName(n.X)
	}
	return "" // Parse-valid Go receiver syntax is covered above; no binding inferred.
}

// Parse and hash the same complete snapshot; the cap bounds only returned pages.
func (w *Workspace) navigationSnapshot(ctx context.Context, root *os.Root, path string) ([]byte, error) {
	source, _, err := w.bytes(ctx, root, path)
	return source, err
}

func (w *Workspace) readNavigation(ctx context.Context, root *os.Root, path string, args tool.Args, coverage map[string]readCoverage) (tool.Result, error) {
	selector, definition := args["definition"].(string)
	outline, _ := args["outline"].(bool)
	offset, _ := args["offset"].(int64)
	limit, _ := args["limit"].(int64)
	if definition && (outline || strings.TrimSpace(selector) == "") {
		return tool.Result{}, fmt.Errorf("use one nonempty definition selector or outline=true, not both")
	}
	if offset < 1 || definition && offset != 1 {
		return tool.Result{}, fmt.Errorf("offset=%d must be >=1; definition requires offset=1; continue with ordinary read at the returned absolute next_offset", offset)
	}
	if limit < 1 || limit > int64(w.Caps.MaxLines) {
		return tool.Result{}, fmt.Errorf("limit=%d must be within 1..%d (tools.bash.max_lines)", limit, w.Caps.MaxLines)
	}
	if strings.ToLower(filepath.Ext(path)) != ".go" {
		return tool.Result{}, fmt.Errorf("definition/outline supports Go syntax declarations (.go) only, not semantic reference resolution; use ordinary read/grep for %s", path)
	}
	source, err := w.navigationSnapshot(ctx, root, path)
	if err != nil {
		return tool.Result{}, err
	}
	hash := digest(source)
	if c, ok := coverage[path]; ok && (c.hash != hash || c.version != w.publications[path]) {
		delete(coverage, path)
	}
	declarations, err := goDeclarations(ctx, path, source)
	if err != nil {
		return tool.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	header := fmt.Sprintf("%s sha256=%s\n[Go syntax declarations; not semantic reference resolution]\n", w.displayPath(path), hash)
	if outline {
		return w.outlinePage(ctx, header, declarations, offset, limit)
	}
	var matches []declaration
	for _, d := range declarations {
		if d.name == selector || d.qualified == selector {
			matches = append(matches, d)
		}
	}
	if len(matches) == 0 {
		return tool.Result{}, fmt.Errorf("no Go declaration named %q in %s; use outline=true or grep for textual occurrences (imports/local binding are not resolved)", selector, path)
	}
	if len(matches) > 1 {
		return tool.Result{}, fmt.Errorf("definition %q is ambiguous: %d declarations, including %s:%d (%s) and %s:%d (%s); use a receiver-qualified name or ordinary read at an outline location", selector, len(matches), path, matches[0].start, matches[0].qualified, path, matches[1].start, matches[1].qualified)
	}
	d := matches[0]
	lines := sourceLines(string(source))
	header += fmt.Sprintf("[definition=%q; declaration_range=%d-%d]\n", d.qualified, d.start, d.end)
	footer := func(shown int64) string {
		last, span, cursor := d.start+shown-1, "none", ""
		if shown > 0 {
			span = fmt.Sprintf("%d-%d", d.start, last)
			if last < d.end {
				cursor = fmt.Sprintf("; next_offset=%d; remaining_definition_lines=%d (ordinary read)", last+1, d.end-last)
			}
		}
		return fmt.Sprintf("\n[shown_range=%s; selection_complete=%t; EOF=%t; truncated=%t%s; metadata not source lines]", span, last == d.end, shown > 0 && last == int64(len(lines)), last < d.end, cursor)
	}
	text, shown := header, int64(0)
	for line := d.start; line <= d.end && shown < limit; line++ {
		if len(text)+len(lines[line-1])+len(footer(shown+1)) > w.Caps.MaxBytes {
			break
		}
		text += lines[line-1]
		shown++
	}
	if len(text)+len(footer(shown)) > w.Caps.MaxBytes {
		return tool.Result{}, fmt.Errorf("definition metadata exceeds tools.bash.max_bytes=%d", w.Caps.MaxBytes)
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if shown > 0 {
		w.recordRead(coverage, path, hash, d.start, d.start+shown-1)
	}
	return tool.Result{Text: text + footer(shown), Truncated: d.start+shown-1 < d.end, NoMatches: shown == 0}, nil
}

func (w *Workspace) outlinePage(ctx context.Context, header string, declarations []declaration, offset, limit int64) (tool.Result, error) {
	header += "[outline navigation metadata; no source coverage; offsets count declarations]\n"
	footer := func(shown int64) string {
		more := offset-1+shown < int64(len(declarations))
		cursor := ""
		if more && shown > 0 {
			cursor = fmt.Sprintf("; next_offset=%d", offset+shown)
		}
		return fmt.Sprintf("\n[%d declarations shown; more=%t; truncated=%t%s]", shown, more, more, cursor)
	}
	text, shown := header, int64(0)
	for i := offset - 1; i < int64(len(declarations)) && shown < limit; i++ {
		d := declarations[i]
		line := fmt.Sprintf("%s %s lines=%d-%d definition=%q\n", d.kind, d.qualified, d.start, d.end, d.qualified)
		if len(text)+len(line)+len(footer(shown+1)) > w.Caps.MaxBytes {
			break
		}
		text += line
		shown++
	}
	if len(text)+len(footer(shown)) > w.Caps.MaxBytes {
		return tool.Result{}, fmt.Errorf("outline metadata exceeds tools.bash.max_bytes=%d", w.Caps.MaxBytes)
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Text: text + footer(shown), Truncated: offset-1+shown < int64(len(declarations)), NoMatches: shown == 0}, nil
}
