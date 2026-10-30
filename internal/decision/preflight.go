package decision



// Preflight checks limits known without provider inference. It does not certify
// token fit, model identity or a verdict; Judge still owns those checks. The
// System One provider endpoint refuses text+schema over 64 KiB; other
// protocols admit by provider token limits only.
func (c *Client) Preflight(r Request) error {
	if c.disabled {
		return ErrDisabled
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
