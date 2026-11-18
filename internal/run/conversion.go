package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"specific-harness/internal/launch"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/plan"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	toolslib "specific-harness/internal/tools"
	"specific-harness/internal/workflowlib"
)

// convert_skill turns one conventional Markdown skill into Caliper's
// native structures, step by step: the host reads and outlines the
// source, the model maps one piece at a time, and publish validates
// coverage (every source heading mapped or explicitly preserved),
// then writes through the same owners as hand-authored work — domain
// graph nodes via appendKnowledge semantics, or library blocks plus a
// workflow via the workflow library. The full source text is always
// preserved as its own knowledge node, so nothing is silently dropped.
const maxSkillSourceBytes = 512 << 10

func conversionDefinition(d launch.Deps, sessionRef string, catalog *prompts.Catalog) tool.Definition {
	pc := catalog
	if pc == nil || !pc.Has("tools.convert_skill.description") {
		pc = prompts.Builtin()
	}
	param := func(name, typ string, required bool, enum ...string) tool.Param {
		return tool.Param{Name: name, Type: typ, Required: required, ExactEnum: len(enum) > 0, Enum: enum, Desc: pc.Text("tools.convert_skill.parameters." + name)}
	}
	return tool.Definition{
		Name: "convert_skill", Prompts: pc, Desc: pc.Text("tools.convert_skill.description"), Purpose: pc.Text("tools.convert_skill.purpose"),
		Params: []tool.Param{
			param("action", tool.TypeString, true, "start", "add", "get", "list", "publish"),
			param("id", tool.TypeString, false),
			param("target", tool.TypeString, false, "domain", "workflow"),
			param("source_path", tool.TypeString, false),
			param("source_heading", tool.TypeString, false),
			param("kind", tool.TypeString, false, "section", "example", "negative"),
			param("piece_title", tool.TypeString, false),
			param("body", tool.TypeString, false),
			param("evidence", tool.TypeString, false),
			param("links", tool.TypeString, false),
			param("preserve", tool.TypeString, false),
		},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			if d.Store == nil || d.Store.Identity() == "" {
				return tool.Result{}, fmt.Errorf("convert_skill needs the owning graph store")
			}
			ctx = plan.WithProject(ctx, d.Workspace)
			get := func(name string) string { value, _ := args[name].(string); return strings.TrimSpace(value) }
			path := toolslib.LibraryPath(d.Workspace)
			s, err := workflowlib.Open(path)
			if err != nil {
				return tool.Result{}, err
			}
			receipt := func(c workflowlib.Conversion, next string) (tool.Result, error) {
				view := map[string]any{
					"id": c.ID, "title": c.Title, "target": c.Target, "status": c.Status,
					"source_path": c.SourcePath, "source_sha256": c.SourceSHA256,
					"headings": c.Headings, "pieces": c.Pieces, "revision": c.Revision,
					"published_keys": c.PublishedKeys, "next_action": next,
				}
				raw, err := json.Marshal(view)
				return tool.Result{Text: string(raw)}, err
			}
			switch get("action") {
			case "start":
				sourcePath := get("source_path")
				abs, err := confinedSourcePath(d.Workspace, sourcePath)
				if err != nil {
					return tool.Result{}, err
				}
				raw, err := os.ReadFile(abs)
				if err != nil {
					return tool.Result{}, err
				}
				if len(raw) > maxSkillSourceBytes {
					return tool.Result{}, fmt.Errorf("skill source is %d bytes; conversion accepts at most %d", len(raw), maxSkillSourceBytes)
				}
				title, _, _ := workflowlib.ParseSkillOutline(string(raw))
				c, err := s.CreateConversionDraft(get("id"), title, get("target"), sourcePath, string(raw))
				if err != nil {
					return tool.Result{}, err
				}
				return receipt(c, "Map one source heading at a time with action=add (kind section, plus one example and one negative piece for domain targets), then action=publish.")
			case "add":
				c, err := s.AddConversionPiece(get("id"), 0, workflowlib.ConversionPiece{
					SourceHeading: get("source_heading"), Kind: get("kind"), Title: get("piece_title"),
					Body: get("body"), Evidence: get("evidence"), Links: get("links"),
				})
				if err != nil {
					return tool.Result{}, err
				}
				return receipt(c, "Add the next piece, or publish when every heading is mapped or preserved.")
			case "get":
				c, ok := s.GetConversion(get("id"))
				if !ok {
					return tool.Result{}, fmt.Errorf("unknown conversion %s", get("id"))
				}
				return receipt(c, "")
			case "list":
				var items []map[string]any
				for _, c := range s.ListConversions() {
					items = append(items, map[string]any{"id": c.ID, "title": c.Title, "target": c.Target, "status": c.Status, "pieces": len(c.Pieces), "headings": len(c.Headings)})
				}
				raw, err := json.Marshal(map[string]any{"conversions": items})
				return tool.Result{Text: string(raw)}, err
			case "publish":
				c, ok := s.GetConversion(get("id"))
				if !ok {
					return tool.Result{}, fmt.Errorf("unknown conversion %s", get("id"))
				}
				if c.Status != workflowlib.ConversionDraft {
					return tool.Result{}, fmt.Errorf("conversion %s is already published", c.ID)
				}
				var preserve []string
				for _, line := range strings.Split(get("preserve"), "\n") {
					if line = strings.TrimSpace(line); line != "" {
						preserve = append(preserve, line)
					}
				}
				if uncovered := c.UncoveredHeadings(preserve); len(uncovered) > 0 {
					return tool.Result{}, fmt.Errorf("source headings not mapped or preserved: %s — map each with action=add or list it in preserve (kept verbatim in the source node)", strings.Join(uncovered, "; "))
				}
				var keys []string
				var err error
				if c.Target == "domain" {
					keys, err = publishDomainConversion(ctx, d, sessionRef, c)
				} else {
					keys, err = publishWorkflowConversion(ctx, d, sessionRef, s, c)
				}
				if err != nil {
					return tool.Result{}, err
				}
				c, err = s.MarkConversionPublished(c.ID, keys)
				if err != nil {
					return tool.Result{}, err
				}
				return receipt(c, "Conversion published; inspect it through the same graph/library surfaces as hand-authored work.")
			default:
				return tool.Result{}, fmt.Errorf("action must be start, add, get, list, or publish")
			}
		},
	}
}

