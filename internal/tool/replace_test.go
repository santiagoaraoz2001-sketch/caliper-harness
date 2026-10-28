package tool

import "testing"

func TestReplacementPreservesCompatibilityAndRoleRestrictions(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"bash", "find", "find_file"} {
		r.Register(Definition{Name: name})
	}
	limited := r.View([]string{"find_file"})
	r.Replace(Definition{Name: "bash", Desc: "owned lifetime"})
	if _, ok := r.Lookup("find"); !ok {
		t.Fatal("hidden compatibility registration lost")
	}
	if d, ok := limited.ResolveName("find"); !ok || d.Name != "find_file" {
		t.Fatal("compatible resolution lost")
	}
	if _, ok := limited.Lookup("bash"); ok {
		t.Fatal("replacement widened role")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("replacement added unregistered authority")
		}
	}()
	r.Replace(Definition{Name: "new-tool"})
}
