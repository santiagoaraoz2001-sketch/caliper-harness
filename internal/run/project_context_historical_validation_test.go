package run

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"specific-harness/internal/checkreport"
	"specific-harness/internal/config"
	"specific-harness/internal/llm"
	"specific-harness/internal/memory"
	"specific-harness/internal/plan"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

// Nonclosed persisted coding provenance is not an active task or reviewer.
// Exercise the actual ordinary provider request, not the context hook alone.
func TestOrdinaryRequestDoesNotSelectHistoricalTaskKnowledge(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	var err error
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := plan.WithProject(t.Context(), root)
	p, err := plan.CreatePlanContext(ctx, s, "historical", "earlier task")
	if err != nil {
		t.Fatal(err)
	}
	a, err := plan.CreateApproach(s, p.ID, "earlier approach")
	if err != nil {
		t.Fatal(err)
	}
	task, err := plan.CreateTask(s, a.ID, "earlier task", "earlier objective")
	if err != nil {
		t.Fatal(err)
	}
	note, err := memory.PutKnowledge(ctx, s, root, "historical-author", "project/features/earlier", "HISTORICAL_TASK_NOTE_NOT_CURRENT", "", task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan []llm.Message, 1)
	checkSchemas := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []llm.Message
			Tools    []llm.ToolDefinition
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		requests <- body.Messages
		for _, definition := range body.Tools {
			if definition.Function.Name == "check" {
				checkSchemas <- definition.Function.Description
			}
		}
		http.Error(w, "capture only", 503)
	}))
	defer server.Close()
	cfg := config.Defaults()
	cfg.Model.Runtime, cfg.Model.ToolProtocol, cfg.Recall.TopN = "openai", "structured", 0
	cfg.Coding.ReportFormat, cfg.Coding.ExpectedTests = checkreport.StageFormat, []string{"current-visible-consumer"}
	historicalReport := checkreport.Contract{Format: checkreport.StageFormat, Expected: []string{"historical-scaffold-only"}}
	history := session.Continuation{Version: 1, Entries: []session.Entry{{Kind: "intent", Message: llm.Message{Role: "user", Content: "Earlier task objective"}}}, Coding: &session.CodingState{TaskID: task.ID, Workspace: root, Attempt: 1, Revision: 7, Report: historicalReport}}
	r, _, err := New(Deps{Cfg: cfg, Store: s, Server: server.URL, Model: "capture", MaxSteps: 1, Window: 32768, Continuation: history}, "Explain the available commands; do not resume earlier work.")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = r.Run(ctx, "Explain the available commands; do not resume earlier work.")
	if err == nil || len(requests) != 1 {
		t.Fatalf("capture failed: %v requests=%d", err, len(requests))
	}
	messages := <-requests
	if len(checkSchemas) != 1 {
		t.Fatal("ordinary request did not advertise one check schema")
	}
	description := <-checkSchemas
	if !strings.Contains(description, "current-visible-consumer") || strings.Contains(description, "historical-scaffold-only") {
		t.Errorf("ordinary request advertised historical task check requirements instead of current configuration: %s", description)
	}
	for _, message := range messages {
		if strings.HasPrefix(message.Content, projectContextMarker) && (strings.Contains(message.Content, note.ID) || strings.Contains(message.Content, note.Body)) {
			t.Fatal("ordinary historical inspection selected previous task knowledge as current")
		}
	}
	if r.Continuation.Assignment != nil || r.Continuation.Coding == nil || r.Continuation.Coding.TaskID != task.ID || r.Continuation.Coding.Revision != 7 || !r.Continuation.Coding.Report.Equal(historicalReport) {
		t.Fatal("context selection changed preserved provenance or minted task authority")
	}
}
