package update

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSplitArchiveInstallAndPartFailure(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			data := fixture(t)
			name := "caliper-0.1.3-" + testPlatform + ".tar.gz"
			mid := len(data) / 2
			parts := [][]byte{data[:mid], data[mid:]}
			manifest := ArchiveParts{Format: 1, Archive: name, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
			for i, p := range parts {
				manifest.Parts = append(manifest.Parts, ArchivePart{Name: fmt.Sprintf("%s.part-%03d", name, i), Bytes: int64(len(p)), SHA256: fmt.Sprintf("%x", sha256.Sum256(p))})
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/sum":
					fmt.Fprint(w, manifest.SHA256)
				case "/manifest":
					json.NewEncoder(w).Encode(manifest)
				default:
					for i, p := range parts {
						if r.URL.Path == "/"+manifest.Parts[i].Name {
							if corrupt && i == 1 {
								w.Write([]byte("broken"))
							} else {
								w.Write(p)
							}
							return
						}
					}
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			m := Manager{Home: t.TempDir(), Platform: testPlatform, Client: srv.Client()}
			r := Release{Version: "0.1.3", PartsURL: srv.URL + "/manifest", ChecksumURL: srv.URL + "/sum"}
			_, err := m.Stage(context.Background(), r)
			if corrupt {
				if err == nil {
					t.Fatal("accepted corrupt transport part")
				}
				if _, e := os.Stat(filepath.Join(m.Home, "current.json")); !os.IsNotExist(e) {
					t.Fatal("activated incomplete parts")
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
