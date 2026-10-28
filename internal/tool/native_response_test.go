package tool

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"specific-harness/internal/llm"
	"strings"
	"testing"
)

func nativeResponseRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	r.Register(Definition{Name: "record", Params: []Param{
		{Name: "text", Type: TypeString, Required: true, AllowEmpty: true},
		{Name: "count", Type: TypeInt},
		{Name: "enabled", Type: TypeBool},
	}, Execute: func(context.Context, Args) (Result, error) {
		t.Fatal("parsing executed a tool")
		return Result{}, nil
	}})
	return r
}

func nativeRecord(value string) string {
	return "<tool_call>\n<function=record>\n<parameter=text>\n" + value + "\n</parameter>\n</function>\n</tool_call>"
}

func TestNativeResponseObservedUnicodeFixture(t *testing.T) {
	// Exact delivered raw text of native-raw-three-chunk-probe.json. The
	// runtime's emitted token IDs lost Unicode fragments; delivered UTF-8 did not.
	raw := "<tool_call>\n<function=record>\n<parameter=text>\ncafé 🚚\n</parameter>\n</function>\n</tool_call>"
	msg, err := nativeResponseRegistry(t).ParseNativeResponse(raw, "normal", "run-7")
	if err != nil || len(msg.ToolCalls) != 1 {
		t.Fatalf("parse: %+v %v", msg, err)
	}
	call := msg.ToolCalls[0]
	if call.ID != "run-7-0" || call.Name != "record" || msg.Role != "assistant" {
		t.Fatalf("identity: %+v", msg)
	}
	args, err := JSONObject(call.Arguments)
	if err != nil || args["text"] != "café 🚚" {
		t.Fatalf("UTF-8 argument: %#v %v", args, err)
	}
}

func TestNativeResponseArgumentLiteralsAndWhitespace(t *testing.T) {
	value := "\n  café 🚚  \nconst x = \"</parameter>\";\nconst y = \"<tool_call><function=record>\";\nconst z = \"</function></tool_call><think></think>\";\n\t"
	msg, err := nativeResponseRegistry(t).ParseNativeResponse(nativeRecord(value), "normal", "owner")
	if err != nil {
		t.Fatal(err)
	}
	args, _ := JSONObject(msg.ToolCalls[0].Arguments)
	if args["text"] != value {
		t.Fatalf("argument changed: got %q want %q", args["text"], value)
	}
}

func TestNativeResponseJSONPreservesBytesTypesAndMarkers(t *testing.T) {
	arguments := `{ "text": "café 🚚 </tool_call> <parameter=x> \\ \" ", "count":9007199254740993, "enabled":true }`
	raw := `<tool_call>{"name":"record","arguments":` + arguments + `}</tool_call>`
	msg, err := nativeResponseRegistry(t).ParseNativeResponse(raw, "normal", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if msg.ToolCalls[0].Arguments != arguments {
		t.Fatalf("JSON lexical bytes changed: %q", msg.ToolCalls[0].Arguments)
	}
	args, _ := JSONObject(msg.ToolCalls[0].Arguments)
	if args["count"] != json.Number("9007199254740993") || args["enabled"] != true {
		t.Fatalf("types changed: %#v", args)
	}
}

func TestNativeResponseReasoningTextAndMultipleCalls(t *testing.T) {
	r := nativeResponseRegistry(t)
	for _, tc := range []struct{ state, prefix string }{
		{"normal", "<think>plan 🚚</think>"},
		{"reasoning", "plan 🚚</think>"},
	} {
		raw := tc.prefix + "\nBefore " + nativeRecord("one") + "\n" + nativeRecord("two") + " after\n"
		msg, err := r.ParseNativeResponse(raw, tc.state, "phase")
		if err != nil {
			t.Fatal(err)
		}
		if msg.ReasoningContent != "plan 🚚" || msg.Content != "\nBefore \n after\n" || len(msg.ToolCalls) != 2 || msg.ToolCalls[1].ID != "phase-1" {
			t.Fatalf("projection: %+v", msg)
		}
	}
	msg, err := r.ParseNativeResponse("  ordinary <b>HTML</b> 🚚\n", "normal", "")
	if err != nil || msg.Content != "  ordinary <b>HTML</b> 🚚\n" {
		t.Fatalf("text: %+v %v", msg, err)
	}
}

func TestNativeResponseRejectsAmbiguousOrIncompleteAtomically(t *testing.T) {
	good := nativeRecord("first")
	cases := map[string]string{
		"missing tool close":           strings.TrimSuffix(nativeRecord("x"), "</tool_call>"),
		"missing function close":       strings.Replace(nativeRecord("x"), "</function>", "", 1),
		"missing parameter close":      strings.Replace(nativeRecord("x"), "</parameter>", "", 1),
		"unclosed JSON":                `<tool_call>{"name":"record","arguments":{"text":"x"}`,
		"duplicate envelope":           `<tool_call>{"name":"record","name":"record","arguments":{"text":"x"}}</tool_call>`,
		"duplicate JSON argument":      `<tool_call>{"name":"record","arguments":{"text":"x","text":"y"}}</tool_call>`,
		"duplicate XML argument":       `<tool_call><function=record><parameter=text>x</parameter><parameter=text>y</parameter></function></tool_call>`,
		"trailing JSON":                `<tool_call>{"name":"record","arguments":{"text":"x"}}{}</tool_call>`,
		"partial marker":               `<tool_call`,
		"stray close":                  `</tool_call>`,
		"reasoning open":               `<think>unfinished`,
		"repeated reasoning":           `<think></think><think>again</think>`,
		"structural literal ambiguity": nativeRecord(`"</parameter></function></tool_call>"`),
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			msg, err := nativeResponseRegistry(t).ParseNativeResponse(good+bad, "normal", "owner")
			if err == nil || !reflect.DeepEqual(msg, llm.Message{}) {
				t.Fatalf("partial effect admission: %+v %v", msg, err)
			}
		})
	}
}

