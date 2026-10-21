package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/statusbar"
)

type streamReadyWriter struct {
	*httptest.ResponseRecorder
	onFlush func()
}

func (w *streamReadyWriter) Flush() {
	w.ResponseRecorder.Flush()
	if w.onFlush != nil {
		fn := w.onFlush
		w.onFlush = nil
		fn()
	}
}

func TestSSESubscribesBeforeHeadersAreFlushed(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "store.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &streamReadyWriter{ResponseRecorder: httptest.NewRecorder()}
	w.onFlush = func() {
		svc.mu.Lock()
		count := len(svc.subs)
		svc.mu.Unlock()
		if count != 1 {
			cancel()
			t.Fatalf("SSE headers sent before subscriber registered: %d subscribers", count)
		}
		svc.broadcast("ses_agy_test", SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": "ses_agy_test"}})
		w.onFlush = cancel
	}
	svc.serveSSE(w, httptest.NewRequest("GET", "/api/hoxi/runner/event", nil).WithContext(ctx), "")
	if !strings.Contains(w.Body.String(), `"type":"session.status"`) {
		t.Fatalf("first event was lost: %s", w.Body.String())
	}
	if len(svc.subs) != 0 {
		t.Fatal("SSE subscription was not cleaned up")
	}
}

func TestUnifiedSSEStreamMergesRunnerAndFilteredOpenCodeEvents(t *testing.T) {
	root := t.TempDir()
	tracker := statusbar.NewTracker()
	svc := NewService(root, nil, tracker, filepath.Join(t.TempDir(), "store.json"))
	rec := SessionRecord{
		ID:           "ses_hoxi_123",
		Runtime:      "opencode",
		ExternalID:   "ses_managed_engine",
		ProjectDir:   root,
		WorktreePath: root,
		SandboxMode:  "local",
		OpenCodeIDs:  []string{"ses_managed_engine"},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/global/event" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		// 1. Leaked engine handle event (must be filtered out)
		fmt.Fprintf(w, "data: {\"directory\":%q,\"payload\":{\"type\":\"message.part.delta\",\"properties\":{\"sessionID\":\"ses_managed_engine\",\"delta\":\"secret\"}}}\n\n", root)
		// 2. Tagged metadata handle event (must be filtered out)
		fmt.Fprintf(w, "data: {\"directory\":%q,\"payload\":{\"type\":\"session.created\",\"properties\":{\"info\":{\"id\":\"ses_tagged\",\"metadata\":{\"hoxiConversation\":\"ses_hoxi_123\"}}}}}\n\n", root)
		// 3. Unmanaged OpenCode event (must pass through and update statusbar tracker)
		fmt.Fprintf(w, "data: {\"directory\":%q,\"payload\":{\"type\":\"session.status\",\"properties\":{\"sessionID\":\"ses_unmanaged_oc\",\"status\":{\"type\":\"busy\"}}}}\n\n", root)
		flusher.Flush()
	}))
	defer upstream.Close()
	svc.ConfigureOpenCode(upstream.URL, "secret")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &streamReadyWriter{ResponseRecorder: httptest.NewRecorder()}
	checkDone := func() {
		body := w.Body.String()
		if strings.Contains(body, `"ses_unmanaged_oc"`) && strings.Contains(body, `"ses_hoxi_123"`) {
			cancel()
		}
	}
	w.onFlush = func() {
		svc.broadcast("ses_hoxi_123", SSEEvent{
			Type:       "message.part.delta",
			Properties: map[string]interface{}{"sessionID": "ses_hoxi_123", "delta": "from-runner"},
		})
		var step func()
		step = func() {
			checkDone()
			if ctx.Err() == nil {
				w.onFlush = step
			}
		}
		w.onFlush = step
		checkDone()
	}

	svc.handleUnifiedSSE(w, httptest.NewRequest("GET", "/api/hoxi/event", nil).WithContext(ctx))
	body := w.Body.String()
	if strings.Contains(body, "ses_managed_engine") || strings.Contains(body, "ses_tagged") {
		t.Fatalf("managed OpenCode handle leaked into unified SSE stream: %s", body)
	}
	if !strings.Contains(body, "ses_unmanaged_oc") || !strings.Contains(body, "from-runner") {
		t.Fatalf("missing expected merged events in unified SSE stream: %s", body)
	}
	if tracker.Snapshot().Running != 1 {
		t.Fatalf("expected statusbar tracker to record busy session from unified SSE, got %+v", tracker.Snapshot())
	}
}

func TestStatusSnapshotListsBusySessionsAcrossProjects(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "store.json"))
	other := t.TempDir()

	svc.mu.Lock()
	svc.procs["ses_agy_alpha"] = &activeProcess{sessionID: "ses_agy_alpha", done: make(chan struct{})}
	svc.childBusy["ses_cld_child"] = true
	svc.mu.Unlock()

	frame := svc.statusSnapshotFrame()
	if !strings.Contains(frame, statusSnapshotEventType) || !strings.Contains(frame, statusSnapshotScope) {
		t.Fatalf("snapshot frame missing type/scope: %s", frame)
	}
	for _, want := range []string{"ses_agy_alpha", "ses_cld_child"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("snapshot omitted busy session %s: %s", want, frame)
		}
	}
	if strings.Contains(frame, other) || strings.Contains(frame, root) {
		t.Fatalf("snapshot leaked project directories: %s", frame)
	}

	svc.mu.Lock()
	delete(svc.procs, "ses_agy_alpha")
	delete(svc.childBusy, "ses_cld_child")
	svc.mu.Unlock()
	if frame := svc.statusSnapshotFrame(); !strings.Contains(frame, `"busy":[]`) {
		t.Fatalf("idle snapshot must send an empty authoritative set: %s", frame)
	}
}

func TestUnifiedSSEStartsWithStatusSnapshot(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "store.json"))
	svc.mu.Lock()
	svc.procs["ses_qdr_running"] = &activeProcess{sessionID: "ses_qdr_running", done: make(chan struct{})}
	svc.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &streamReadyWriter{ResponseRecorder: httptest.NewRecorder()}
	w.onFlush = func() {
		if strings.Contains(w.Body.String(), "ses_qdr_running") {
			cancel()
			return
		}
		w.onFlush = func() { cancel() }
	}
	svc.handleUnifiedSSE(w, httptest.NewRequest("GET", "/api/hoxi/event", nil).WithContext(ctx))
	body := w.Body.String()
	if !strings.Contains(body, statusSnapshotEventType) || !strings.Contains(body, "ses_qdr_running") {
		t.Fatalf("unified stream did not start with the busy snapshot: %s", body)
	}
}
