package sessionstore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func countLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		n++
	}
	return n
}

func TestStorePersistsTranscriptPerSession(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	seedStore(t, dir).Flush()

	reopened := NewStore(dir)
	meta, ok := reopened.Meta("ses_cld_1")
	if !ok || meta.Title != "first" || meta.Turns != nil {
		t.Fatalf("meta = %+v, %v", meta, ok)
	}
	if reopened.IsTurnsLoaded("ses_cld_1") {
		t.Fatal("transcript loaded before first Get")
	}
	rec, ok := reopened.Get("ses_cld_1")
	if !ok || len(rec.Turns) != 1 || len(rec.Turns[0].Parts) != 1 || rec.Turns[0].Parts[0].Text != "hello world" {
		t.Fatalf("reloaded turns = %+v", rec.Turns)
	}
	for _, name := range []string{sessionMetaFile, sessionTurnsFile} {
		if _, err := os.Stat(filepath.Join(dir, "ses_cld_1", name)); err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
	}
}

func TestStoreMetadataUpdateAppendsPendingDeltasWithoutRewrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := seedStore(t, dir)
	turnsPath := filepath.Join(dir, "ses_cld_1", sessionTurnsFile)
	before := countLines(t, turnsPath)

	if _, _, err := store.Update("ses_cld_1", func(r *SessionRecord) { r.Title = "renamed" }); err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, turnsPath); got != before+1 {
		t.Fatalf("turns log lines = %d, want %d (one flushed delta)", got, before+1)
	}
	rec, _ := NewStore(dir).Get("ses_cld_1")
	if rec.Title != "renamed" || rec.Turns[0].Parts[0].Text != "hello world" {
		t.Fatalf("reloaded = %+v", rec)
	}
}

func TestStoreRewritesTranscriptWhenTurnsReplaced(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := seedStore(t, dir)
	if _, _, err := store.Update("ses_cld_1", func(r *SessionRecord) { r.Turns = nil }); err != nil {
		t.Fatal(err)
	}
	if rec, _ := NewStore(dir).Get("ses_cld_1"); len(rec.Turns) != 0 {
		t.Fatalf("turns survived replacement: %+v", rec.Turns)
	}
}

func TestStoreCompactsRepeatedPartUpdates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := seedStore(t, dir)
	for i := 0; i < 500; i++ {
		store.UpsertTurnPart("ses_cld_1", MessagePart{ID: "prt_1", SessionID: "ses_cld_1", MessageID: "msg_1", Type: "text", Text: strings.Repeat("x", i)})
	}
	if lines := countLines(t, filepath.Join(dir, "ses_cld_1", sessionTurnsFile)); lines > 2*2+64+1 {
		t.Fatalf("turns log not compacted: %d lines", lines)
	}
	rec, _ := NewStore(dir).Get("ses_cld_1")
	if got := rec.Turns[0].Parts[0].Text; len(got) != 499 {
		t.Fatalf("latest part text length = %d", len(got))
	}
}

func TestStoreSkipsTornTrailingLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	seedStore(t, dir).Flush()
	f, err := os.OpenFile(filepath.Join(dir, "ses_cld_1", sessionTurnsFile), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"part":{"id":"prt_2","messageID":"msg_1","ty`)
	_ = f.Close()

	reopened := NewStore(dir)
	rec, ok := reopened.Get("ses_cld_1")
	if !ok || len(rec.Turns) != 1 || len(rec.Turns[0].Parts) != 1 || rec.Turns[0].Parts[0].Text != "hello world" {
		t.Fatalf("torn line broke replay: %+v", rec.Turns)
	}

	// Appending a second turn after crash recovery must strip the torn tail so
	// the new entry is not concatenated onto the broken fragment and lost on reload.
	reopened.UpsertTurnInfo("ses_cld_1", MessageInfo{ID: "msg_2", SessionID: "ses_cld_1", Role: "user"})
	reopened.UpsertTurnPart("ses_cld_1", MessagePart{ID: "prt_u2", SessionID: "ses_cld_1", MessageID: "msg_2", Type: "text", Text: "after crash"})
	reopened.Flush()

	inMem, ok := reopened.Get("ses_cld_1")
	if !ok || len(inMem.Turns) != 2 {
		t.Fatalf("in-memory turns after recovery append = %d, want 2", len(inMem.Turns))
	}

	reloaded, ok := NewStore(dir).Get("ses_cld_1")
	if !ok || len(reloaded.Turns) != 2 || len(reloaded.Turns[1].Parts) != 1 || reloaded.Turns[1].Parts[0].Text != "after crash" {
		t.Fatalf("new turn lost on reload after torn trailing line: %+v", reloaded.Turns)
	}
}

func TestStoreRejectsUnsafeIDsAndDeletesDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := seedStore(t, dir)
	for _, id := range []string{"", "..", "../escape", "a/b", `a\b`} {
		if err := store.Put(SessionRecord{ID: id}); err == nil {
			t.Fatalf("Put(%q) succeeded", id)
		}
	}
	if err := store.Delete("ses_cld_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ses_cld_1")); !os.IsNotExist(err) {
		t.Fatalf("session directory survived delete: %v", err)
	}
	if _, ok := NewStore(dir).Get("ses_cld_1"); ok {
		t.Fatal("deleted session reloaded")
	}
}

func TestStoreMigratesLegacySingleFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	legacy := filepath.Join(root, legacyStoreFile)
	doc := map[string]any{"sessions": []SessionRecord{{
		ID: "ses_agy_old", Runtime: "antigravity", ProjectDir: "/repo", Title: "legacy",
		Turns: []MessageTurn{{
			Info:  MessageInfo{ID: "msg_1", SessionID: "ses_agy_old", Role: "user"},
			Parts: []MessagePart{{ID: "prt_1", SessionID: "ses_agy_old", MessageID: "msg_1", Type: "text", Text: "hi"}},
		}},
	}}}
	raw, _ := json.Marshal(doc)
	if err := os.WriteFile(legacy, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "sessions")
	store := NewStore(dir)
	store.MigrateLegacy(legacy)
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy file kept after migration")
	}
	if _, err := os.Stat(legacy + ".migrated"); err != nil {
		t.Fatalf("legacy backup missing: %v", err)
	}
	rec, ok := NewStore(dir).Get("ses_agy_old")
	if !ok || rec.Title != "legacy" || len(rec.Turns) != 1 || rec.Turns[0].Parts[0].Text != "hi" {
		t.Fatalf("migrated record = %+v, %v", rec, ok)
	}
}

func TestVisitTurnsTransientAndCompactIdleTurns(t *testing.T) {
	t.Parallel()
	storeDir := t.TempDir()
	s := NewStore(storeDir)
	id := "ses_cld_compact_1"
	if err := s.Put(SessionRecord{ID: id, Runtime: string(RuntimeClaudeCode), Title: "Compact"}); err != nil {
		t.Fatal(err)
	}
	s.UpsertTurnInfo(id, MessageInfo{ID: "m1", SessionID: id, Role: "assistant"})
	for i := 0; i < 10; i++ {
		s.UpsertTurnPart(id, MessagePart{
			ID:        "p1",
			SessionID: id,
			MessageID: "m1",
			Type:      "text",
			Text:      strings.Repeat("x", 1024*(i+1)),
		})
	}
	s.Flush()

	// Re-open cold store so turnsLoaded is false.
	cold := NewStore(storeDir)
	var visitedText string
	if !cold.VisitTurnsTransient(id, func(rec SessionRecord) {
		if len(rec.Turns) == 1 && len(rec.Turns[0].Parts) == 1 {
			visitedText = rec.Turns[0].Parts[0].Text
		}
	}) {
		t.Fatal("expected VisitTurnsTransient to find session")
	}
	if len(visitedText) != 10*1024 {
		t.Fatalf("expected latest text length 10240, got %d", len(visitedText))
	}
	cold.mu.RLock()
	cached := cold.cachedSessions
	cold.mu.RUnlock()
	if cached != 0 {
		t.Fatalf("VisitTurnsTransient must not pin cold sessions into LRU cache, got cachedSessions=%d", cached)
	}

	compacted, freed := cold.CompactIdleTurns()
	if compacted != 1 || freed <= 0 {
		t.Fatalf("expected 1 compacted session with freed > 0, got compacted=%d freed=%d", compacted, freed)
	}
}

func TestAppendPartDeltaAvoidsQuadraticAllocations(t *testing.T) {
	storeDir := t.TempDir()
	s := NewStore(storeDir)
	sid := "ses_cld_delta_alloc"
	if err := s.Put(SessionRecord{ID: sid, Runtime: string(RuntimeClaudeCode)}); err != nil {
		t.Fatal(err)
	}
	s.SetActive(sid, true)
	s.UpsertTurnInfo(sid, MessageInfo{ID: "msg_delta", SessionID: sid, Role: "assistant"})
	s.UpsertTurnPart(sid, MessagePart{ID: "prt_delta", MessageID: "msg_delta", SessionID: sid, Type: "text"})

	const (
		totalChars = 256_000
		chunkSize  = 16
		chunks     = totalChars / chunkSize
	)
	chunk := strings.Repeat("a", chunkSize)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < chunks; i++ {
		s.AppendPartDelta(sid, "msg_delta", "prt_delta", chunk)
	}
	runtime.ReadMemStats(&after)
	s.FlushSession(sid)
	s.SetActive(sid, false)

	allocatedMiB := float64(after.TotalAlloc-before.TotalAlloc) / (1 << 20)
	if allocatedMiB > 16 {
		t.Fatalf("AppendPartDelta allocated %.2f MiB for %d chunks (%d bytes), expected linear < 16 MiB", allocatedMiB, chunks, totalChars)
	}
	rec, ok := s.Get(sid)
	if !ok || len(rec.Turns) != 1 || len(rec.Turns[0].Parts) != 1 || len(rec.Turns[0].Parts[0].Text) != totalChars {
		t.Fatalf("unexpected final streamed text length = %d, want %d", len(rec.Turns[0].Parts[0].Text), totalChars)
	}
	reloaded, ok := NewStore(storeDir).Get(sid)
	if !ok || len(reloaded.Turns) != 1 || len(reloaded.Turns[0].Parts) != 1 || len(reloaded.Turns[0].Parts[0].Text) != totalChars {
		t.Fatalf("unexpected reloaded text length = %d, want %d", len(reloaded.Turns[0].Parts[0].Text), totalChars)
	}
}