func TestNativeResponseFramingEvidenceAndRoleAdmission(t *testing.T) {
	r := nativeResponseRegistry(t)
	draft := "<tool_call><function=record><parameter=text>unfinished 🚚"
	_, err := r.ParseNativeResponse(nativeRecord("first")+draft, "normal", "owner")
	var framing *llm.ToolFramingError
	if !errors.As(err, &framing) || framing.Draft != draft || framing.CallIndex != 1 || framing.Boundary != "stop" {
		t.Fatalf("lost original draft/index: %#v %v", framing, err)
	}
	for _, tc := range []struct{ raw, state, prefix string }{
		{string([]byte{0xff}), "normal", "owner"},
		{"text", "unknown", "owner"},
		{"unfinished", "reasoning", "owner"},
		{nativeRecord("x"), "normal", ""},
	} {
		if _, err := r.ParseNativeResponse(tc.raw, tc.state, tc.prefix); err == nil {
			t.Fatalf("invalid context admitted: %#v", tc)
		}
	}
}

func TestNativeResponseLeavesAdmissionToExistingDispatch(t *testing.T) {
	r := nativeResponseRegistry(t)
	for _, raw := range []string{
		strings.Replace(nativeRecord("x"), "function=record", "function=unknown", 1),
		strings.Replace(nativeRecord("x"), "function=record", "function=Record", 1),
		strings.Replace(nativeRecord("x"), "parameter=text", "parameter=unknown", 1),
		`<tool_call>{"name":"record","arguments":{"text":false}}</tool_call>`,
		`<tool_call>{"name":"record","arguments":{}}</tool_call>`,
		`<tool_call>{"name":"record","arguments":{"text":{"nested":"wrong shape"}}}</tool_call>`,
		`<tool_call><function=record><parameter=text>x</parameter><parameter=count>not-a-number</parameter></function></tool_call>`,
	} {
		msg, err := r.ParseNativeResponse(raw, "normal", "owner")
		if err != nil || len(msg.ToolCalls) != 1 {
			t.Fatalf("lost correlated refusal: %+v %v", msg, err)
		}
		call := msg.ToolCalls[0]
		if def, found := r.Lookup(call.Name); found && def.Name == call.Name {
			if _, gap := ValidateJSON(def, call.Arguments); gap == "" {
				t.Fatalf("test expected downstream refusal: %+v", call)
			}
		}
	}
	msg, err := r.View([]string{}).ParseNativeResponse(nativeRecord("x"), "normal", "owner")
	if err != nil || len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "record" {
		t.Fatalf("role admission was guessed: %+v %v", msg, err)
	}
}
