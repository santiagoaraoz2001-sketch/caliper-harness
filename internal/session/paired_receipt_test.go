package session

import (
	"encoding/json"
	"testing"
)

func TestLegacyCheckReadableWithoutNewQualification(t *testing.T) {
	var c CodingState
	raw := `{"task_id":"legacy","workspace":"/owned","command":"suite","revision":1,"receipt":{"command":"suite","cwd":"/owned","revision":1,"started":"start","ended":"end","status":"passed","exit_code":0}}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Passed() {
		t.Fatal("legacy receipt retroclaimed as paired proof")
	}
	c.Receipt.ExitCode = nil
	if c.Validate() == nil {
		t.Fatal("legacy validation weakened")
	}
}
