package graph

import (
	"context"
	"fmt"
	"specific-harness/internal/graph/model"
	"specific-harness/internal/memory"
	"strings"
	"time"
)

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func knowledgeFields(ctx context.Context) (map[string]bool, []string) {
	want := map[string]bool{}
	selection := selected(ctx, rootSelection(ctx), "Knowledge")
	fields := selectedNames(selection)
	// Classification belongs to the stored key even when the caller omits or
	// aliases it. Never infer domain provenance from projected user fields.
	fields = append(fields, "key", "previousId")
	for _, name := range fields {
		want[name] = true
	}
	for _, field := range selection {
		if field.Name == "references" {
			for _, child := range selected(ctx, field.SelectionSet, "KnowledgeReference") {
				want["references."+child.Name] = true
			}
		}
		if field.Name == "sections" {
			for _, child := range selected(ctx, field.SelectionSet, "KnowledgeSection") {
				want["sections."+child.Name] = true
				if child.Name != "references" {
					continue
				}
				want["sections.references"] = true
				for _, ref := range selected(ctx, child.SelectionSet, "KnowledgeReference") {
					want["references."+ref.Name] = true
				}
			}
		}
	}
	if want["freshness"] {
		fields = append(fields, "filePath", "fileSHA256")
	}
	return want, fields
}

func (r *Resolver) knowledgePage(ctx context.Context, key, id string, n int, filter memory.KnowledgeSelection, section string) ([]*model.Knowledge, error) {
	s, err := scope(ctx)
	if err != nil {
		return nil, err
	}
	rows, bytes := available(ctx)
	if n <= 0 || n > rows {
		n = rows
	}
	if n <= 0 || bytes <= 0 {
		return nil, fmt.Errorf("GraphQL knowledge source budget exhausted")
	}
	if filter.Offset < 0 || filter.Offset > 100000 {
		return nil, fmt.Errorf("knowledge offset must be 0..100000")
	}
	if key != "" && id != "" {
		return nil, fmt.Errorf("knowledge accepts key or id, not both")
	}
	if filter.LinkedFrom != "" && filter.LinkedTo != "" {
		return nil, fmt.Errorf("choose linkedFrom or linkedTo")
	}
	want, fields := knowledgeFields(ctx)
	linkedBuiltins := map[string]bool{}
	if filter.LinkedFrom != "" {
		linkedBuiltins, err = memory.LinkedBuiltins(ctx, r.store, s.root, filter.LinkedFrom, filter.Relation)
		if err != nil {
			return nil, err
		}
	}
	entries := []*memory.Entry{}
	// Builtins are explicitly discoverable by keyword or key, not inserted into
	// every project inventory or into automatic memory recall.
	if strings.HasPrefix(key, "caliper/") || strings.HasPrefix(id, "caliper-") || filter.Text != "" || len(linkedBuiltins) > 0 || filter.LinkedTo != "" {
		for _, candidate := range memory.BuiltinKeys {
			if filter.LinkedTo != "" {
				linked := false
				for _, ref := range memory.BuiltinReferences(candidate, false) {
					linked = linked || (ref.Key == filter.LinkedTo && (filter.Relation == "" || ref.Relation == filter.Relation))
				}
				if !linked {
					continue
				}
			}
			if filter.LinkedFrom != "" && !linkedBuiltins[candidate] {
				continue
			}
			if n <= 0 {
				break
			}
			if key != "" && key != candidate {
				continue
			}
			if e := memory.BuiltinKnowledge(candidate, id, filter.Text); e != nil {
				if filter.Offset == 0 {
					entries = append(entries, e)
					n--
				} else {
					filter.Offset--
				}
			}
		}
	}
	if n > 0 && !strings.HasPrefix(key, "caliper/") && !strings.HasPrefix(id, "caliper-") {
		more, e := memory.SearchKnowledge(ctx, r.store, s.root, key, id, n, bytes, want["body"] || want["sections"] || section != "", fields, filter)
		if e != nil {
			return nil, e
		}
		entries = append(entries, more...)
	}
	return r.knowledgeModels(ctx, entries, false, filter.Text, section)
}

