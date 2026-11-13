package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func enableStreamPersistence(t testing.TB, s *Store) {
	t.Helper()
	s.StartPersistence(context.Background())
	t.Cleanup(s.ClosePersistence)
}

func occupyPersistWorkers(s *Store) func() {
	for i := 0; i < cap(s.persistSlots); i++ {
		s.persistSlots <- struct{}{}
	}
	return func() {
		for i := 0; i < cap(s.persistSlots); i++ {
			<-s.persistSlots
		}
	}
}

func waitPersistSettled(t *testing.T, s *Store, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ss := s.lockSession(id, false)
		if ss == nil {
			return
		}
		settled := ss.persistJob == nil
		ss.mu.Unlock()
		if settled {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("persistence job did not settle")
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

func TestAsyncPersistenceRetainsLatestStateAfterFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := seedStore(t, dir)
	enableStreamPersistence(t, s)
	defer s.Flush()
	s.SetActive("ses_cld_1", true)
	path := s.sessionDir("ses_cld_1")
	backup := path + ".backup"
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("block writes"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.upsertTurnPart("ses_cld_1", MessagePart{ID: "prt_1", MessageID: "msg_1", Type: "text", Text: "replacement"}, true)
	waitPersistSettled(t, s, "ses_cld_1")
	s.AppendPartDelta("ses_cld_1", "msg_1", "prt_1", " latest")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	s.FlushSession("ses_cld_1")
	got, _ := NewStore(dir).Get("ses_cld_1")
	if got.Turns[0].Parts[0].Text != "replacement latest" {
		t.Fatal("failed async write overwrote newer deltas")
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

func TestQueuedPersistenceCannotResurrectDeletedOrReplacedTranscript(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := seedStore(t, dir)
	enableStreamPersistence(t, s)
	release := occupyPersistWorkers(s)
	defer release()
	defer s.Flush()
	s.SetActive("ses_cld_1", true)
	s.upsertTurnInfo("ses_cld_1", MessageInfo{ID: "queued_old"}, true)
	if err := s.Delete("ses_cld_1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(SessionRecord{ID: "ses_cld_1", Title: "recreated"}); err != nil {
		t.Fatal(err)
	}
	s.SetActive("ses_cld_1", true)
	s.upsertTurnInfo("ses_cld_1", MessageInfo{ID: "queued_new"}, true)
	if _, _, err := s.Update("ses_cld_1", func(rec *SessionRecord) { rec.Turns = nil }); err != nil {
		t.Fatal(err)
	}
	s.FlushSession("ses_cld_1")
	got, _ := NewStore(dir).Get("ses_cld_1")
	if got.Title != "recreated" || len(got.Turns) != 0 {
		t.Fatal("queued data survived deletion/replacement")
	}
}

func TestAsyncPersistenceBoundsPendingKeys(t *testing.T) {
	t.Parallel()
	s := seedStore(t, t.TempDir())
	enableStreamPersistence(t, s)
	s.SetActive("ses_cld_1", true)
	release := occupyPersistWorkers(s)
	defer release()
	defer s.Flush()
	for i := 0; i < 600; i++ {
		s.upsertTurnPart("ses_cld_1", MessagePart{ID: fmt.Sprintf("new_%d", i), MessageID: "msg_1", Type: "text", Text: "x"}, true)
	}
	ss := s.lockSession("ses_cld_1", false)
	count := len(ss.dirty) + len(ss.dirtyInfos) + len(ss.pending)
	ss.mu.Unlock()
	if count >= maxPendingStreamEntries {
		t.Fatalf("unbounded pending stream keys: %d", count)
	}
	s.FlushSession("ses_cld_1")
	got, _ := NewStore(s.dir).Get("ses_cld_1")
	if len(got.Turns[0].Parts) != 601 {
		t.Fatal("backpressure lost parts")
	}
}

func TestAsyncWriterReleasesTranscriptLockDuringIO(t *testing.T) {
	t.Parallel()
	s := seedStore(t, t.TempDir())
	enableStreamPersistence(t, s)
	s.SetActive("ses_cld_1", true)
	defer s.Flush()
	s.upsertTurnPart("ses_cld_1", MessagePart{ID: "large", MessageID: "msg_1", Type: "tool", Tool: "read",
		State: &ToolState{Status: "completed", Output: strings.Repeat("x", 32<<20)}}, true)
	ss := s.records["ses_cld_1"]
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !ss.diskMu.TryLock() {
			if ss.mu.TryLock() {
				ss.mu.Unlock()
				return
			}
		} else {
			ss.diskMu.Unlock()
		}
		time.Sleep(100 * time.Microsecond)
	}
	t.Fatal("disk writer never released the transcript lock during I/O")
}

func TestPersistenceContextCancellationFlushesAndLateUpdatesAreSynchronous(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := seedStore(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartPersistence(ctx)
	t.Cleanup(s.ClosePersistence)
	release := occupyPersistWorkers(s)
	defer release()
	s.SetActive("ses_cld_1", true)
	s.upsertTurnPart("ses_cld_1", MessagePart{ID: "prt_1", MessageID: "msg_1", Type: "text", Text: "queued"}, true)
	cancel()
	s.ClosePersistence()
	if s.asyncPersistenceEnabled() {
		t.Fatal("cancelled host still accepts asynchronous work")
	}
	got, _ := NewStore(dir).Get("ses_cld_1")
	if got.Turns[0].Parts[0].Text != "queued" {
		t.Fatal("host shutdown lost its queued state")
	}
	s.upsertTurnPart("ses_cld_1", MessagePart{ID: "prt_1", MessageID: "msg_1", Type: "text", Text: "late teardown"}, true)
	got, _ = NewStore(dir).Get("ses_cld_1")
	if got.Turns[0].Parts[0].Text != "late teardown" {
		t.Fatal("late teardown update was left in a stopped queue")
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
	svc.store.upsertTurnInfo("stop_all", MessageInfo{ID: "before_stop"}, true)
	svc.CloseAll()
	if !svc.store.asyncPersistenceEnabled() {
		t.Fatal("stopping turns disabled the still-running host's persistence")
	}
	got, _ := NewStore(dir).Get("stop_all")
	if len(got.Turns) != 1 || got.Turns[0].Info.ID != "before_stop" {
		t.Fatal("stopping turns did not flush pending data")
	}
}
