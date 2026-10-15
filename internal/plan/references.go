package plan

import (
	"fmt"
	"sort"
	"specific-harness/internal/store"
	"strings"
	"time"
)

// Reference includes retired history so ordinal aliases never silently change owner.
type Reference struct {
	Kind, ID, Ref, State string
	Parent               string // ephemeral owning UUID, not persisted focus
	Closed               bool   // completed ancestor blocks ordinary child mutations
	Ancestors            []string
	Children             int
	Retired              bool
}

func ByCreated[T any](items []T, at func(T) (time.Time, string)) {
	sort.Slice(items, func(i, j int) bool {
		a, x := at(items[i])
		b, y := at(items[j])
		if a.Equal(b) {
			return x < y
		}
		return a.Before(b)
	})
}
func ResolveReference(nodes []Reference, ref string) (Reference, error) {
	var found []Reference
	var alternatives []string
	for _, n := range nodes {
		alias := n.Ref
		if n.Kind != "plan" {
			_, alias, _ = strings.Cut(n.Ref, "-")
		}
		if n.ID == ref || n.Ref == ref || alias == ref {
			found = append(found, n)
			alternatives = append(alternatives, n.Ref)
		}
	}
	if len(found) == 0 {
		return Reference{}, fmt.Errorf("unknown plan target %q", ref)
	}
	if len(found) > 1 {
		return Reference{}, fmt.Errorf("ambiguous %q — use %s", ref, strings.Join(alternatives, ", "))
	}
	if found[0].Retired {
		return Reference{}, fmt.Errorf("%s (%s) is retired", found[0].Ref, found[0].ID)
	}
	return found[0], nil
}
func References(s *store.Store) ([]Reference, error) {
	ps, err := ListPlans(s, "")
	if err != nil {
		return nil, err
	}
	ByCreated(ps, func(p *Plan) (time.Time, string) { return p.CreatedAt, p.ID })
	var out []Reference
	for pi, p := range ps {
		pr := fmt.Sprintf("P%d", pi+1)
		as, err := PlanApproaches(s, p.ID)
		if err != nil {
			return nil, err
		}
		ByCreated(as, func(a *Approach) (time.Time, string) { return a.CreatedAt, a.ID })
		retired := p.State == PlanRetired
		out = append(out, Reference{Kind: "plan", ID: p.ID, Ref: pr, State: p.State, Children: len(as), Retired: retired})
		for ai, a := range as {
			ar := fmt.Sprintf("%s-A%d", pr, ai+1)
			ts, err := ApproachTasks(s, a.ID)
			if err != nil {
				return nil, err
			}
			ByCreated(ts, func(t *Task) (time.Time, string) { return t.CreatedAt, t.ID })
			closed := retired || a.State == ApproachRetired
			out = append(out, Reference{Kind: "approach", ID: a.ID, Ref: ar, State: a.State, Parent: p.ID, Closed: p.State == PlanComplete, Ancestors: []string{pr}, Children: len(ts), Retired: closed})
			for ti, t := range ts {
				out = append(out, Reference{Kind: "task", ID: t.ID, Ref: fmt.Sprintf("%s-T%d", ar, ti+1), State: t.State, Parent: a.ID, Closed: p.State == PlanComplete || a.State == ApproachComplete, Ancestors: []string{pr, ar}, Retired: closed})
			}
		}
	}
	return out, nil
}
