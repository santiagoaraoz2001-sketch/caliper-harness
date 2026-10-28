package decision

import "fmt"

// Preflight checks limits known without provider inference. It does not certify
// token fit, model identity or a verdict; Judge still owns those checks. System
// One has a hard encoded-body limit, unlike the other configured protocols.
func (c *Client) Preflight(r Request) error {
	if c.disabled {
		return ErrDisabled
	}
	if r.MaxBytes < 0 {
		return fmt.Errorf("judge request byte limit must not be negative")
	}
	if r.Instructions == "" {
		r.Instructions = c.Prompts.Text("decision.default.instructions")
	}
	if c.expected != nil && c.expected.Runtime == "ollama" && c.expected.Protocol == "system_one" {
		_, err := c.systemOneRequest(r)
		return err
	}
	return nil
}
