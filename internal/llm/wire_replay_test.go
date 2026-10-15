package llm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Historical real Mac response bytes exercise protocol compatibility without
// contacting a model. This is not fresh inference or a capability qualification.
func TestCapturedBackendTransport(t *testing.T) {
	var fixtures []struct {
		Fixture     string `json:"fixture"`
		SHA256      string `json:"sha256"`
		ReplySHA256 string `json:"reply_sha256"`
		Stream      bool   `json:"stream"`
		ServedModel string `json:"served_model"`
		In          int    `json:"usage_in"`
		Out         int    `json:"usage_out"`
		Callbacks   int    `json:"nonempty_callbacks"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		t.Run(f.Fixture, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", f.Fixture))
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", sha256.Sum256(raw)) != f.SHA256 {
				t.Fatal("historical fixture bytes drifted")
			}
			c, _ := stubClient(t, 200, string(raw))
			var reply, served string
			var u Usage
			var observed strings.Builder
			callbacks := 0
			if f.Stream {
				reply, served, u, err = c.ChatStream(context.Background(), nil, func(s string) { observed.WriteString(s); callbacks++ })
			} else {
				reply, served, u, err = c.Chat(context.Background(), nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", sha256.Sum256([]byte(reply))) != f.ReplySHA256 || served != f.ServedModel || u != (Usage{In: f.In, Out: f.Out, Reported: true}) {
				t.Fatalf("captured response changed: served=%q usage=%+v", served, u)
			}
			if f.Stream && (observed.String() != reply || callbacks != f.Callbacks) {
				t.Fatalf("delta evidence differs: callbacks=%d want=%d", callbacks, f.Callbacks)
			}
		})
	}
}