func confinedSourcePath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("source_path must be a workspace-relative path")
	}
	abs := filepath.Join(root, filepath.Clean(rel))
	r, err := filepath.Rel(root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("source_path escapes the workspace")
	}
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("source_path symlink refused")
	}
	return abs, nil
}

var slugClean = regexp.MustCompile(`[^a-z0-9]+`)

func conversionSlug(title string) string {
	slug := strings.Trim(slugClean.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if slug == "" {
		return "section"
	}
	return slug
}

func conversionRefs(links string) []memory.Reference {
	var refs []memory.Reference
	for _, line := range strings.Split(links, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, section, _ := strings.Cut(line, "#")
		refs = append(refs, memory.Reference{Key: strings.TrimSpace(key), Relation: "explains", Section: strings.TrimSpace(section)})
	}
	return refs
}

func publishDomainConversion(ctx context.Context, d launch.Deps, sessionRef string, c workflowlib.Conversion) ([]string, error) {
	kinds := map[string]bool{}
	for _, p := range c.Pieces {
		kinds[p.Kind] = true
	}
	if !kinds["example"] || !kinds["negative"] {
		return nil, fmt.Errorf("domain conversion needs at least one example piece and one negative piece before publishing")
	}
	if len(c.Pieces) == 0 {
		return nil, fmt.Errorf("conversion has no pieces to publish")
	}
	counter := func(context.Context, string) (int, error) { return 0, llm.ErrTokenCountingUnsupported }
	type node struct {
		key, body string
		refs      []memory.Reference
	}
	var nodes []node
	for _, p := range c.Pieces {
		key := "project/guides/" + c.ID + "/" + conversionSlug(p.Title)
		body := "## " + p.Title + "\n" + strings.TrimSpace(p.Body)
		refs := conversionRefs(p.Links)
		if _, err := memory.AdmitDomainSections(ctx, counter, key, "", memory.Sections(body, "", ""), refs); err != nil {
			return nil, fmt.Errorf("piece %q does not fit the domain budget — split it at a meaningful decision and retry: %w", p.Title, err)
		}
		nodes = append(nodes, node{key, body, refs})
	}
	// Every piece admitted before any write: a refused conversion
	// publishes nothing.
	sourceKey := "project/skills/" + c.ID + "/source"
	if _, err := memory.PutKnowledgeReferences(ctx, d.Store, d.Workspace, sessionRef, sourceKey, c.SourceText, "", "", c.SourcePath, nil); err != nil {
		return nil, fmt.Errorf("preserve source skill: %w", err)
	}
	keys := []string{sourceKey}
	for _, n := range nodes {
		if _, err := memory.PutKnowledgeReferences(ctx, d.Store, d.Workspace, sessionRef, n.key, n.body, "", "", c.SourcePath, n.refs); err != nil {
			return keys, fmt.Errorf("publish %s: %w", n.key, err)
		}
		keys = append(keys, n.key)
	}
	var index strings.Builder
	index.WriteString("## choices\nConverted from " + c.SourcePath + " (sha256 " + c.SourceSHA256 + ").\n")
	var indexRefs []memory.Reference
	for _, n := range nodes {
		index.WriteString("- " + n.key + "\n")
		indexRefs = append(indexRefs, memory.Reference{Key: n.key, Relation: "explains", SourceSection: "choices", Label: "Read this converted section"})
	}
	indexKey := "project/guides/" + c.ID + "/index"
	if _, err := memory.PutKnowledgeReferences(ctx, d.Store, d.Workspace, sessionRef, indexKey, index.String(), "", "", c.SourcePath, indexRefs); err != nil {
		return keys, fmt.Errorf("publish %s: %w", indexKey, err)
	}
	return append(keys, indexKey), nil
}

func publishWorkflowConversion(ctx context.Context, d launch.Deps, sessionRef string, s *workflowlib.Store, c workflowlib.Conversion) ([]string, error) {
	if len(c.Pieces) == 0 {
		return nil, fmt.Errorf("conversion has no pieces to publish")
	}
	sourceKey := "project/skills/" + c.ID + "/source"
	if _, err := memory.PutKnowledgeReferences(ctx, d.Store, d.Workspace, sessionRef, sourceKey, c.SourceText, "", "", c.SourcePath, nil); err != nil {
		return nil, fmt.Errorf("preserve source skill: %w", err)
	}
	var steps []string
	for _, p := range c.Pieces {
		blockID := c.ID + "-" + conversionSlug(p.Title)
		if _, ok := s.GetBlock(blockID, 0); !ok {
			b, err := s.CreateBlockDraft(blockID, p.Title)
			if err != nil {
				return nil, err
			}
			evidence := p.Evidence
			if evidence == "" {
				evidence = "recorded result for this step"
			}
			fields := map[string]string{
				"purpose": firstNonBlank(p.Purpose, p.Title), "inputs": "task request",
				"impl_type": "main_model", "impl_instructions": p.Body,
				"outputs": "recorded result", "completion_evidence": evidence,
				"failure_handling": "return to this step with the failed output; do not advance",
				"examples":         "converted from " + c.SourcePath + " heading: " + p.SourceHeading,
			}
			if b, _, err = s.SetBlockFields(blockID, b.Revision, fields); err != nil {
				return nil, err
			}
			if _, err := s.PublishBlock(blockID, b.Revision); err != nil {
				return nil, err
			}
		}
		steps = append(steps, blockID+"@1")
	}
	if _, ok := s.GetWorkflow(c.ID, 0); ok {
		return []string{sourceKey, c.ID}, nil
	}
	w, err := s.CreateWorkflowDraft(c.ID, c.Title)
	if err != nil {
		return nil, err
	}
	fields := map[string]string{
		"purpose": firstNonBlank(c.Title, c.ID) + " (converted from " + c.SourcePath + " sha256=" + c.SourceSHA256 + ")",
		"applies_to": "skill-conversion",
		"inputs":     "task request", "steps": strings.Join(steps, "\n"),
		"completion_requirements": "every step advanced with observed evidence",
		"revision_route":          "return to the owning step with the failure evidence; never skip ahead",
	}
	if w, _, err = s.SetWorkflowFields(c.ID, w.Revision, fields); err != nil {
		return nil, err
	}
	if _, err := s.PublishWorkflow(c.ID, w.Revision); err != nil {
		return nil, err
	}
	return []string{sourceKey, c.ID}, nil
}

func firstNonBlank(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
