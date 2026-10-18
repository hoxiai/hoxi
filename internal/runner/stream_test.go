package runner

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
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
