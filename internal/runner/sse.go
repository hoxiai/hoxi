package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The unified stream (/api/hoxi/event) merges runner events with OpenCode's
// own event stream. Its heartbeat is a real `data:` event, because SSE
// comments never reach EventSource listeners: a client that hears nothing
// for a few intervals knows the connection is dead and reconnects.
//
// The OpenCode leg reconnects on its own (OpenCode ends `/event` when its
// instance is disposed, and the daemon may restart), and tells the client
// to resync, since events sent while it was down are gone.

const (
	sseHeartbeatInterval = 10 * time.Second
	sseKeepaliveInterval = 20 * time.Second

	upstreamRetryMin = 250 * time.Millisecond
	upstreamRetryMax = 5 * time.Second
	// OpenCode heartbeats every 10s; silence past this means a dead socket.
	upstreamIdleTimeout = 35 * time.Second
	// A connection that lived this long was healthy, so backoff restarts.
	upstreamStableAfter = 30 * time.Second

	heartbeatEventType = "server.heartbeat"
	resyncEventType    = "hoxi.stream.resync"
)

var (
	heartbeatFrame = fmt.Sprintf("data: {\"type\":%q,\"properties\":{}}\n\n", heartbeatEventType)
	resyncFrame    = fmt.Sprintf("data: {\"type\":%q,\"properties\":{}}\n\n", resyncEventType)
)

func (s *Service) handleGlobalSSE(w http.ResponseWriter, r *http.Request) {
	s.serveSSE(w, r, "")
}

func (s *Service) handleUnifiedSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch, unsub := s.Subscribe("")
	defer unsub()

	var upstream chan string
	if s.openCodeClient != nil && strings.TrimSpace(s.openCodeURL) != "" {
		upstream = make(chan string, 128)
		directory := strings.TrimSpace(r.URL.Query().Get("directory"))
		go s.pumpOpenCodeEvents(r.Context(), directory, upstream)
	}

	startSSE(w, flusher)
	streamSSE(r.Context(), w, flusher, ch, upstream, sseHeartbeatInterval, heartbeatFrame)
}

func (s *Service) serveSSE(w http.ResponseWriter, r *http.Request, sessionID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	// Subscribe before flushing headers so clients cannot dispatch a prompt before we listen.
	ch, unsub := s.Subscribe(sessionID)
	defer unsub()

	startSSE(w, flusher)
	streamSSE(r.Context(), w, flusher, ch, nil, sseKeepaliveInterval, ": keepalive\n\n")
}

func startSSE(w http.ResponseWriter, flusher http.Flusher) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
}

// streamSSE writes runner events and raw upstream frames until the client leaves.
func streamSSE(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	ch <-chan SSEEvent,
	upstream <-chan string,
	every time.Duration,
	heartbeat string,
) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = io.WriteString(w, heartbeat)
		case evt := <-ch:
			raw, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		case frame, ok := <-upstream:
			if !ok {
				upstream = nil
				continue
			}
			_, _ = io.WriteString(w, frame)
		}
		flusher.Flush()
	}
}

// pumpOpenCodeEvents keeps one OpenCode event subscription alive for the
// lifetime of ctx, reconnecting with backoff whenever it drops.
func (s *Service) pumpOpenCodeEvents(ctx context.Context, directory string, out chan<- string) {
	path := "/global/event"
	if directory != "" {
		path = "/event"
	}
	delay := upstreamRetryMin
	for attempt := 0; ctx.Err() == nil; attempt++ {
		connCtx, cancel := context.WithCancel(ctx)
		resp, err := s.openCodeRequest(connCtx, http.MethodGet, path, directory, nil)
		if err == nil {
			opened := time.Now()
			if attempt == 0 || sendFrame(ctx, out, resyncFrame) {
				s.relayOpenCodeFrames(connCtx, cancel, resp.Body, out)
			}
			_ = resp.Body.Close()
			if time.Since(opened) >= upstreamStableAfter {
				delay = upstreamRetryMin
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, upstreamRetryMax)
	}
}

// relayOpenCodeFrames forwards complete SSE frames, so a frame cut off by a
// disconnect is dropped instead of merging into the next event. A silent
// socket is cancelled so the pump can reconnect.
func (s *Service) relayOpenCodeFrames(ctx context.Context, cancel context.CancelFunc, body io.Reader, out chan<- string) {
	idle := time.AfterFunc(upstreamIdleTimeout, cancel)
	defer idle.Stop()

	reader := bufio.NewReaderSize(body, 64*1024)
	var frame strings.Builder
	keep := true
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		idle.Reset(upstreamIdleTimeout)
		if strings.TrimSpace(line) == "" {
			if keep && frame.Len() > 0 && !sendFrame(ctx, out, frame.String()+"\n") {
				return
			}
			frame.Reset()
			keep = true
			continue
		}
		if strings.HasPrefix(line, "data:") && keep {
			// The unified stream sends its own heartbeat.
			if strings.Contains(line, `"`+heartbeatEventType+`"`) || !s.AllowOpenCodeSSELine(line) {
				keep = false
			} else if s.status != nil {
				s.status.InspectSSELine(strings.TrimRight(line, "\r\n"))
			}
		}
		frame.WriteString(line)
	}
}

func sendFrame(ctx context.Context, out chan<- string, frame string) bool {
	select {
	case out <- frame:
		return true
	case <-ctx.Done():
		return false
	}
}
