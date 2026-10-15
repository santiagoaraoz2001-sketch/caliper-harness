package hw

import "testing"

func TestResolveWindowExplicitWins(t *testing.T) {
	if w, err := ResolveWindow(4096); w != 4096 || err != nil {
		t.Errorf("got %d,%v", w, err)
	}
}
