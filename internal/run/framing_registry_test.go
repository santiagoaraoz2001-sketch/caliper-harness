package run

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

// Exercise the actual full registry without invoking any tool. Existing owner
// examples supply argument values; missing examples remain missing rather than
// inventing commands, paths, IDs or authored work to make a candidate succeed.
func TestFramingReconstructionActualRegistry(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Defaults()
	cfg.Recall.TopN = 0
	r, _, err := New(Deps{Tools: "full", Cfg: cfg, Store: openAssignedStore(t, dir), Model: "component", Server: "http://127.0.0.1:1", Window: 32768}, "registry reconstruction coverage")
	if err != nil {
		t.Fatal(err)
	}
	names := r.Registry.Names()
	if len(names) < 20 {
		t.Fatalf("expected full registry, got %v", names)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			d, _ := r.Registry.Lookup(name)
			// Synthetic authored test values exercise typed reconstruction for
			// every real definition. They are never bound or executed and do
			// not establish semantic validity of paths, commands or node IDs.
			typed := map[string]string{}
			for _, p := range d.Params {
				switch p.Type {
				case tool.TypeString:
					typed[p.Name] = "  authored fixture\r\nsecond line\n"
				case tool.TypeInt:
					typed[p.Name] = "1"
				case tool.TypeFloat:
					typed[p.Name] = "0.25"
				case tool.TypeBool:
					typed[p.Name] = "true"
				default:
					t.Fatalf("reconstruction does not cover registered parameter %s of type %q", p.Name, p.Type)
				}
				if len(p.Enum) > 0 {
					typed[p.Name] = p.Enum[0]
				}
			}
			wantTyped, typedGap := tool.Validate(d, typed)
			if typedGap != "" {
				t.Fatalf("synthetic authored fixture invalid: %s", typedGap)
			}
			_, gotTyped, _, typedOK := r.Registry.ReconstructCall(framingRegistryDraft(name, typed))
			if !typedOK || !reflect.DeepEqual(gotTyped, wantTyped) {
				t.Fatalf("typed reconstruction changed authored fixture: got %#v want %#v ok %v", gotTyped, wantTyped, typedOK)
			}
			slots := map[string]string{}
			for k, v := range d.Example {
				slots[k] = fmt.Sprint(v)
			}
			want, gap := tool.Validate(d, slots)
			gotDef, got, _, ok := r.Registry.ReconstructCall(framingRegistryDraft(name, slots))
			if gap != "" {
				if ok {
					t.Fatalf("reconstruction fabricated missing/invalid example arguments: %v", got)
				}
				t.Log("owner example incomplete or invalid: reconstruction correctly refuses")
				return
			}
			if !ok || gotDef.Name != name || !reflect.DeepEqual(got, want) {
				t.Fatalf("reconstruction differs from current schema: got %v ok %v; want %v", got, ok, want)
			}
		})
	}
}

func framingRegistryDraft(name string, slots map[string]string) string {
	var keys []string
	for key := range slots {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var raw strings.Builder
	raw.WriteString("<tool_call>" + name + ">")
	for _, key := range keys {
		raw.WriteString("<arg_key>" + key + "</arg_key><arg_value>" + slots[key] + "</arg_value>")
	}
	raw.WriteString("</tool_call>")
	return raw.String()
}
