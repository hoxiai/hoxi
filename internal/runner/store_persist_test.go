package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func enableStreamPersistence(t testing.TB, s *Store) {
	t.Helper()
	s.StartPersistence(context.Background())
	t.Cleanup(s.ClosePersistence)
}

func occupyPersistWorkers(s *Store) func() {
	return s.OccupyPersistWorkers()
}

func TestStreamBroadcastDoesNotWaitForDiskWorkersAndIdleFlushesInOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	svc := NewService("/repo", nil, nil, dir)
	enableStreamPersistence(t, svc.store)
	if err := svc.store.Put(SessionRecord{ID: "stream"}); err != nil {
		t.Fatal(err)
	}
	release := occupyPersistWorkers(svc.store)
	defer release()
	defer svc.CloseAll()
	ch, stop := svc.Subscribe("stream")
	defer stop()
	svc.broadcast("stream", SSEEvent{Type: "session.status", Properties: map[string]interface{}{"status": SessionStatus{Type: "busy"}}})
	for i := 0; i < 3; i++ {
		messageID := fmt.Sprintf("msg_%d", i)
		svc.broadcast("stream", SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": MessageInfo{ID: messageID, SessionID: "stream"}}})
		for j := 0; j < 10; j++ {
			partID := fmt.Sprintf("part_%d", j)
			svc.broadcast("stream", SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": MessagePart{ID: partID, MessageID: messageID, SessionID: "stream", Type: "text", Text: partID}}})
		}
	}
	if len(ch) != 34 {
		t.Fatalf("broadcast waited for persistence or dropped events: %d", len(ch))
	}
	if info, err := os.Stat(filepath.Join(dir, "stream", sessionTurnsFile)); err != nil || info.Size() != 0 {
		t.Fatal("queued stream performed synchronous disk writes")
	}
	if rec, ok := svc.store.Get("stream"); !ok || len(rec.Turns) != 3 {
		t.Fatal("REST state lagged behind streamed events")
	}
	svc.broadcast("stream", SSEEvent{Type: "session.idle", Properties: map[string]interface{}{"sessionID": "stream"}})
	reloaded, _ := NewStore(dir).Get("stream")
	if len(reloaded.Turns) != 3 {
		t.Fatal("idle did not flush queued infos")
	}
	for i, turn := range reloaded.Turns {
		if turn.Info.ID != fmt.Sprintf("msg_%d", i) || len(turn.Parts) != 10 {
			t.Fatal("coalescing reordered turns or lost parts")
		}
		for j, part := range turn.Parts {
			if part.ID != fmt.Sprintf("part_%d", j) {
				t.Fatal("coalescing reordered parts")
			}
		}
	}
}

func TestIdleStatusAloneIsADurableStreamBarrier(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	svc := NewService("/repo", nil, nil, dir)
	enableStreamPersistence(t, svc.store)
	if err := svc.store.Put(SessionRecord{ID: "status_idle"}); err != nil {
		t.Fatal(err)
	}
	release := occupyPersistWorkers(svc.store)
	defer release()
	defer svc.CloseAll()
	svc.store.SetActive("status_idle", true)
	svc.broadcast("status_idle", SSEEvent{Type: "message.updated", Properties: map[string]interface{}{
		"info": MessageInfo{ID: "msg", SessionID: "status_idle"},
	}})
	svc.broadcast("status_idle", SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{
		"part": MessagePart{ID: "text", MessageID: "msg", SessionID: "status_idle", Type: "text", Text: "latest"},
	}})
	svc.broadcast("status_idle", SSEEvent{Type: "message.part.delta", Properties: map[string]interface{}{
		"messageID": "msg", "partID": "text", "delta": " token",
	}})
	svc.broadcast("status_idle", SSEEvent{Type: "session.status", Properties: map[string]interface{}{
		"status": SessionStatus{Type: "idle"},
	}})
	got, _ := NewStore(dir).Get("status_idle")
	if len(got.Turns) != 1 || len(got.Turns[0].Parts) != 1 || got.Turns[0].Parts[0].Text != "latest token" {
		t.Fatal("idle status was visible before the stream was durably flushed")
	}
}

func TestCloseAllFlushesWithoutStoppingHostPersistence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	svc := NewService("/repo", nil, nil, dir)
	enableStreamPersistence(t, svc.store)
	if err := svc.store.Put(SessionRecord{ID: "stop_all"}); err != nil {
		t.Fatal(err)
	}
	svc.store.SetActive("stop_all", true)
	svc.store.UpsertTurnInfoAsync("stop_all", MessageInfo{ID: "before_stop"})
	svc.CloseAll()
	if !svc.store.AsyncPersistenceEnabled() {
		t.Fatal("stopping turns disabled the still-running host's persistence")
	}
	got, _ := NewStore(dir).Get("stop_all")
	if len(got.Turns) != 1 || got.Turns[0].Info.ID != "before_stop" {
		t.Fatal("stopping turns did not flush pending data")
	}
}
