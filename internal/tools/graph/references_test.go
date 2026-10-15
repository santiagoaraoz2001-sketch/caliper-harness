package graph

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"specific-harness/internal/config"
	api "specific-harness/internal/graph"
	"specific-harness/internal/memory"
	"specific-harness/internal/store"
)

func TestReferencePublicationExplicitAndConcurrent(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, limits := api.New(s, "test", t.TempDir()), config.Defaults().Tools.GraphQL
	if _, err := New(owner, limits, root, "ordinary-session", s.Identity()); err != nil {
		t.Fatal(err)
	}
	entries, err := memory.Knowledge(context.Background(), s, root, "", "", 10, limits.MaxResultBytes, true)
	if err != nil || len(entries) != 0 {
		t.Fatal("read-only tool construction published reference writes", entries, err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 6)
	for n := 0; n < cap(failures); n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			failures <- PublishReferences(context.Background(), owner, s, root)
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal("matching concurrent startup refused", err)
		}
	}
	versions, err := s.Query(`MATCH(m:MemoryEntry) WHERE m.project_root=$root AND m.key STARTS WITH 'harness/reference/' RETURN m.key AS key, count(m) AS versions`, map[string]any{"root": root})
	if err != nil || len(versions) != 5 {
		t.Fatal("missing actual reference versions", versions, err)
	}
	for _, v := range versions {
		if fmt.Sprint(v["versions"]) != "1" {
			t.Fatal("matching concurrent startups duplicated versions", versions)
		}
	}
	selected, err := memory.SelectedKnowledge(context.Background(), s, root, memory.KnowledgeSelection{SessionRef: "actual-active-session", TaskID: "actual-task", FilePath: "main.go"}, limits.MaxRows, limits.MaxResultBytes)
	if err != nil || len(selected) != 0 {
		t.Fatal("reference library entered automatic product bookkeeping selection", selected, err)
	}
	cfg := config.Defaults()
	recalled, err := memory.Query(s, "", "Plan Approach Task worker proof validation", cfg.Recall.TopN, cfg.Recall.MaxChars, cfg.Recall.MinWord)
	if err != nil || recalled != "" {
		t.Fatal("reference library entered global automatic recall", recalled, err)
	}
	foreign, err := memory.Knowledge(context.Background(), s, t.TempDir(), "harness/reference/index", "", 1, limits.MaxResultBytes, true)
	if err != nil || len(foreign) != 0 {
		t.Fatal("references escaped project scope", foreign, err)
	}
}

func TestReferencePublicationPreservesNamespaceCollision(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	limits := config.Defaults().Tools.GraphQL
	old, err := memory.PutKnowledge(context.Background(), s, root, "existing-author", "harness/reference/workers", "existing authored bytes", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	err = PublishReferences(context.Background(), api.New(s, "test", t.TempDir()), s, root)
	if err == nil || !strings.Contains(err.Error(), "namespace collision") || !strings.Contains(err.Error(), "workers") || !strings.Contains(err.Error(), "existing-author") {
		t.Fatal("namespace collision did not fail actionably", err)
	}
	actual, err := memory.Knowledge(context.Background(), s, root, "", "", 10, limits.MaxResultBytes, true)
	if err != nil || len(actual) != 1 || actual[0].ID != old.ID || actual[0].Body != "existing authored bytes" {
		t.Fatal("collision overwrote author or published partial library", actual, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := PublishReferences(ctx, api.New(s, "test", t.TempDir()), s, root); err == nil {
		t.Fatal("cancelled startup published references")
	}
}
