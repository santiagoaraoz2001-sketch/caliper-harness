package tools

import (
	"context"
	"fmt"
	"strings"

	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

// Help resolves the active view at execution time, including newly claimed checks.
func helpDefinition(cfg config.Config, reg *tool.Registry, pc *prompts.Catalog, current ...func() *tool.Registry) tool.Definition {
	pc = prompts.Select(pc)
	return tool.Definition{Opening: true, Prompts: pc, Name: "help", Desc: pc.Text("tools.help.description"), Purpose: pc.Text("tools.help.purpose"), Params: []tool.Param{{Name: "topic", Type: tool.TypeString, AllowEmpty: true, Desc: pc.Text("tools.help.parameters.topic")}}, Execute: func(ctx context.Context, a tool.Args) (tool.Result, error) {
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		active := reg
		if len(current) > 0 {
			active = current[0]()
		}
		if active == nil {
			return tool.Result{}, fmt.Errorf("help requires the current tool view")
		}
		topic, _ := a["topic"].(string)
		topic = strings.TrimPrefix(topic, "help.")
		names := active.Names()
		var text string
		if topic == "schema" || strings.HasPrefix(topic, "schema.") {
			var err error
			text, err = helpSchema(topic, active, reg, pc)
			if err != nil {
				return tool.Result{}, err
			}
		} else if topic == "" {
			text = pc.Text("help.overview") + "\n" + helpSchemaIndex(active, pc) + "\n" + pc.Text("help.reference.index")
			if graph, ok := active.Lookup("graphql"); ok && graph.GraphQLFields != nil {
				text = graph.Purpose + "\n" + helpSchemaIndex(active, pc)
			}
		} else {
			if topic == "guide" {
				text = pc.Text("help.overview")
				if graph, ok := active.Lookup("graphql"); ok && graph.GraphQLFields != nil {
					text = graph.Purpose
				}
			} else {
				if topic == "reference" {
					topic = "reference.index"
				}
				names = []string{topic}
			}
			for _, name := range names {
				owner := name
				if strings.HasPrefix(name, "graphql.") {
					owner = "graphql"
				}
				if name == "read.definition" || name == "read.outline" {
					owner = "read"
				}
				def, available := active.ResolveName(owner)
				if available {
					owner = def.Name
				}
				known := available || pc.Has("tools."+owner+".description")
				if reg != nil {
					_, registered := reg.Lookup(owner)
					known = known || registered
				}
				reference := strings.HasPrefix(name, "reference.")
				documented := pc.Has("help."+name) && (reference || known)
				if !documented {
					return tool.Result{}, fmt.Errorf("%s", pc.Format("guidance.help.unknown", name))
				}
				if reference {
					text += "\n" + pc.Text("help."+name)
					continue
				}
				if available {
					if owner == "graphql" && def.GraphQLFields != nil && name == "graphql.lifecycle" {
						text += "\n" + pc.Text("help.unavailable")
						continue
					}
					text += pc.Format("help.available", owner, def.Schema())
				} else {
					text += "\n" + pc.Text("help.unavailable")
				}
				// The current definition/question and native example come first;
				// prose below explains use cases without competing future calls.
				if owner == "graphql" && available && def.GraphQLFields != nil {
					text += "\n" + pc.Text("help.graphql.readonly")
					for _, field := range def.GraphQLFields {
						if field == "appendKnowledge" {
							text += "\n" + pc.Text("help.graphql.worker_write")
							text += "\n" + pc.Text("help.graphql.worker_authority")
						}
					}
				} else if graph, ok := active.Lookup("graphql"); owner == "help" && ok && graph.GraphQLFields != nil {
					text += "\n" + pc.Text("system.help_available.knowledge")
				} else {
					text += "\n" + pc.Text("help."+name)
					if owner == "graphql" && available && name == "graphql.knowledge" {
						text += "\n" + pc.Text("help.graphql.readonly")
						text += "\n" + pc.Text("help.graphql.worker_write")
					}
				}
				text += "\n"
			}
			text += pc.Format("help.output_caps", cfg.Tools.Bash.MaxBytes, cfg.Tools.Bash.MaxLines, cfg.Tools.Search.MaxBytes, cfg.Tools.Search.MaxMatches)
		}
		if len(text) > cfg.Tools.Bash.MaxBytes && topic == "" {
			text = pc.Format("help.compact_index", strings.Join(names, ", "))
		}
		if len(text) > cfg.Tools.Bash.MaxBytes {
			return tool.Result{}, fmt.Errorf("%s", pc.Format("help.oversize_guidance", cfg.Tools.Bash.MaxBytes))
		}
		return tool.Result{Text: text}, nil
	}}
}
