package run

import (
	"specific-harness/internal/config"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/bash"
)

// Replace only the ordinary Bash definition; retain aliases, dynamic help and
// existing role restrictions. Register still rejects duplicate ownership.
func bindServiceOwner(reg *tool.Registry, cfg config.Config, workspace string, owner *bash.ServiceOwner) *tool.Registry {
	reg.Replace(bash.NewAtOwned(cfg.Tools.Bash, workspace, owner, cfg.PromptCatalog))
	return reg
}
