package sessionstore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

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
