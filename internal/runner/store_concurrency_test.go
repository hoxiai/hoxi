package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreConversationLockDoesNotBlockOtherSessionsOrMetadata(t *testing.T) {
	s := seedStore(t, t.TempDir())
	if err := s.Put(SessionRecord{ID: "other"}); err != nil {
		t.Fatal(err)
	}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		s.VisitTurns("ses_cld_1", func(SessionRecord) { close(entered); <-release })
	}()
	<-entered
	defer func() { close(release); <-finished }()
	done := make(chan struct{})
	go func() {
		s.UpsertTurnInfo("other", MessageInfo{ID: "new", SessionID: "other", Role: "user"})
		s.Meta("ses_cld_1")
		s.List("")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a locked transcript blocked an unrelated session or metadata")
	}
}

func TestStoreChildRevisionDoesNotCancelParentRevision(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, rec := range []SessionRecord{{ID: "parent"}, {ID: "child", ParentID: "parent"}} {
		if err := s.Put(rec); err != nil {
			t.Fatal(err)
		}
	}
	info := MessageInfo{ID: "msg", SessionID: "child"}
	s.UpsertTurnInfo("child", info)
	s.UpsertTurnPart("child", MessagePart{ID: "part", MessageID: "msg", Type: "text"})
	s.AppendPartDelta("child", "msg", "part", "x")
	s.UpsertTurnInfo("parent", MessageInfo{ID: "msg", SessionID: "parent"})
	_, before := s.ChildSessionIDs("parent")
	s.UpsertTurnInfo("parent", MessageInfo{ID: "msg", SessionID: "parent"})
	s.AppendPartDelta("child", "msg", "part", "y")
	_, after := s.ChildSessionIDs("parent")
	if after == before {
		t.Fatal("simultaneous parent/child changes reused a stale diff stamp")
	}
}

func TestStoreEvictsIdleTranscriptsAndReloadsDeltas(t *testing.T) {
	dir := t.TempDir()
	s := seedStore(t, dir)
	s.cacheMaxSessions = 1
	s.SetActive("ses_cld_1", true)
	if err := s.Put(SessionRecord{ID: "other"}); err != nil {
		t.Fatal(err)
	}
	if !s.records["ses_cld_1"].turnsLoaded {
		t.Fatal("evicted active transcript")
	}
	s.SetActive("ses_cld_1", false)
	if _, ok := s.Get("other"); !ok {
		t.Fatal("missing second session")
	}
	if s.records["ses_cld_1"].turnsLoaded {
		t.Fatal("idle transcript was not evicted")
	}
	for _, store := range []*Store{s, NewStore(dir)} {
		rec, ok := store.Get("ses_cld_1")
		if !ok || rec.Turns[0].Parts[0].Text != "hello world" {
			t.Fatalf("eviction lost streamed deltas: %+v", rec)
		}
	}
}

func TestStoreEvictionHonorsByteBudgetAndRevertHistory(t *testing.T) {
	s := NewStore(t.TempDir())
	s.cacheMaxBytes = 128
	turn := MessageTurn{Info: MessageInfo{ID: "msg_1"}, Parts: []MessagePart{{ID: "prt_1", MessageID: "msg_1", Type: "text", Text: strings.Repeat("x", 4096)}}}
	if err := s.Put(SessionRecord{ID: "big", Turns: []MessageTurn{turn}, RevertedTurns: []MessageTurn{turn}}); err != nil {
		t.Fatal(err)
	}
	if s.records["big"].turnsLoaded || s.cachedBytes != 0 {
		t.Fatal("oversized idle body remained cached")
	}
	rec, ok := s.Get("big")
	if !ok || len(rec.Turns) != 1 || len(rec.RevertedTurns) != 1 || rec.RevertedTurns[0].Parts[0].Text != turn.Parts[0].Text {
		t.Fatal("eviction lost revert history")
	}
	if len(s.List("")) != 1 {
		t.Fatal("eviction removed session metadata")
	}
}

func TestStoreConcurrentSameSessionUpdatesSurviveReload(t *testing.T) {
	dir := t.TempDir()
	s := seedStore(t, dir)
	s.SetActive("ses_cld_1", true)
	done := make(chan struct{}, 4)
	for worker := 0; worker < 4; worker++ {
		go func() {
			for i := 0; i < 100; i++ {
				s.AppendPartDelta("ses_cld_1", "msg_1", "prt_1", "x")
			}
			done <- struct{}{}
		}()
	}
	for worker := 0; worker < 4; worker++ {
		<-done
	}
	s.FlushSession("ses_cld_1")
	rec, _ := NewStore(dir).Get("ses_cld_1")
	if got := rec.Turns[0].Parts[0].Text; got != "hello world"+strings.Repeat("x", 400) {
		t.Fatalf("concurrent deltas were lost: length %d", len(got))
	}
}

func TestStoreFailedWriteRetainsLatestDeltasUntilRetry(t *testing.T) {
	dir := t.TempDir()
	s := seedStore(t, dir)
	s.SetActive("ses_cld_1", true)
	sessionDir := s.sessionDir("ses_cld_1")
	backup := sessionDir + ".backup"
	if err := os.Rename(sessionDir, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionDir, []byte("block writes"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		s.UpsertTurnPart("ses_cld_1", MessagePart{ID: "prt_1", MessageID: "msg_1", SessionID: "ses_cld_1", Type: "text", Text: "replacement"})
	}
	if len(s.records["ses_cld_1"].pending) != 1 {
		t.Fatal("failed writes retained superseded part snapshots")
	}
	s.AppendPartDelta("ses_cld_1", "msg_1", "prt_1", "!")
	s.cacheMaxSessions = 0
	s.SetActive("ses_cld_1", false)
	if !s.records["ses_cld_1"].turnsLoaded {
		t.Fatal("unpersisted transcript was evicted")
	}
	if err := os.Remove(sessionDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, sessionDir); err != nil {
		t.Fatal(err)
	}
	s.FlushSession("ses_cld_1")
	if s.records["ses_cld_1"].turnsLoaded {
		t.Fatal("successfully flushed idle body was not evicted")
	}
	rec, _ := NewStore(dir).Get("ses_cld_1")
	if got := rec.Turns[0].Parts[0].Text; got != "replacement!" {
		t.Fatalf("retry overwrote latest deltas: %q", got)
	}
}

func BenchmarkStoreReplay(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			dir := filepath.Join(b.TempDir(), "sessions")
			turns := make([]MessageTurn, count)
			for i := range turns {
				id := fmt.Sprintf("msg_%d", i)
				turns[i] = MessageTurn{Info: MessageInfo{ID: id}, Parts: []MessagePart{{ID: "part", MessageID: id, Type: "text", Text: strings.Repeat("x", 1024)}}}
			}
			if err := NewStore(dir).Put(SessionRecord{ID: "bench", Turns: turns}); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, ok := NewStore(dir).Get("bench"); !ok {
					b.Fatal("missing transcript")
				}
			}
		})
	}
}
