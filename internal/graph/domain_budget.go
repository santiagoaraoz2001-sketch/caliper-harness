package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"specific-harness/internal/memory"
)

type domainArticle struct {
	key, version, previousBody string
	sections                   []memory.Section
	refs                       []memory.Reference
}

type domainCounterKey struct{}

// Previous versions and direct references are source metadata, not recursive
// retrievals. Their existing source byte budget still applies.
func (r *Resolver) domainPrevious(ctx context.Context, key, previous string) (string, error) {
	if previous == "" {
		return "", nil
	}
	s, err := scope(ctx)
	if err != nil {
		return "", err
	}
	_, remaining := available(ctx)
	if remaining <= 0 {
		return "", fmt.Errorf("domain previous version source budget exhausted")
	}
	entries, err := memory.Knowledge(ctx, r.store, s.root, "", previous, 1, remaining, true, []string{"key"})
	if err != nil {
		return "", err
	}
	if len(entries) != 1 || entries[0].Key != key {
		return "", fmt.Errorf("domain previous version unavailable for %s", key)
	}
	if err := charge(ctx, 0, entries[0].Body); err != nil {
		return "", err
	}
	return entries[0].Body, nil
}

func (r *Resolver) domainReferences(ctx context.Context, previous string) ([]memory.Reference, error) {
	s, err := scope(ctx)
	if err != nil {
		return nil, err
	}
	_, remaining := available(ctx)
	if remaining <= 0 {
		return nil, fmt.Errorf("domain reference source budget exhausted")
	}
	return memory.References(ctx, r.store, s.root, previous, false, remaining, func(raw string) error { return charge(ctx, 0, raw) })
}

// Validate complete authored sections before the existing atomic append, even
// when the caller requests only an ID. Ordinary project facts never enter here.
func (r *Resolver) admitDomainWrite(ctx context.Context, key, body, previous string, refs []memory.Reference) error {
	s, err := scope(ctx)
	if err != nil || !s.model || !memory.ProjectGuide(key) {
		return err
	}
	previousBody, err := r.domainPrevious(ctx, key, previous)
	if err != nil {
		return err
	}
	if refs == nil && previous != "" {
		refs, err = r.domainReferences(ctx, previous)
		if err != nil {
			return err
		}
	}
	count, _ := ctx.Value(domainCounterKey{}).(memory.TokenCounter)
	_, err = memory.AdmitDomainSections(ctx, count, key, previousBody, memory.Sections(body, "", ""), refs)
	return err
}

// Omit only the classified domain values. A failed domain admission must not
// erase ordinary facts in a sibling field or in the same knowledge page.
func omitDomains(b *requestBudget, data json.RawMessage) (json.RawMessage, string, error) {
	var roots map[string]json.RawMessage
	if err := json.Unmarshal(data, &roots); err != nil {
		return nil, "", err
	}
	paths := []string{}
	for alias, indices := range b.domains {
		root := bytes.TrimSpace(roots[alias])
		if len(root) > 0 && root[0] == '{' {
			delete(roots, alias)
			paths = append(paths, alias)
			continue
		}
		var values []json.RawMessage
		if err := json.Unmarshal(root, &values); err != nil {
			return nil, "", err
		}
		marked := map[int]bool{}
		for _, i := range indices {
			if i < 0 || i >= len(values) {
				return nil, "", fmt.Errorf("domain projection index unavailable")
			}
			marked[i] = true
		}
		kept := []json.RawMessage{}
		for i, value := range values {
			if marked[i] {
				paths = append(paths, fmt.Sprintf("%s[%d]", alias, i))
			} else {
				kept = append(kept, value)
			}
		}
		if len(kept) == 0 {
			delete(roots, alias)
		} else {
			roots[alias], _ = json.Marshal(kept)
		}
	}
	sort.Strings(paths)
	if len(roots) == 0 {
		return nil, strings.Join(paths, ", "), nil
	}
	raw, err := json.Marshal(roots)
	return raw, strings.Join(paths, ", "), err
}

// WithDomainTokenCounter supplies the coding model's count-only capability.
// Host-only graph access has no model-context admission boundary.
func (a *API) WithDomainTokenCounter(count memory.TokenCounter) *API {
	copy := *a
	copy.domainTokens = count
	return &copy
}

func noteDomain(ctx context.Context, index int) {
	b := budget(ctx)
	s, err := scope(ctx)
	if b == nil || err != nil || !s.model {
		return
	}
	alias := graphql.GetFieldContext(ctx).Field.Alias
	if b.domains == nil {
		b.domains = map[string][]int{}
	}
	b.domains[alias] = append(b.domains[alias], index)
}

// Count after GraphQL projection, so aliases, repeated fields and navigation
// metadata cannot escape the bound. Only catalog values are charged; an ordinary
// project record in the same response is not domain guidance.
func (a *API) admitDomains(ctx context.Context, b *requestBudget, data json.RawMessage) (string, error) {
	if len(b.domains) == 0 {
		return "", nil
	}
	seen := map[string]bool{}
	for _, article := range b.domainArticles {
		for _, section := range article.sections {
			identity := article.version + "\x00" + section.ID
			if seen[identity] {
				continue
			}
			seen[identity] = true
			if basis, err := memory.AdmitDomainSections(ctx, a.domainTokens, article.key, article.previousBody, []memory.Section{section}, article.refs); err != nil {
				return basis, err
			}
		}
	}
	var roots map[string]json.RawMessage
	if err := json.Unmarshal(data, &roots); err != nil {
		return "", fmt.Errorf("domain projection unavailable: %w", err)
	}
	selected := map[string]json.RawMessage{}
	for alias, indices := range b.domains {
		root := bytes.TrimSpace(roots[alias])
		if len(root) > 0 && root[0] == '{' {
			// appendKnowledge returns the one committed version, not a list.
			selected[alias] = root
			continue
		}
		var values []json.RawMessage
		if err := json.Unmarshal(roots[alias], &values); err != nil {
			return "", fmt.Errorf("domain projection unavailable: %w", err)
		}
		parts := []json.RawMessage{}
		for _, i := range indices {
			if i >= len(values) {
				return "", fmt.Errorf("domain projection index unavailable")
			}
			parts = append(parts, values[i])
		}
		selected[alias], _ = json.Marshal(parts)
	}
	raw, err := json.Marshal(selected)
	if err != nil {
		return "", err
	}
	return memory.AdmitDomainEnvelope(ctx, a.domainTokens, func(basis string) ([]byte, error) {
		return json.Marshal(Result{Data: raw, DomainBudgetBasis: basis})
	})
}
