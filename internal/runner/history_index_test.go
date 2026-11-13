package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func indexedPage(t *testing.T, s *Store, id string, page historyPage) []MessageTurn {
	t.Helper()
	raw, ok := s.MarshalTurnsPage(id, page)
	if !ok {
		t.Fatal("missing session")
	}
	var turns []MessageTurn
	if err := json.Unmarshal(raw, &turns); err != nil {
		t.Fatal(err)
	}
	return turns
}

func TestIndexedHistoryReadsPagesWithoutHydratingFullTranscript(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Put(SessionRecord{ID: "bench", Turns: performanceTurns(5000)}); err != nil {
		t.Fatal(err)
	}
	cold := NewStore(dir)
	for _, page := range []historyPage{{limit: 200}, {limit: 200, before: "msg_004800"}, {limit: 200, before: "missing"}} {
		got := indexedPage(t, cold, "bench", page)
		want := sliceHistoryPage(performanceTurns(5000), page)
		if !reflect.DeepEqual(turnIDs(got), turnIDs(want)) {
			t.Fatalf("wrong page %v", page)
		}
		if cold.records["bench"].turnsLoaded || cold.cachedBytes != 0 {
			t.Fatal("a page pinned the entire transcript")
		}
	}
	summary, ok := cold.sessionEditsSnapshot("bench")
	if !ok || len(summary.paths) != 5000 {
		t.Fatal("cold edit summary lost paths")
	}
	if cold.records["bench"].turnsLoaded || cold.cachedBytes != 0 {
		t.Fatal("the follow-up Diff query undid paged history's memory bound")
	}
}

func TestLegacyEditSummaryIsCachedWithoutPinningTranscript(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Put(SessionRecord{ID: "bench", Turns: performanceTurns(100)}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.sessionDir("bench"), historyIndexFile)); err != nil {
		t.Fatal(err)
	}
	cold := NewStore(dir)
	if summary, ok := cold.sessionEditsSnapshot("bench"); !ok || len(summary.paths) != 100 {
		t.Fatal("legacy edit summary lost paths")
	}
	if cold.records["bench"].turnsLoaded {
		t.Fatal("legacy summary pinned full history")
	}
	info, err := os.Stat(filepath.Join(s.sessionDir("bench"), sessionTurnsFile))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := cold.loadHistoryIndex("bench", info)
	if err != nil || idx == nil || !idx.EditsComplete || len(idx.EditedPaths) != 100 {
		t.Fatal("legacy summary was not cached")
	}
}

func TestIndexedHistoryReplaysOldMessageUpdatesAndTaskBoundaries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(dir)
	turns := historyTestTurns(10)
	turns[7].Info.ContinueTask = true
	if err := s.Put(SessionRecord{ID: "page", Turns: turns}); err != nil {
		t.Fatal(err)
	}
	s.SetActive("page", true)
	// The last log record is an update to an old message, not the newest turn.
	s.UpsertTurnInfo("page", MessageInfo{ID: "msg_007", Role: "assistant"})
	s.UpsertTurnPart("page", MessagePart{ID: "prt_msg_007", MessageID: "msg_007", Type: "text", Text: "corrected"})
	s.FlushSession("page")
	full, _ := NewStore(dir).Get("page")
	cold := NewStore(dir)
	got := indexedPage(t, cold, "page", historyPage{limit: 3})
	if !reflect.DeepEqual(got, full.Turns[7:]) {
		t.Fatalf("indexed replay differs from full replay: %+v", got)
	}
	if !got[0].Info.ContinueTask || got[0].Parts[0].Text != "corrected" {
		t.Fatal("lost task boundary or old-message correction")
	}
	if cold.records["page"].turnsLoaded {
		t.Fatal("stale index forced full hydration")
	}
}

func TestIndexedHistoryBacksUpCorruptIndexesAndRecoversTornLogs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Put(SessionRecord{ID: "page", Turns: historyTestTurns(10)}); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(s.sessionDir("page"), historyIndexFile)
	if err := os.WriteFile(indexPath, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(indexedPage(t, NewStore(dir), "page", historyPage{limit: 3})) != 3 {
		t.Fatal("corrupt index broke history")
	}
	backups, _ := filepath.Glob(indexPath + ".corrupt-*")
	if len(backups) != 1 {
		t.Fatal("corrupt index was overwritten without backup")
	}
	logPath := filepath.Join(s.sessionDir("page"), sessionTurnsFile)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"part":{"id":"torn`)
	_ = f.Close()
	if got := indexedPage(t, NewStore(dir), "page", historyPage{limit: 3}); len(got) != 3 {
		t.Fatal("torn tail broke indexed history")
	}
	recovered := NewStore(dir)
	recovered.UpsertTurnInfo("page", MessageInfo{ID: "after_crash"})
	recovered.UpsertTurnPart("page", MessagePart{ID: "text", MessageID: "after_crash", Type: "text", Text: "after recovery"})
	got := indexedPage(t, NewStore(dir), "page", historyPage{limit: 1})
	if len(got) != 1 || got[0].Parts[0].Text != "after recovery" {
		t.Fatal("append after recovery lost the new turn")
	}
}

func TestIndexedHistoryInvalidatesOffsetsAfterTranscriptReplacement(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Put(SessionRecord{ID: "page", Turns: historyTestTurns(20)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Update("page", func(rec *SessionRecord) { rec.Turns = rec.Turns[:4] }); err != nil {
		t.Fatal(err)
	}
	got := indexedPage(t, NewStore(dir), "page", historyPage{limit: 2})
	if len(got) != 2 || got[0].Info.ID != "msg_002" || got[1].Info.ID != "msg_003" {
		t.Fatal("replacement reused old index offsets")
	}
}
