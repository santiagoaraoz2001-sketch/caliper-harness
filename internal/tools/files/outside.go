package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/search"
)

func (w *Workspace) displayPath(p string) string {
	if w.outside {
		return filepath.Join(w.Path, p)
	}
	return p
}

// OutsideDefinitions are explicit read-only discovery. Their temporary roots never
// share the workspace's publication map or a conversation's read coverage.
func (w *Workspace) OutsideDefinitions(cfg config.SearchConfig) []tool.Definition {
	pc := prompts.Select(w.Prompts)
	path := tool.Param{Name: "path", Type: tool.TypeString, Required: true, Desc: pc.Text("tools.outside.parameters.path")}
	read := tool.Definition{Prompts: pc, Name: "read_outside", Desc: pc.Text("tools.read_outside.description"), Purpose: pc.Text("tools.read_outside.purpose"), Params: []tool.Param{
		path,
		{Name: "offset", Type: tool.TypeInt, Default: "1", Desc: pc.Text("tools.read.parameters.offset")},
		{Name: "limit", Type: tool.TypeInt, Default: fmt.Sprint(w.Caps.MaxLines), Desc: pc.Format("tools.read.parameters.limit", w.Caps.MaxLines)},
	}, Execute: func(ctx context.Context, a tool.Args) (tool.Result, error) {
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		external, args, err := w.outsideArgs(a)
		if err != nil {
			return tool.Result{}, err
		}
		return external.execute(ctx, "read", args, nil)
	}}
	read.Params = append(read.Params, w.navigationParams()...)
	read = w.normalizeOutsidePath(read)
	find := tool.Definition{Prompts: pc, Name: "find_outside", Desc: pc.Text("tools.find_outside.description"), Purpose: pc.Text("tools.find_outside.purpose"), Params: []tool.Param{
		path,
		{Name: "mode", Type: tool.TypeString, Required: true, Enum: []string{"file", "dir"}, Desc: pc.Text("tools.find_outside.parameters.mode")},
		{Name: "pattern", Type: tool.TypeString, Required: true, Desc: pc.Text("tools.find_outside.parameters.pattern")},
		{Name: "offset", Type: tool.TypeInt, Desc: pc.Text("tools.find_outside.parameters.offset")},
		{Name: "limit", Type: tool.TypeInt, Desc: pc.Text("tools.find_outside.parameters.limit")},
	}, Execute: func(ctx context.Context, a tool.Args) (tool.Result, error) {
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		external, args, err := w.outsideArgs(a)
		if err != nil {
			return tool.Result{}, err
		}
		target := external.displayPath(args["path"].(string))
		info, err := os.Stat(target)
		if err != nil {
			return tool.Result{}, err
		}
		if !info.IsDir() {
			return tool.Result{}, fmt.Errorf("find_outside path must be a directory")
		}
		switch a["mode"] {
		case "file":
			if _, ok := a["limit"]; ok {
				return tool.Result{}, fmt.Errorf("limit applies only to mode=dir; file mode uses tools.search caps")
			}
			args["path"] = target
			// Scope the existing finder to the admitted outside root, without following child symlinks.
			return search.FindFilesOutside(ctx, cfg, target, args, pc)
		case "dir":
			if _, ok := args["offset"]; !ok {
				args["offset"] = int64(1)
			}
			if _, ok := args["limit"]; !ok {
				args["limit"] = int64(w.Caps.MaxLines)
			}
			return external.FindDirDefinition().Execute(ctx, args)
		default:
			return tool.Result{}, fmt.Errorf("mode must be file or dir")
		}
	}}
	return []tool.Definition{read, w.normalizeOutsidePath(find)}
}

func (w *Workspace) normalizeOutsidePath(def tool.Definition) tool.Definition {
	def.NormalizeArgs = func(ctx context.Context, args tool.Args) (tool.Args, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		input, _ := args["path"].(string)
		path, err := search.DiscoveryPath(w.Path, search.ExistingPath(w.Path, input), true)
		if err != nil {
			return nil, err
		}
		normalized := make(tool.Args, len(args))
		for key, value := range args {
			normalized[key] = value
		}
		normalized["path"] = path
		return normalized, nil
	}
	return def
}

func (w *Workspace) outsideArgs(a tool.Args) (*Workspace, tool.Args, error) {
	input, _ := a["path"].(string)
	target, err := search.DiscoveryPath(w.Path, input, true)
	if err != nil {
		return nil, nil, err
	}
	external := &Workspace{Path: filepath.Dir(target), Caps: w.Caps, Prompts: w.Prompts, outside: true}
	args := make(tool.Args, len(a))
	for key, value := range a {
		args[key] = value
	}
	args["path"] = filepath.Base(target)
	// filepath.Base("/") is absolute; the root directory itself is a valid page.
	if target == filepath.Dir(target) {
		args["path"] = "."
	}
	return external, args, nil
}
