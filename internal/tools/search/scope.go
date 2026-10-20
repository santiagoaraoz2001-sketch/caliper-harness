package search

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"strings"
)

// ExistingPath tries exactly one path decoding only after literal absence. The
// caller still owns scope/type admission; URIs and source strings are not paths.
// ExistingPath resolves one unambiguous respelling of a path the model
// copied from another surface (URL escapes, shell-escaped spaces). The
// original spelling always wins when it exists; a respelling is used only
// when the original does not exist and the respelling does.
func ExistingPath(workspace, input string) (string, error) {
	if strings.Contains(input, "://") {
		return input, nil
	}
	abs := func(path string) string {
		if filepath.IsAbs(path) {
			return path
		}
		return filepath.Join(workspace, path)
	}
	if _, err := os.Lstat(abs(input)); !os.IsNotExist(err) {
		return input, nil
	}
	var candidates []string
	if strings.Contains(input, "%") {
		if decoded, err := url.PathUnescape(input); err == nil && decoded != input {
			candidates = append(candidates, decoded)
		}
	}
	if unescaped := strings.ReplaceAll(input, "\\ ", " "); unescaped != input {
		candidates = append(candidates, unescaped)
	}
	matched := ""
	for _, candidate := range candidates {
		if _, err := os.Lstat(abs(candidate)); err == nil {
			if matched != "" && filepath.Clean(abs(matched)) != filepath.Clean(abs(candidate)) {
				return "", fmt.Errorf("path %q is ambiguous; existing interpretations are %q and %q; use the intended literal path", input, matched, candidate)
			}
			matched = candidate
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("cannot resolve copied path %q as %q: %w", input, candidate, err)
		}
	}
	if matched != "" {
		return matched, nil
	}
	return input, nil
}

func normalizeSearchPath(dir string, def tool.Definition) tool.Definition {
	if dir == "" {
		return def
	}
	def.NormalizeArgs = func(ctx context.Context, args tool.Args) (tool.Args, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		normalized := make(tool.Args, len(args))
		for key, value := range args {
			normalized[key] = value
		}
		input, _ := args["path"].(string)
		path, err := ExistingPath(dir, input)
		if err != nil {
			return nil, err
		}
		normalized["path"] = path
		normalized, err = scopedArgs(dir, normalized)
		return normalized, err
	}
	return def
}

// DiscoveryPath resolves filesystem aliases before admitting the selected scope.
// Search still uses rg's normal non-following traversal; this is not a Bash sandbox.
func DiscoveryPath(workspace, input string, outside bool) (string, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if outside && !filepath.IsAbs(input) {
		return "", fmt.Errorf("outside discovery requires an explicit absolute path")
	}
	if input == "" {
		input = "."
	}
	target := input
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	if outside {
		info, err := os.Lstat(target)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("outside discovery refuses final symlinks; supply the actual absolute path")
		}
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	inside := rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	if outside == inside {
		if outside {
			return "", fmt.Errorf("path is inside workspace %s; use ordinary read/find tools", root)
		}
		return "", fmt.Errorf("path must stay within workspace %s; use explicit read_outside or find_outside for outside discovery", root)
	}
	return target, nil
}

func scopedArgs(dir string, args tool.Args) (tool.Args, error) {
	if dir == "" {
		return args, nil
	} // Preserve standalone search semantics.
	input, _ := args["path"].(string)
	target, err := DiscoveryPath(dir, input, false)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return nil, err
	}
	// Canonical in-project locators are directly reusable across read/edit/search.
	normalized := make(tool.Args, len(args))
	for key, value := range args {
		normalized[key] = value
	}
	normalized["path"] = relative
	return normalized, nil
}

func discoveryPurpose(pc *prompts.Catalog, key, dir string) string {
	text := pc.Text(key)
	if dir != "" {
		text += " " + pc.Text("tools.discovery.workspace_scope")
	}
	return text
}
