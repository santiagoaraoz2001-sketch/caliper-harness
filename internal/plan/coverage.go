package plan

import (
	"encoding/json"
	"fmt"
)

// ArticleRef binds an authored selection to the exact Plan article at that index.
// It lives in the existing Approach title, so contract/revision snapshots include it.
type ArticleRef struct {
	Index int    `json:"article_index"`
	Text  string `json:"article"`
}
type ApproachBrief struct {
	Coverage []ArticleRef `json:"coverage"`
	Strategy string       `json:"strategy"`
}

// GoalArticles recognizes the existing guided document, retaining freeform goals.
// A declared but malformed articles value is not an unscoped/freeform plan.
func GoalArticles(raw string) ([]string, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return nil, nil
	}
	value, exists := fields["articles"]
	if !exists {
		return nil, nil
	}
	var articles []string
	if json.Unmarshal(value, &articles) != nil || len(articles) == 0 {
		return nil, fmt.Errorf("Plan articles must be a nonempty list of authored scope")
	}
	return articles, nil
}

// ApproachCoverage reads both new and historical associations without rewriting
// either. Unknown freeform titles have no explicit article coverage.
func ApproachCoverage(raw string) ([]ArticleRef, string, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return nil, "", nil
	}
	var strategy string
	json.Unmarshal(fields["strategy"], &strategy)
	var entries []map[string]json.RawMessage
	if value, exists := fields["coverage"]; exists {
		if json.Unmarshal(value, &entries) != nil || len(entries) == 0 {
			return nil, strategy, fmt.Errorf("Approach coverage must contain article references")
		}
	} else if _, exists := fields["article_index"]; exists {
		entries = []map[string]json.RawMessage{fields}
	} else {
		return nil, strategy, nil
	}
	refs := make([]ArticleRef, 0, len(entries))
	for _, entry := range entries {
		var index *int
		var text *string
		if json.Unmarshal(entry["article_index"], &index) != nil || index == nil || *index < 0 || json.Unmarshal(entry["article"], &text) != nil || text == nil {
			return nil, strategy, fmt.Errorf("Approach coverage needs an article index and its exact text")
		}
		refs = append(refs, ArticleRef{*index, *text})
	}
	return refs, strategy, nil
}

// Coverage is computed from owning records, never a second mutable progress log.
type Coverage struct {
	Articles   []string
	Approaches []*Approach
	Unresolved []*Approach
	Uncovered  []ArticleRef
}

func (c Coverage) Qualified() bool {
	return len(c.Approaches) > 0 && len(c.Unresolved) == 0 && len(c.Uncovered) == 0
}

// PlanCoverage is the shared completion predicate for navigation and transitions.
// Every declared Approach must qualify, even when its coverage overlaps another.
func PlanCoverage(s stateStore, id string) (Coverage, error) {
	var c Coverage
	p, err := GetPlan(s, id)
	if err != nil {
		return c, err
	}
	c.Articles, err = GoalArticles(p.Goal)
	if err != nil {
		return c, err
	}
	c.Approaches, err = PlanApproaches(s, id)
	if err != nil {
		return c, err
	}
	covered := make([]bool, len(c.Articles))
	for _, a := range c.Approaches {
		qualified, err := ApproachQualified(s, a.ID)
		if err != nil {
			return c, err
		}
		if !qualified {
			c.Unresolved = append(c.Unresolved, a)
		}
		if len(c.Articles) == 0 {
			continue
		}
		refs, _, err := ApproachCoverage(a.Title)
		if err != nil {
			return c, fmt.Errorf("approach %q: %w", a.ID, err)
		}
		for _, ref := range refs {
			if ref.Index >= len(c.Articles) || ref.Text != c.Articles[ref.Index] {
				return c, fmt.Errorf("approach %q coverage no longer matches Plan article %d; revise the authored scope explicitly", a.ID, ref.Index+1)
			}
			if qualified {
				covered[ref.Index] = true
			}
		}
	}
	for i, text := range c.Articles {
		if !covered[i] {
			c.Uncovered = append(c.Uncovered, ArticleRef{i, text})
		}
	}
	return c, nil
}
