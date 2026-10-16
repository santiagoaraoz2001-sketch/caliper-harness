package llm

import "encoding/json"

// ToolFramingError preserves a rejected native tool draft at a settled call
// boundary. It is evidence for repair, not an executable call. The client only
// returns it after a clean transfer; partial/length/cancelled replies keep their
// ordinary errors and must not enter reconstruction.
type ToolFramingError struct {
	Draft     string `json:"draft"`
	Reason    string `json:"reason"`
	Boundary  string `json:"boundary"`
	CallIndex int    `json:"call_index"`
}

func (e *ToolFramingError) Error() string { return "native tool framing failed: " + e.Reason }

func toolFramingError(raw json.RawMessage) *ToolFramingError {
	var wire struct {
		Message string            `json:"message"`
		Draft   string            `json:"native_tool_draft"`
		Failure *ToolFramingError `json:"native_tool_failure"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Failure == nil {
		return nil
	}
	e := wire.Failure
	if e.Draft == "" || e.Reason == "" || e.Draft != wire.Draft || e.Reason != wire.Message || e.CallIndex < 0 ||
		(e.Boundary != "tool_close" && e.Boundary != "stop") {
		return nil
	}
	return e
}
