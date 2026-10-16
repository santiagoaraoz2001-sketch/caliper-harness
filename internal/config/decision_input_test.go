package config

import (
	"strings"
	"testing"
)

func TestDecisionInputBudgetDefaultAndValidation(t *testing.T) {
	c := Defaults()
	if c.Decision.MaxInputTokens != 15999 {
		t.Fatal(c.Decision.MaxInputTokens)
	}
	c.Decision.MaxInputTokens = 0
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "decision.max_input_tokens") {
		t.Fatal(err)
	}
	c.Decision.MaxInputTokens = 12000
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}
