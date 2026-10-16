package tool

import (
	"reflect"
	"testing"
)

func TestReconstructCapturedOpeningFailures(t *testing.T) {
	for _, name := range []string{"plan", "custom_entry"} {
		r := NewRegistry()
		r.Register(Definition{Name: name, Opening: true})
		for _, suffix := range []string{
			"><arg_key></arg_key><arg_value>{}</arg_value>",
			"<arg_key>argument_keys></arg_key><arg_value>{}</arg_value><arg_key>argument_values>",
		} {
			d, args, reason, ok := r.ReconstructCall(name + suffix)
			if !ok || d.Name != name || len(args) != 0 || reason == "" {
				t.Fatalf("captured form refused: %s %v %v %s", name+suffix, ok, args, reason)
			}
		}
	}
}

func TestReconstructUsesSchemaAndPreservesValues(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Name: "custom", Params: []Param{
		{Name: "source", Type: TypeString, Required: true},
		{Name: "count", Type: TypeInt, Required: true},
		{Name: "ratio", Type: TypeFloat, Required: true},
		{Name: "flag", Type: TypeBool, Required: true},
	}})
	source := "  first\r\nsecond\n"
	raw := "<tool_call>custom><arg_key>source</arg_key><arg_value>" + source + "</arg_value><arg_key>count</arg_key><arg_value>2</arg_value><arg_key>ratio</arg_key><arg_value>0.25</arg_value><arg_key>flag</arg_key><arg_value>true</arg_value></tool_call>"
	_, args, _, ok := r.ReconstructCall(raw)
	want := Args{"source": source, "count": int64(2), "ratio": float64(.25), "flag": true}
	if !ok || !reflect.DeepEqual(args, want) {
		t.Fatalf("got %#v, ok %v, want %#v", args, ok, want)
	}
	r.Register(Definition{Name: "bash", Params: []Param{{Name: "cmd", Type: TypeString, Required: true}}})
	_, args, _, ok = r.ReconstructCall("bash><arg_key>command</arg_key><arg_value>printf 'a b'</arg_value>")
	if !ok || args["cmd"] != "printf 'a b'" {
		t.Fatalf("existing alias lost: %v %v", args, ok)
	}
}

func TestReconstructRejectsAmbiguousIncompleteOrUnavailableCalls(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Name: "plan", Opening: true})
	r.Register(Definition{Name: "effect"})
	r.Register(Definition{Name: "bash", Params: []Param{{Name: "cmd", Type: TypeString, Required: true}}})
	r.Register(Definition{Name: "read", Params: []Param{{Name: "path", Type: TypeString, Required: true}}})
	for _, raw := range []string{
		"plan", "Please call plan", "<tool_call>plan", "missing><arg_key></arg_key><arg_value>{}</arg_value>",
		"effect><arg_key></arg_key><arg_value>{}</arg_value>",
		"plan><arg_key></arg_key><arg_value>{\"cmd\":\"rm x\"}</arg_value>",
		"plan><arg_key>objective</arg_key><arg_value>authored content</arg_value>",
		"bash><arg_key>cmd</arg_key><arg_value>echo unfinished",
		"bash><arg_key>cmd</arg_key><arg_value>echo x</arg_value><arg_key>cmd</arg_key><arg_value>echo x</arg_value>",
		"bash><arg_key>cmd</arg_key><arg_value>echo x</arg_value><arg_key>command</arg_key><arg_value>echo x</arg_value>",
		"read><arg_key></arg_key><arg_value>{}</arg_value>",
		"read><arg_key>path</arg_key><arg_value>x</arg_value>read><arg_key>path</arg_key><arg_value>y</arg_value>",
		"<tool_call>plan</tool_call><tool_call>effect</tool_call>",
		"bash><arg_key>cmd</arg_key><arg_value>echo <arg_value>x</arg_value>",
		"bash><arg_key>cmd</arg_key><arg_value>echo x</arg_value>junk",
	} {
		if _, _, _, ok := r.ReconstructCall(raw); ok {
			t.Fatalf("unsafe reconstruction accepted: %s", raw)
		}
	}
	if _, _, _, ok := r.View([]string{"read"}).ReconstructCall("bash><arg_key>cmd</arg_key><arg_value>echo x</arg_value>"); ok {
		t.Fatal("hidden tool was reconstructed")
	}
}