func (r *Resolver) knowledgeModels(ctx context.Context, entries []*memory.Entry, mutation bool, text, section string) ([]*model.Knowledge, error) {
	s, err := scope(ctx)
	if err != nil {
		return nil, err
	}
	want, _ := knowledgeFields(ctx)
	count := len(entries)
	if mutation && count > 0 {
		count--
	}
	if err := charge(ctx, count); err != nil {
		return nil, err
	}
	out := make([]*model.Knowledge, 0, len(entries))
	for _, e := range entries {
		m := &model.Knowledge{ID: e.ID, Key: e.Key, Body: e.Body, CreatedAt: e.CreatedAt.Format(time.RFC3339), SessionRef: e.SessionRef, PreviousID: e.PreviousID, TaskID: e.TaskID, FilePath: e.FilePath, FileSha256: e.FileSHA256, Source: "project-authored", Freshness: "unknown", Sections: []*model.KnowledgeSection{}, References: []*model.KnowledgeReference{}}
		builtin := strings.HasPrefix(e.Key, "caliper/") && e.SessionRef == "caliper-default"
		if builtin {
			m.Source = "caliper-default"
			m.CreatedAt = ""
		}
		if want["sections"] || section != "" {
			for _, part := range memory.Sections(e.Body, text, section) {
				m.Sections = append(m.Sections, &model.KnowledgeSection{ID: part.ID, Title: part.Title, Body: part.Body, References: []*model.KnowledgeReference{}})
			}
			if section != "" {
				m.Body = ""
				for _, part := range m.Sections {
					m.Body += part.Body + "\n"
				}
				if len(m.Sections) == 0 {
					continue
				}
			}
		}
		if !want["body"] {
			m.Body = ""
		}
		if want["freshness"] && !builtin {
			m.Freshness = memory.Freshness(s.root, e)
		}
		var budgetRefs []memory.Reference
		refsLoaded := false
		if want["references"] || (want["sections.references"] && len(m.Sections) > 0) {
			_, remaining := available(ctx)
			var refs []memory.Reference
			var anchors []string
			if section != "" {
				anchors = []string{section}
			} else if !want["references"] {
				for _, part := range m.Sections {
					anchors = append(anchors, part.ID)
				}
			}
			if builtin {
				refs = memory.BuiltinReferences(e.Key, want["references.status"])
			} else {
				refs, err = memory.References(ctx, r.store, s.root, e.ID, want["references.status"], remaining, func(raw string) error { return charge(ctx, 0, raw) }, anchors...)
			}
			if err != nil {
				return nil, err
			}
			budgetRefs = append([]memory.Reference(nil), refs...)
			for i := range budgetRefs {
				budgetRefs[i].Status = ""
			}
			// A full body alongside text-filtered sections may need references
			// outside the displayed section list for its canonical budget.
			refsLoaded = !want["body"] || want["references"] || text == "" || section != ""
			if want["references.status"] {
				sourceBody := e.Body
				if !builtin && !want["body"] && !want["sections"] && section == "" {
					needsSource := false
					for _, ref := range refs {
						needsSource = needsSource || (ref.Status == "resolved" && ref.SourceSection != "")
					}
					if needsSource {
						_, remaining := available(ctx)
						if remaining <= 0 {
							return nil, fmt.Errorf("knowledge source section budget exhausted")
						}
						source, err := memory.Knowledge(ctx, r.store, s.root, "", e.ID, 1, remaining, true, []string{"key"})
						if err != nil {
							return nil, err
						}
						if len(source) != 1 {
							return nil, fmt.Errorf("knowledge source version unavailable")
						}
						sourceBody = source[0].Body
						if err := charge(ctx, 0, sourceBody); err != nil {
							return nil, err
						}
					}
				}
				anchors := map[string]bool{}
				for _, part := range memory.Sections(sourceBody, "", "") {
					anchors[part.ID] = true
				}
				for i := range refs {
					// Target failures take precedence. A resolved target does not
					// conceal a source heading removed by a later prose revision.
					if refs[i].Status == "resolved" && refs[i].SourceSection != "" && !anchors[refs[i].SourceSection] {
						refs[i].Status = "missing-source-section"
					}
				}
			}
			project := func(refs []memory.Reference) ([]*model.KnowledgeReference, error) {
				out := make([]*model.KnowledgeReference, 0, len(refs))
				for _, ref := range refs {
					if err := charge(ctx, 1, ref.Key, ref.Relation, ref.Section, ref.SourceSection, ref.Label, ref.Status); err != nil {
						return nil, err
					}
					out = append(out, &model.KnowledgeReference{Key: ref.Key, Relation: ref.Relation, Section: ref.Section, SourceSection: ref.SourceSection, Label: ref.Label, Status: ref.Status})
				}
				return out, nil
			}
			if want["references"] {
				m.References, err = project(memory.SectionReferences(refs, section))
				if err != nil {
					return nil, err
				}
			}
			if want["sections.references"] {
				for _, part := range m.Sections {
					part.References, err = project(memory.SectionReferences(refs, part.ID))
					if err != nil {
						return nil, err
					}
				}
			}
		}
		for _, part := range m.Sections {
			if err := charge(ctx, 1, part.ID, part.Title, part.Body); err != nil {
				return nil, err
			}
		}
		if err := charge(ctx, 0, m.ID, m.Key, m.Body, m.SessionRef, m.PreviousID, m.TaskID, m.FilePath, m.FileSha256, m.Source, m.Freshness); err != nil {
			return nil, err
		}
		if builtin || memory.ProjectGuide(e.Key) {
			if b := budget(ctx); b != nil && s.model && !mutation && (want["body"] || want["sections.body"]) {
				previousBody, err := r.domainPrevious(ctx, e.Key, e.PreviousID)
				if err != nil {
					return nil, err
				}
				refs := budgetRefs
				if !refsLoaded && builtin {
					refs = memory.BuiltinReferences(e.Key, false)
				} else if !refsLoaded {
					refs, err = r.domainReferences(ctx, e.ID)
					if err != nil {
						return nil, err
					}
				}
				filter := text
				if want["body"] {
					filter = ""
				}
				b.domainArticles = append(b.domainArticles, domainArticle{key: e.Key, version: e.ID, previousBody: previousBody, sections: memory.Sections(e.Body, filter, section), refs: refs})
			}
			noteDomain(ctx, len(out))
		}
		out = append(out, m)
	}
	return out, nil
}
