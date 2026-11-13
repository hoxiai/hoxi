package runner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMetadataUpdateLeavesColdHistoryAndRevertIntact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(dir)
	rec := SessionRecord{ID: "metadata", Title: "before", UpdatedAt: 7, Turns: performanceTurns(1000),
		RevertedTurns: performanceTurns(2), Revert: &SessionRevert{MessageID: "msg_000002"}}
	if err := s.Put(rec); err != nil {
		t.Fatal(err)
	}
	s = NewStore(dir)
	path := filepath.Join(dir, rec.ID, sessionTurnsFile)
	before, _ := os.ReadFile(path)
	edits := s.editMutations.Load()
	got, ok, err := s.UpdateMetadata(rec.ID, func(meta *SessionMetadata) { meta.Title = "after"; meta.ArchivedAt = 99 })
	if err != nil || !ok || got.Title != "after" || got.ArchivedAt != 99 || got.Turns != nil || got.RevertedTurns != nil {
		t.Fatalf("bad metadata result: %#v %v %v", got, ok, err)
	}
	ss := s.lockSession(rec.ID, false)
	loaded := ss.turnsLoaded || ss.rec.Turns != nil || ss.rec.RevertedTurns != nil
	ss.mu.Unlock()
	if loaded {
		t.Fatal("metadata update hydrated cold history")
	}
	if s.editMutations.Load() != edits {
		t.Fatal("metadata invalidated file summaries")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("metadata update rewrote history log")
	}
	if err := s.SetProjectID(rec.ID, "project"); err != nil {
		t.Fatal(err)
	}
	project, _ := s.Meta(rec.ID)
	if project.UpdatedAt != got.UpdatedAt {
		t.Fatal("project attribution changed activity")
	}
	reloaded, _ := NewStore(dir).Get(rec.ID)
	if reloaded.Title != "after" || len(reloaded.Turns) != 1000 || len(reloaded.RevertedTurns) != 2 || reloaded.Revert == nil {
		t.Fatal("metadata update lost transcript or reverted history after restart")
	}
}

func TestMetadataUpdateKeepsPendingStreamWrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := seedStore(t, dir)
	enableStreamPersistence(t, s)
	s.SetActive("ses_cld_1", true)
	release := occupyPersistWorkers(s)
	defer release()
	s.upsertTurnPart("ses_cld_1", MessagePart{ID: "prt_1", MessageID: "msg_1", Type: "text", Text: "live"}, true)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			s.AppendPartDelta("ses_cld_1", "msg_1", "prt_1", " token")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			if _, _, err := s.UpdateMetadata("ses_cld_1", func(meta *SessionMetadata) { meta.Title = fmt.Sprintf("title-%d", i) }); err != nil {
				t.Error(err)
			}
		}
	}()
	wg.Wait()
	s.FlushSession("ses_cld_1")
	reloaded, _ := NewStore(dir).Get("ses_cld_1")
	if reloaded.Title != "title-9" || reloaded.Turns[0].Parts[0].Text != "live"+string(bytes.Repeat([]byte(" token"), 20)) {
		t.Fatal("metadata update cancelled, lost or reordered pending stream writes")
	}
}

func TestMetadataUpdateFailureDoesNotPublishOrOverwriteCorruption(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Put(SessionRecord{ID: "broken", Title: "before", RevertedTurns: performanceTurns(1)}); err != nil {
		t.Fatal(err)
	}
	s = NewStore(dir)
	path := filepath.Join(dir, "broken", sessionMetaFile)
	bad := []byte("{broken")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateMetadata("broken", func(meta *SessionMetadata) { meta.Title = "after" }); err == nil {
		t.Fatal("accepted corrupted metadata")
	}
	meta, _ := s.Meta("broken")
	raw, _ := os.ReadFile(path)
	backups, _ := filepath.Glob(path + ".corrupt-*")
	if meta.Title != "before" || !bytes.Equal(raw, bad) || len(backups) != 1 {
		t.Fatal("failed update overwrote state or skipped corruption backup")
	}
	if _, ok, err := s.UpdateMetadata("missing", func(meta *SessionMetadata) {}); err != nil || ok {
		t.Fatal("missing update created a session")
	}
}

func BenchmarkRunnerMetadataUpdate(b *testing.B) {
	for _, fast := range []bool{false, true} {
		name := "full-update"
		if fast {
			name = "metadata-only"
		}
		b.Run(name, func(b *testing.B) {
			dir := b.TempDir()
			s := NewStore(dir)
			if err := s.Put(SessionRecord{ID: "bench", Turns: performanceTurns(5000)}); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				cold := NewStore(dir)
				b.StartTimer()
				var err error
				if fast {
					_, _, err = cold.UpdateMetadata("bench", func(meta *SessionMetadata) { meta.Title = "rename" })
				} else {
					_, _, err = cold.Update("bench", func(rec *SessionRecord) { rec.Title = "rename" })
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