func TestStoreRevisionTracksWritesNotReads(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "sessions"))
	start := store.Revision()
	if err := store.Put(SessionRecord{ID: "ses_rev", Title: "rev"}); err != nil {
		t.Fatal(err)
	}
	afterPut := store.Revision()
	if afterPut == start {
		t.Fatal("Put must advance the revision")
	}
	store.Get("ses_rev")
	store.Meta("ses_rev")
	store.VisitTurnsTransient("ses_rev", func(SessionRecord) {})
	if got := store.Revision(); got != afterPut {
		t.Fatalf("reads changed the revision: %d -> %d", afterPut, got)
	}
	store.UpsertTurnInfo("ses_rev", MessageInfo{ID: "msg_rev", SessionID: "ses_rev", Role: "assistant"})
	afterUpsert := store.Revision()
	if afterUpsert == afterPut {
		t.Fatal("UpsertTurnInfo must advance the revision")
	}
	if err := store.Delete("ses_rev"); err != nil {
		t.Fatal(err)
	}
	if store.Revision() == afterUpsert {
		t.Fatal("Delete must advance the revision")
	}
}

func TestSummaryTurnsCacheIsBoundedAndCleanedOnDelete(t *testing.T) {
	t.Parallel()
	s := NewStore(filepath.Join(t.TempDir(), "sessions"))
	s.cacheMaxSessions = 2
	s.summaryMaxSessions = 4

	countCachedSummaries := func() (acct int, actual int) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		acct = s.summarySessions
		for _, ss := range s.records {
			if ss.summaryTurns != nil {
				actual++
			}
		}
		return acct, actual
	}

	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("ses_cld_sum_%d", i)
		rec := SessionRecord{
			ID:      id,
			Runtime: string(RuntimeClaudeCode),
			Title:   fmt.Sprintf("Title %d", i),
			Turns: []MessageTurn{{
				Info: MessageInfo{ID: fmt.Sprintf("msg_%d", i), SessionID: id, Role: "assistant"},
				Parts: []MessagePart{{
					ID:        fmt.Sprintf("prt_%d", i),
					SessionID: id,
					MessageID: fmt.Sprintf("msg_%d", i),
					Type:      "tool",
					Tool:      "bash",
					Text:      strings.Repeat("payload-", 64),
					State:     &ToolState{Status: "completed", Title: fmt.Sprintf("Run %d", i), Output: "big output"},
				}},
			}},
		}
		if err := s.Put(rec); err != nil {
			t.Fatal(err)
		}
	}

	acct, actual := countCachedSummaries()
	if acct != actual || actual > s.summaryMaxSessions {
		t.Fatalf("after transcript evictions: summarySessions=%d actual=%d, want both <= %d", acct, actual, s.summaryMaxSessions)
	}

	// Visiting an evicted cold session reloads its summary accurately while respecting the cap.
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("ses_cld_sum_%d", i)
		var gotTitle, gotStatus string
		if !s.VisitTurnsSummary(id, func(rec SessionRecord) {
			if len(rec.Turns) == 1 && len(rec.Turns[0].Parts) == 1 && rec.Turns[0].Parts[0].State != nil {
				gotTitle = rec.Turns[0].Parts[0].State.Title
				gotStatus = rec.Turns[0].Parts[0].State.Status
			}
		}) {
			t.Fatalf("VisitTurnsSummary(%s) returned false", id)
		}
		if gotTitle != fmt.Sprintf("Run %d", i) || gotStatus != "completed" {
			t.Fatalf("VisitTurnsSummary(%s) = (%q, %q), want (%q, completed)", id, gotTitle, gotStatus, fmt.Sprintf("Run %d", i))
		}
	}

	acct, actual = countCachedSummaries()
	if acct != actual || actual > s.summaryMaxSessions {
		t.Fatalf("after VisitTurnsSummary sweep: summarySessions=%d actual=%d, want both <= %d", acct, actual, s.summaryMaxSessions)
	}

	// Deleting a session with a cached summary decrements summarySessions.
	for i := 0; i < 10; i++ {
		if err := s.Delete(fmt.Sprintf("ses_cld_sum_%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	acct, actual = countCachedSummaries()
	if acct != 0 || actual != 0 {
		t.Fatalf("after deleting all sessions: summarySessions=%d actual=%d, want 0", acct, actual)
	}
}
