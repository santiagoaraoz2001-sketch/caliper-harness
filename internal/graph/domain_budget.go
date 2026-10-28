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
