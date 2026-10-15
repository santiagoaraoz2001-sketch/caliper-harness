package tool

import "testing"

// Captured from the 2026-10-15 native Laguna rerun: the model wrapped plan
// arguments in a GraphQL-style {"input": {...}} object. The wrapper is an
// unambiguous respelling and must splice; other nested values stay refused.
func TestJSONObjectUnwrapsInputWrapper(t *testing.T) {
	got, err := JSONObject(`{"input": {}}`)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty input wrapper not unwrapped: %v %v", got, err)
	}
	got, err = JSONObject(`{"input": {"invoke": "EXECUTE"}}`)
	if err != nil || got["invoke"] != "EXECUTE" || len(got) != 1 {
		t.Fatalf("input wrapper not spliced: %v %v", got, err)
	}
	if _, err := JSONObject(`{"arguments": {"a": 1}}`); err == nil {
		t.Fatal("non-input nested object was accepted")
	}
	if _, err := JSONObject(`{"input": {"a": {"b": 1}}}`); err == nil {
		t.Fatal("doubly nested value was accepted")
	}
	if _, err := JSONObject(`{"a": 1, "input": {"a": 2}}`); err == nil {
		t.Fatal("spliced duplicate argument was accepted")
	}
}
