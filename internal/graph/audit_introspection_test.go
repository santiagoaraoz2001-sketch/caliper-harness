package graph

import (
	"path/filepath"
	"specific-harness/internal/store"
	"strings"
	"testing"
)

func TestModelIntrospectionCannotAdvertiseForbiddenRoots(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	host := New(s, "test", t.TempDir())
	model, err := host.Scoped(t.TempDir(), "audit", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`{__type(name:"Query"){fields{name}}}`, `{x:__schema{queryType{fields{name}}}}`, `query{...F} fragment F on Query {x:__type(name:"Query"){fields{name}}}`} {
		if _, err := model.Exec(t.Context(), query, "", nil); err == nil || !strings.Contains(err.Error(), "schema.graphql") {
			t.Fatalf("model introspection not redirected: %v", err)
		}
		if _, err := host.Exec(t.Context(), query, "", nil); err != nil {
			t.Fatalf("host introspection broken: %v", err)
		}
	}
	if _, err := model.Exec(t.Context(), `{__typename version}`, "", nil); err != nil {
		t.Fatal(err)
	}
	if got := ModelReference(`{project{id}}`, ""); got != "schema.graphql" {
		t.Fatalf("unrelated root hint: %s", got)
	}
}
