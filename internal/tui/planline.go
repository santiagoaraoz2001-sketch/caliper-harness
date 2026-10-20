package tui

import (
	"fmt"

	"specific-harness/internal/plan"
	"specific-harness/internal/store"
)

// planStatus reads the latest non-retired plan plus up to two
// in-flight approaches with done/total counts. ok=false means the
// store was unreadable (keep stale); ok=true with empty title means
// genuinely planless. Built on the plan API — no new Cypher.
func planStatus(storePath string, bufferPoolBytes uint64) (title string, approaches []string, ok bool) {
	s, err := store.OpenWithBufferPool(storePath, bufferPoolBytes)
	if err != nil {
		return "", nil, false
	}
	defer s.Close()
	plans, err := plan.ActivePlans(s)
	if err != nil {
		return "", nil, false
	}
	if len(plans) == 0 {
		return "", nil, true
	}
	cur := plans[len(plans)-1]
	appr, err := plan.PlanApproaches(s, cur.ID)
	if err != nil {
		return cur.Title, nil, true
	}
	for _, a := range appr {
		if a.State == plan.ApproachRetired {
			continue
		}
		tasks, err := plan.ApproachTasks(s, a.ID)
		if err != nil {
			continue
		}
		done := 0
		for _, t := range tasks {
			if t.Qualified() {
				done++
			}
		}
		if a.State == plan.ApproachComplete && len(tasks) > 0 && done == len(tasks) {
			continue
		}
		text := fmt.Sprintf("%s %d/%d qualified", truncate(approachLabel(a.Title), 20), done, len(tasks))
		if a.State == plan.ApproachComplete {
			text += " (complete, unverified)"
		}
		approaches = append(approaches, text)
		if len(approaches) >= 2 {
			break
		}
	}
	return cur.Title, approaches, true
}
