package runner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

type disconnectedStreamWriter struct {
	header   http.Header
	writes   int
	flushed  func()
	writable int
}

func (w *disconnectedStreamWriter) Header() http.Header { return w.header }
func (w *disconnectedStreamWriter) WriteHeader(int)     {}
func (w *disconnectedStreamWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes <= w.writable {
		return len(p), nil
	}
	return 0, io.ErrClosedPipe
}
func (w *disconnectedStreamWriter) Flush() {
	if w.flushed != nil {
		w.flushed()
	}
}

func TestSSEWriteFailureStopsEveryFrameSource(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"runner", "upstream", "heartbeat", "snapshot", "closed"} {
		t.Run(source, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			ch := make(chan SSEEvent, 1)
			upstream := make(chan string, 1)
			var snapshot func() string
			switch source {
			case "runner":
				ch <- SSEEvent{Type: "session.idle"}
			case "upstream":
				upstream <- "data: {}\n\n"
			case "snapshot":
				snapshot = func() string { return "data: {}\n\n" }
			case "closed":
				close(ch)
			}
			w := &disconnectedStreamWriter{header: make(http.Header)}
			streamSSE(ctx, w, w, ch, upstream, time.Millisecond, heartbeatFrame, snapshot)
			if ctx.Err() != nil {
				t.Fatal("stream ignored write failure/closed channel until context timeout")
			}
			want := 1
			if source == "closed" {
				want = 0
			}
			if w.writes != want {
				t.Fatalf("writes=%d want=%d", w.writes, want)
			}
		})
	}
}

func TestSSEWriteFailureReleasesSubscription(t *testing.T) {
	t.Parallel()
	for _, unified := range []bool{false, true} {
		root := t.TempDir()
		svc := NewService(root, nil, nil, filepath.Join(root, "sessions"))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		w := &disconnectedStreamWriter{header: make(http.Header)}
		w.flushed = func() { svc.broadcast("session", SSEEvent{Type: "session.idle"}) }
		r := httptest.NewRequest(http.MethodGet, "/api/hoxi/event", nil).WithContext(ctx)
		if unified {
			svc.handleUnifiedSSE(w, r)
		} else {
			svc.serveSSE(w, r, "")
		}
		expired := ctx.Err() != nil
		cancel()
		svc.mu.Lock()
		remaining := len(svc.subs)
		svc.mu.Unlock()
		svc.CloseAll()
		if expired || remaining != 0 {
			t.Fatalf("unified=%v expired=%v remaining subscribers=%d", unified, expired, remaining)
		}
	}
}

func TestSSEWriteFailureCancelsUpstreamWithoutParentCancellation(t *testing.T) {
	t.Parallel()
	disconnected := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(disconnected)
	}))
	defer upstream.Close()
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(root, "sessions"))
	defer svc.CloseAll()
	svc.ConfigureOpenCode(upstream.URL, "fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Allow the initial busy snapshot, then fail the first upstream frame.
	w := &disconnectedStreamWriter{header: make(http.Header), writable: 1}
	svc.handleUnifiedSSE(w, httptest.NewRequest(http.MethodGet, "/api/hoxi/event", nil).WithContext(ctx))
	if ctx.Err() != nil {
		t.Fatal("stream waited for parent cancellation instead of the write failure")
	}
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("upstream event pump survived the disconnected client")
	}
}
