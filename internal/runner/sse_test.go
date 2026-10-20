package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func collectFrames(t *testing.T, out <-chan string, want int) []string {
	t.Helper()
	var frames []string
	deadline := time.After(5 * time.Second)
	for len(frames) < want {
		select {
		case f := <-out:
			frames = append(frames, f)
		case <-deadline:
			t.Fatalf("timed out after %d frames: %q", len(frames), frames)
		}
	}
	return frames
}

func TestOpenCodePumpReconnectsAndAsksClientsToResync(t *testing.T) {
	var conns atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := conns.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if n == 1 {
			// A complete event, then one cut off by the disconnect.
			fmt.Fprint(w, "data: {\"type\":\"first\",\"properties\":{}}\n\n")
			fmt.Fprint(w, "data: {\"type\":\"cut")
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"server.heartbeat\",\"properties\":{}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"second\",\"properties\":{}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()

	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "store.json"))
	svc.ConfigureOpenCode(upstream.URL, "secret")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan string, 16)
	go svc.pumpOpenCodeEvents(ctx, "", out)

	frames := collectFrames(t, out, 3)
	if !strings.Contains(frames[0], `"first"`) {
		t.Fatalf("first frame = %q", frames[0])
	}
	if frames[1] != resyncFrame {
		t.Fatalf("reconnect did not ask clients to resync: %q", frames[1])
	}
	if !strings.Contains(frames[2], `"second"`) {
		t.Fatalf("third frame = %q", frames[2])
	}
	for _, f := range frames {
		if strings.Contains(f, "cut") || strings.Contains(f, heartbeatEventType) {
			t.Fatalf("partial frame or upstream heartbeat leaked: %q", f)
		}
		if !strings.HasSuffix(f, "\n\n") {
			t.Fatalf("frame is not terminated: %q", f)
		}
	}
}

func TestOpenCodePumpRetriesUntilUpstreamComesUp(t *testing.T) {
	var up atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			up.Store(true)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()

	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "store.json"))
	svc.ConfigureOpenCode(upstream.URL, "secret")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan string, 16)
	go svc.pumpOpenCodeEvents(ctx, "", out)

	frames := collectFrames(t, out, 2)
	if frames[0] != resyncFrame || !strings.Contains(frames[1], "server.connected") {
		t.Fatalf("frames = %q", frames)
	}
}

func TestUnifiedSSEHeartbeatIsADataEvent(t *testing.T) {
	if !strings.HasPrefix(heartbeatFrame, "data: ") || !strings.Contains(heartbeatFrame, heartbeatEventType) {
		t.Fatalf("heartbeat must reach EventSource listeners: %q", heartbeatFrame)
	}
}
