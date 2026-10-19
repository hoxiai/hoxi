package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestSessionEditedFileSetCachesOpenCodeChildTranscripts(t *testing.T) {
	ws := t.TempDir()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/session/ses_child/message" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]MessageTurn{{Parts: []MessagePart{{
			ID: "prt_edit", Type: "tool", Tool: "edit",
			State: &ToolState{Status: "completed", Input: map[string]interface{}{"filePath": filepath.Join(ws, "child.go")}},
		}}}})
	}))
	defer srv.Close()

	svc := NewService(ws, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.ConfigureOpenCode(srv.URL, "pw")
	if err := svc.store.Put(SessionRecord{ID: "ses_root", ProjectDir: ws, Turns: []MessageTurn{{Parts: []MessagePart{
		{ID: "prt_task", Type: "tool", Tool: "task", State: &ToolState{Status: "completed", Output: "session_id: ses_child"}},
		{ID: "prt_missing", Type: "tool", Tool: "task", State: &ToolState{Status: "running", Input: map[string]interface{}{"task_id": "ses_gone"}}},
	}}}}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		edited := svc.sessionEditedFileSet("ses_root")
		if _, ok := edited["child.go"]; !ok {
			t.Fatalf("child edit missing: %v", edited)
		}
	}
	// One read per child (including the missing one), not one per diff event.
	if got := hits.Load(); got != 2 {
		t.Fatalf("expected 2 OpenCode reads, got %d", got)
	}
}
