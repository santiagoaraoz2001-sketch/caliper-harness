// Package tools registers every built-in tool into a registry.
package tools

import (
	"io"
	"path/filepath"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/ask"
	"specific-harness/internal/tools/bash"
	"specific-harness/internal/tools/fetch"
	"specific-harness/internal/tools/files"
	"specific-harness/internal/tools/search"
	waittool "specific-harness/internal/tools/wait"
	"specific-harness/internal/tools/workflowtools"
)

// LibraryPath returns the shared reusable workflow library file. The
// library is user-scoped, not project-scoped.
func LibraryPath(workspace string) string {
	if dir, err := config.UserDataDir(); err == nil && dir != "" {
		return filepath.Join(dir, "workflow-library.json")
	}
	return filepath.Join(workspace, ".caliper", "workflow-library.json")
}

func registerLibraryTools(reg *tool.Registry, cfg config.Config, root string) {
	path := LibraryPath(root)
	reg.Register(waittool.New(root, cfg.PromptCatalog))
	reg.Register(workflowtools.BuildBlock(path, cfg.PromptCatalog))
	reg.Register(workflowtools.BuildWorkflow(path, cfg.PromptCatalog))
	reg.Register(workflowtools.LibraryWorkflow(path, cfg.PromptCatalog))
}

// RegisterAll adds every built-in tool. in/out serve the ask tool;
// callers pass real stdio (CLI) or buffers (tests). askFunc, when
// set, answers ask-tool questions instead of in/out (TUI transport).
func RegisterAll(reg *tool.Registry, cfg config.Config, in io.Reader, out io.Writer, askFunc ask.Querier, current ...func() *tool.Registry) {
	reg.Register(bash.New(cfg.Tools.Bash, cfg.PromptCatalog))
	reg.Register(search.NewGrep(cfg.Tools.Search, cfg.PromptCatalog))
	reg.Register(search.NewFind(cfg.Tools.Search, cfg.PromptCatalog))
	reg.Register(search.NewFindFileAt(cfg.Tools.Search, "", cfg.PromptCatalog))
	reg.Register((&files.Workspace{Path: ".", Caps: cfg.Tools.Bash, Prompts: cfg.PromptCatalog}).FindDirDefinition())
	reg.Register(fetch.New(cfg.Tools.Fetch, cfg.PromptCatalog))
	reg.Register(ask.New(&ask.Prompter{In: in, Out: out, Ask: askFunc}, cfg.PromptCatalog))
	registerLibraryTools(reg, cfg, ".")
	reg.Register(helpDefinition(cfg, reg, cfg.PromptCatalog, current...))
}

// RegisterAt shares a descriptor-free workspace across parent/child definitions.
func RegisterAt(reg *tool.Registry, cfg config.Config, in io.Reader, out io.Writer, askFunc ask.Querier, w *files.Workspace, current ...func() *tool.Registry) {
	reg.Register(bash.NewAt(cfg.Tools.Bash, w.Path, cfg.PromptCatalog))
	reg.Register(search.NewGrepAt(cfg.Tools.Search, w.Path, cfg.PromptCatalog))
	reg.Register(search.NewFindAt(cfg.Tools.Search, w.Path, cfg.PromptCatalog))
	reg.Register(search.NewFindFileAt(cfg.Tools.Search, w.Path, cfg.PromptCatalog))
	reg.Register(w.FindDirDefinition())
	for _, d := range w.OutsideDefinitions(cfg.Tools.Search) {
		reg.Register(d)
	}
	reg.Register(fetch.New(cfg.Tools.Fetch, cfg.PromptCatalog))
	reg.Register(ask.New(&ask.Prompter{In: in, Out: out, Ask: askFunc}, cfg.PromptCatalog))
	registerLibraryTools(reg, cfg, w.Path)
	reg.Register(helpDefinition(cfg, reg, cfg.PromptCatalog, current...))
	for _, d := range w.Definitions() {
		reg.Register(d)
	}
}
