package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
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
	// statusSnapshotEventType carries the authoritative busy set for every
	// Hoxi-managed session, regardless of which project the client is viewing.
	statusSnapshotEventType = "hoxi.status.snapshot"
	statusSnapshotScope     = "managed"
)

var (
	heartbeatFrame = fmt.Sprintf("data: {\"type\":%q,\"properties\":{}}\n\n", heartbeatEventType)
	resyncFrame    = fmt.Sprintf("data: {\"type\":%q,\"properties\":{}}\n\n", resyncEventType)
)

// Called with Service.mu held so concurrent producers cannot interleave a
// recovery marker and its event. A stalled client never stalls a runtime; it
// reconciles from durable state after the bounded queue overflows.
func enqueueStreamEvent(ch chan SSEEvent, evt SSEEvent) {
	select {
	case ch <- evt:
		return
	default:
	}
	for len(ch) > 0 {
		select {
		case <-ch:
		default:
		}
	}
	ch <- SSEEvent{Type: resyncEventType, Properties: map[string]interface{}{}}
	ch <- evt
}

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
	if s.openCode.Available() {
		upstream = make(chan string, 128)
		directory := strings.TrimSpace(r.URL.Query().Get("directory"))
		go s.pumpOpenCodeEvents(r.Context(), directory, upstream)
	}

	startSSE(w, flusher)
	// The first frame is the authoritative busy set, so a client that switched
	// projects never has to poll every project to learn what is still running.
	if frame := s.statusSnapshotFrame(); frame != "" {
		_, _ = io.WriteString(w, frame)
		flusher.Flush()
	}
	streamSSE(r.Context(), w, flusher, ch, upstream, sseHeartbeatInterval, heartbeatFrame, s.statusSnapshotFrame)
}

// statusSnapshotFrame reports the runner's own process table only: these are
// the sessions Hoxi executes and tracks regardless of the active project. It is
// a pure in-memory read, so the periodic refresh stays cheap. Stall flags ride
// along so a reconnect restores Attention for stalled sessions in every project.
func (s *Service) statusSnapshotFrame() string {
	busy := s.busySessionIDs()
	sort.Strings(busy)
	busyRaw, err := json.Marshal(busy)
	if err != nil {
		return ""
	}
	stalledRaw, err := json.Marshal(s.stalledSessions())
	if err != nil {
		return ""
	}
	return fmt.Sprintf("data: {\"type\":%q,\"properties\":{\"scope\":%q,\"busy\":%s,\"stalled\":%s}}\n\n",
		statusSnapshotEventType, statusSnapshotScope, busyRaw, stalledRaw)
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
	streamSSE(r.Context(), w, flusher, ch, nil, sseKeepaliveInterval, ": keepalive\n\n", nil)
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
// snapshot, when set, is written on every tick before the heartbeat so a client
// that missed events can still converge on the authoritative state.
func streamSSE(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	ch <-chan SSEEvent,
	upstream <-chan string,
	every time.Duration,
	heartbeat string,
	snapshot func() string,
) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if snapshot != nil {
				if frame := snapshot(); frame != "" {
					_, _ = io.WriteString(w, frame)
				}
			}
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
			if resp.StatusCode == http.StatusOK {
				opened := time.Now()
				if attempt == 0 || sendFrame(ctx, out, resyncFrame) {
					s.relayOpenCodeFrames(connCtx, cancel, resp.Body, out, directory)
				}
				if time.Since(opened) >= upstreamStableAfter {
					delay = upstreamRetryMin
				}
			}
			_ = resp.Body.Close()
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
func (s *Service) relayOpenCodeFrames(ctx context.Context, cancel context.CancelFunc, body io.Reader, out chan<- string, directory string) {
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
			line = sanitizeOpenCodePermissionSSELine(line)
			// The unified stream sends its own heartbeat.
			if strings.Contains(line, `"`+heartbeatEventType+`"`) || !s.AllowOpenCodeSSELine(line) ||
				s.claimOpenCodeSSELine(line, directory) {
				keep = false
			} else {
				line = trimSummaryDiffPatches(line)
				if s.status != nil {
					s.status.InspectSSELine(strings.TrimRight(line, "\r\n"))
				}
			}
		}
		frame.WriteString(line)
	}
}

func sanitizeOpenCodePermissionSSELine(line string) string {
	if !strings.Contains(line, `"permission.asked"`) || !strings.Contains(line, stopMarkerPrefix) {
		return line
	}
	trimmed := strings.TrimRight(line, "\r\n")
	suffix := line[len(trimmed):]
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.UseNumber()
	var root map[string]interface{}
	if dec.Decode(&root) != nil {
		return line
	}
	changed := false
	if t, _ := root["type"].(string); t == "permission.asked" {
		if props, ok := root["properties"].(map[string]interface{}); ok && sanitizeOpenCodePermissionItem(props) {
			changed = true
		}
	}
	if inner, ok := root["payload"].(map[string]interface{}); ok {
		if t, _ := inner["type"].(string); t == "permission.asked" {
			if props, ok := inner["properties"].(map[string]interface{}); ok && sanitizeOpenCodePermissionItem(props) {
				changed = true
			}
		}
	}
	if !changed {
		return line
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return line
	}
	return "data: " + string(raw) + suffix
}

// trimSummaryDiffPatches drops unified diff bodies from message summaries before
// they reach the workspace UI, which never renders them.
func trimSummaryDiffPatches(line string) string {
	if !strings.Contains(line, `"summary"`) || !strings.Contains(line, `"diffs"`) || !strings.Contains(line, `"patch"`) {
		return line
	}
	trimmed := strings.TrimRight(line, "\r\n")
	suffix := line[len(trimmed):]
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.UseNumber()
	var root interface{}
	if dec.Decode(&root) != nil {
		return line
	}
	if !stripSummaryDiffPatches(root) {
		return line
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return line
	}
	return "data: " + string(raw) + suffix
}

func stripSummaryDiffPatches(node interface{}) bool {
	changed := false
	switch v := node.(type) {
	case map[string]interface{}:
		if summary, ok := v["summary"].(map[string]interface{}); ok {
			if diffs, ok := summary["diffs"].([]interface{}); ok {
				for _, item := range diffs {
					diff, ok := item.(map[string]interface{})
					if !ok {
						continue
					}
					if _, hasPatch := diff["patch"]; hasPatch {
						delete(diff, "patch")
						changed = true
					}
				}
			}
		}
		for _, item := range v {
			if stripSummaryDiffPatches(item) {
				changed = true
			}
		}
	case []interface{}:
		for _, item := range v {
			if stripSummaryDiffPatches(item) {
				changed = true
			}
		}
	}
	return changed
}

func sendFrame(ctx context.Context, out chan<- string, frame string) bool {
	select {
	case out <- frame:
		return true
	case <-ctx.Done():
		return false
	}
}
