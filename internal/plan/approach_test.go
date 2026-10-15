package plan

import (
	"errors"
	"testing"
)

func TestApproachPlan(t *testing.T) {
	s := open(t)
	p, err := CreatePlan(s, "p", "g")
	if err != nil {
		t.Fatal(err)
	}
	a, err := CreateApproach(s, p.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ApproachPlan(s, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID || got.Goal != "g" {
		t.Errorf("got %+v", got)
	}
	if _, err := ApproachPlan(s, "absent"); !errors.Is(err, ErrNotFound) {
		t.Errorf("absent err=%v", err)
	}
}
