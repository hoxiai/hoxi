package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionEditedFileSetCachesOpenCodeChildTranscripts(t *testing.T) {
	ws := t.TempDir()
	var hits atomic.Int32
	var failChild atomic.Bool
	var allowGone atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/session/ses_child/message":
			if failChild.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode([]MessageTurn{{Parts: []MessagePart{{
				ID: "prt_edit", Type: "tool", Tool: "edit",
				State: &ToolState{Status: "completed", Input: map[string]interface{}{"filePath": filepath.Join(ws, "child.go")}},
			}}}})
		case "/session/ses_gone/message":
			if !allowGone.Load() {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode([]MessageTurn{{Parts: []MessagePart{{
				ID: "prt_edit_2", Type: "tool", Tool: "write",
				State: &ToolState{Status: "completed", Input: map[string]interface{}{"filePath": filepath.Join(ws, "recovered.go")}},
			}}}})
		default:
			http.NotFound(w, r)
		}
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

	// Different directory key must not collide with the cached entry for ws.
	otherDir := t.TempDir()
	_ = svc.openCodeChildTurns("ses_child", otherDir)
	if got := hits.Load(); got != 3 {
		t.Fatalf("expected distinct dir key to trigger read, got %d reads", got)
	}

	// Negative cache expires after childTurnsErrTTL (250ms) and recovers when ses_gone becomes available.
	allowGone.Store(true)
	time.Sleep(childTurnsErrTTL + 40*time.Millisecond)
	edited := svc.sessionEditedFileSet("ses_root")
	if _, ok := edited["recovered.go"]; !ok {
		t.Fatalf("expected negative cache to recover after childTurnsErrTTL, got %v", edited)
	}

	// Stale-if-error: if ses_child's positive cache expires and the refresh fails transiently,
	// previously discovered turns are preserved instead of dropping child.go from the shelf.
	key := childTurnsKey{dir: filepath.Clean(ws), sid: "ses_child"}
	if v, ok := svc.childTurns.Load(key); ok {
		entry := v.(childTurnsEntry)
		entry.expires = time.Now().Add(-time.Second)
		svc.childTurns.Store(key, entry)
	}
	failChild.Store(true)
	editedAfterFail := svc.sessionEditedFileSet("ses_root")
	if _, ok := editedAfterFail["child.go"]; !ok {
		t.Fatalf("expected stale child.go to be preserved on transient error, got %v", editedAfterFail)
	}
}
