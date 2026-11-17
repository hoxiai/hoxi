package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSSEReplayIncrementalCatchupAndResyncFallback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(root, "store.json"))
	defer svc.CloseAll()

	rec := SessionRecord{
		ID:           "ses_seq_1",
		Runtime:      string(RuntimeNative),
		ProjectDir:   root,
		WorktreePath: root,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	// Emit 3 events across 2 sessions -> seq 1, 2, 3
	svc.broadcast("ses_seq_1", SSEEvent{
		Type:       "message.part.delta",
		Properties: map[string]interface{}{"sessionID": "ses_seq_1", "messageID": "m1", "partID": "p1", "field": "text", "delta": "A"},
	})
	svc.broadcast("ses_other", SSEEvent{
		Type:       "session.status",
		Properties: map[string]interface{}{"sessionID": "ses_other", "status": SessionStatus{Type: "busy"}},
	})
	svc.broadcast("ses_seq_1", SSEEvent{
		Type:       "message.part.delta",
		Properties: map[string]interface{}{"sessionID": "ses_seq_1", "messageID": "m1", "partID": "p1", "field": "text", "delta": "B"},
	})

	if got := svc.CurrentEventSeq(); got != 3 {
		t.Fatalf("expected CurrentEventSeq=3, got %d", got)
	}

	// 1. Unified SSE reconnect with ?after_seq=1 replays seq 2 and seq 3 without resync!
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	w := &streamReadyWriter{ResponseRecorder: httptest.NewRecorder()}
	var step func()
	step = func() {
		if strings.Contains(w.Body.String(), `"delta":"B"`) {
			cancel()
			return
		}
		w.onFlush = step
	}
	w.onFlush = step
	svc.handleUnifiedSSE(w, httptest.NewRequest(http.MethodGet, "/api/hoxi/event?after_seq=1", nil).WithContext(ctx))
	body := w.Body.String()
	if strings.Contains(body, resyncEventType) {
		t.Fatalf("did not expect resync when cursor is within ring buffer: %s", body)
	}
	if strings.Contains(body, `"delta":"A"`) {
		t.Fatalf("expected seq=1 event to be skipped on after_seq=1, got: %s", body)
	}
	if !strings.Contains(body, `"seq":2`) || !strings.Contains(body, `"seq":3`) || !strings.Contains(body, `"delta":"B"`) {
		t.Fatalf("expected seq 2 & 3 replayed in order, got: %s", body)
	}

	// 2. Per-session SubscribeSince filters replayed events to that session only
	chSes, unsubSes := svc.SubscribeSince("ses_seq_1", 1)
	defer unsubSes()
	select {
	case ev := <-chSes:
		if ev.Seq != 3 || ev.Properties["delta"] != "B" {
			t.Fatalf("expected filtered replay seq=3 delta=B, got %+v", ev)
		}
	default:
		t.Fatal("expected replayed event for ses_seq_1")
	}

	// 3. Server restart cursor (after_seq > currentSeq) triggers immediate hoxi.stream.resync with authoritative seq
	chRestart, unsubRestart := svc.SubscribeSince("", 999)
	defer unsubRestart()
	select {
	case ev := <-chRestart:
		if ev.Type != resyncEventType || ev.Seq != 3 {
			t.Fatalf("expected %s with seq=3 on ahead cursor, got %+v", resyncEventType, ev)
		}
	default:
		t.Fatal("expected immediate resync event when after_seq > currentSeq")
	}

	// 4. Ring buffer overflow triggers immediate hoxi.stream.resync for stale cursor
	for i := 0; i < sseReplayCapacity+10; i++ {
		svc.broadcast("ses_other", SSEEvent{
			Type:       "session.idle",
			Properties: map[string]interface{}{"sessionID": "ses_other"},
		})
	}
	chOverflow, unsubOverflow := svc.SubscribeSince("", 2)
	defer unsubOverflow()
	select {
	case ev := <-chOverflow:
		if ev.Type != resyncEventType {
			t.Fatalf("expected %s after ring buffer eviction, got %+v", resyncEventType, ev)
		}
	default:
		t.Fatal("expected resync event when cursor fell behind ring buffer")
	}

	// 5. GET /api/hoxi/runner/sessions/{id}/message exposes X-Hoxi-Event-Seq header
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	msgRec := httptest.NewRecorder()
	mux.ServeHTTP(msgRec, httptest.NewRequest(http.MethodGet, "/api/hoxi/runner/sessions/ses_seq_1/message", nil))
	if msgRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on message endpoint, got %d", msgRec.Code)
	}
	hdrSeq, err := strconv.ParseUint(msgRec.Header().Get("X-Hoxi-Event-Seq"), 10, 64)
	if err != nil || hdrSeq != svc.CurrentEventSeq() {
		t.Fatalf("expected X-Hoxi-Event-Seq=%d, got %q", svc.CurrentEventSeq(), msgRec.Header().Get("X-Hoxi-Event-Seq"))
	}
	var turns []MessageTurn
	if err := json.Unmarshal(msgRec.Body.Bytes(), &turns); err != nil {
		t.Fatalf("invalid message JSON: %v", err)
	}
}
