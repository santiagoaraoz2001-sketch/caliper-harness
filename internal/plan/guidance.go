package plan

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GuidanceRef pins a section in the existing authored owner document. It is
// context selection, never permission, task assignment or evidence of a check.
type GuidanceRef struct {
	Key     string `json:"key"`
	Version string `json:"version"`
	Section string `json:"section"`
}

// GuidanceSelection distinguishes inheritance (nil) from explicit clearing
// ([]). Legacy prose has no selection; a malformed declared member is an error.
func GuidanceSelection(raw string) (*[]GuidanceRef, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return nil, nil
	}
	member, exists := fields["guidance"]
	if !exists {
		return nil, nil
	}
	var refs []GuidanceRef
	if string(member) == "null" || json.Unmarshal(member, &refs) != nil {
		return nil, fmt.Errorf("guidance must be an array of key/version/section references (empty clears selection)")
	}
	seen := map[GuidanceRef]bool{}
	for _, ref := range refs {
		if strings.TrimSpace(ref.Key) == "" || strings.TrimSpace(ref.Version) == "" || strings.TrimSpace(ref.Section) == "" || seen[ref] {
			return nil, fmt.Errorf("guidance needs distinct, complete key/version/section references")
		}
		seen[ref] = true
	}
	return &refs, nil
}

// EffectiveGuidance takes owner documents from most specific to least specific.
func EffectiveGuidance(documents ...string) ([]GuidanceRef, error) {
	for _, raw := range documents {
		refs, err := GuidanceSelection(raw)
		if err != nil {
			return nil, err
		}
		if refs != nil {
			return *refs, nil
		}
	}
	return nil, nil
}
