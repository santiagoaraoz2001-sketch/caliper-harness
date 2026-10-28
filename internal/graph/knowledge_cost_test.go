package graph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/store"
	"strings"
	"testing"
	"time"
)

// Run explicitly with -run '^$' -bench BenchmarkKnowledgeCost -benchtime=20x.
// Each graph contains current notes; no model or network is involved.
func BenchmarkKnowledgeCost(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			s, err := store.Open(filepath.Join(b.TempDir(), "db"))
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			root := b.TempDir()
			a, err := New(s, "bench", b.TempDir()).Scoped(root, "bench", true)
			if err != nil {
				b.Fatal(err)
			}
			root = a.scope.root
			body := "## state\nOwns velocity and thrust.\n" + strings.Repeat("Observed implementation detail. ", 24)
			if err := s.Write(`UNWIND range(1,$count) AS i CREATE(m:MemoryEntry {id:'bench-'+CAST(i AS STRING),kind:'note',body:$body,created_at:$now,session_ref:'bench',project_root:$root,key:'project/item/'+CAST(i AS STRING),previous_id:'',task_id:'',file_path:'',file_sha256:''})`, map[string]any{"count": int64(size), "body": body, "now": time.Now().UTC(), "root": root}); err != nil {
				b.Fatal(err)
			}
			var target strings.Builder
			refs := []memory.Reference{}
			for i := 0; i < 32; i++ {
				fmt.Fprintf(&target, "## section-%d\nEvidence for section %d.\n", i, i)
				refs = append(refs, memory.Reference{Key: "project/target", Relation: "explains", Section: fmt.Sprintf("section-%d", i)})
			}
			if _, err := memory.PutKnowledgeReferences(context.Background(), s, root, "bench", "project/target", target.String(), "", "", "", nil); err != nil {
				b.Fatal(err)
			}
			if _, err := memory.PutKnowledgeReferences(context.Background(), s, root, "bench", "project/links", "Links", "", "", "", refs); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "owner.go"), []byte(strings.Repeat("// code\n", 512)), 0600); err != nil {
				b.Fatal(err)
			}
			if _, err := memory.PutKnowledgeReferences(context.Background(), s, root, "bench", "project/file", "Owner", "", "", "owner.go", nil); err != nil {
				b.Fatal(err)
			}
			queries := []struct{ name, q string }{
				{"key", `{knowledge(key:"project/item/1"){key body}}`},
				{"keyword", `{knowledge(text:"thrust",limit:3){key sections{id body}}}`},
				{"refs_keys", `{knowledge(key:"project/links"){references{key section}}}`},
				{"refs_status", `{knowledge(key:"project/links"){references{key section status}}}`},
				{"freshness", `{knowledge(key:"project/file"){freshness}}`},
			}
			for _, query := range queries {
				b.Run(query.name, func(b *testing.B) {
					var output []byte
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						output, err = a.ExecBounded(context.Background(), query.q, "", nil, config.Defaults().Tools.GraphQL)
						if err != nil {
							b.Fatal(err)
						}
					}
					b.ReportMetric(float64(len(output)), "output_bytes")
				})
			}
		})
	}
}
