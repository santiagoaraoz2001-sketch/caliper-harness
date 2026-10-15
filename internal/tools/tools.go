// Package tools registers every built-in tool into a registry.
package tools

import (
	"io"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/ask"
	"specific-harness/internal/tools/bash"
	"specific-harness/internal/tools/fetch"
	"specific-harness/internal/tools/files"
	"specific-harness/internal/tools/search"
)

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
	reg.Register(helpDefinition(cfg, reg, cfg.PromptCatalog, current...))
	for _, d := range w.Definitions() {
		reg.Register(d)
	}
}
