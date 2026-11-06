package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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

func seedStore(t *testing.T, dir string) *Store {
	t.Helper()
	store := NewStore(dir)
	if err := store.Put(SessionRecord{ID: "ses_cld_1", Runtime: "claude-code", ProjectDir: "/repo", Title: "first"}); err != nil {
		t.Fatal(err)
	}
	store.UpsertTurnInfo("ses_cld_1", MessageInfo{ID: "msg_1", SessionID: "ses_cld_1", Role: "assistant"})
	store.UpsertTurnPart("ses_cld_1", MessagePart{ID: "prt_1", SessionID: "ses_cld_1", MessageID: "msg_1", Type: "text"})
	store.AppendPartDelta("ses_cld_1", "msg_1", "prt_1", "hello ")
	store.AppendPartDelta("ses_cld_1", "msg_1", "prt_1", "world")
	return store
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
	if reopened.records["ses_cld_1"].turnsLoaded {
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
	store.migrateLegacy(legacy)
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

func TestRunnerMultiRuntimeMemoryInjectionAndSync(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	storeDir := t.TempDir()
	svc := NewService(workDir, nil, nil, storeDir)

	rec := SessionRecord{
		ID:           "ses_hoxi_mem_1",
		Runtime:      string(RuntimeClaudeCode),
		ProjectDir:   workDir,
		WorktreePath: workDir,
		SandboxMode:  "local",
		ModelID:      "claude-sonnet-4-6",
		Branch:       "main",
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_u1", SessionID: "ses_hoxi_mem_1", Role: "user"},
				Parts: []MessagePart{{ID: "prt_u1", SessionID: "ses_hoxi_mem_1", MessageID: "msg_u1", Type: "text", Text: "Optimize runner memory sync"}},
			},
			{
				Info: MessageInfo{ID: "msg_a1", SessionID: "ses_hoxi_mem_1", Role: "assistant"},
				Parts: []MessagePart{
					{
						ID: "prt_t1", SessionID: "ses_hoxi_mem_1", MessageID: "msg_a1", Type: "tool", Tool: "edit",
						State: &ToolState{Status: "completed", Input: map[string]interface{}{"filePath": "internal/runner/memory.go"}},
					},
					{
						ID: "prt_txt1", SessionID: "ses_hoxi_mem_1", MessageID: "msg_a1", Type: "text", Text: "Implemented 8-dimension memory sync.",
					},
				},
			},
		},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	memDir := filepath.Join(workDir, ".hoxi", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "project.md"), []byte("# Project\n- Real project rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc.syncSessionMemoryFile(rec.ID)

	sessMemPath := filepath.Join(workDir, ".hoxi", "memory", "sessions", rec.ID+".md")
	rawSess, err := os.ReadFile(sessMemPath)
	if err != nil {
		t.Fatalf("expected session memory file created: %v", err)
	}
	if !strings.Contains(string(rawSess), "## 1. Primary Request and Intent") || !strings.Contains(string(rawSess), "internal/runner/memory.go") {
		t.Fatalf("unexpected session memory content: %s", string(rawSess))
	}
	if _, err := os.Stat(filepath.Join(workDir, ".opencode")); !os.IsNotExist(err) {
		t.Fatalf("memory sync must never create .opencode, stat err = %v", err)
	}

	injected := runtimeMemoryContext(rec)
	if !strings.Contains(injected, "<project_memory>") || !strings.Contains(injected, "<session_handoff_memory>") {
		t.Fatalf("expected project and session memory blocks, got: %s", injected)
	}
	if strings.Contains(injected, "Updated:") {
		t.Fatalf("expected volatile Updated timestamp stripped from injected context: %s", injected)
	}

	// Once ExternalID is set and ContextPending is false, native resume takes over without re-injecting
	rec.ExternalID = "claude-ext-1"
	rec.ContextPending = false
	if got := runtimeMemoryContext(rec); got != "" {
		t.Fatalf("expected empty memory injection on native resume turn, got: %s", got)
	}
}

func TestSyncSessionMemoryNeverSeedsPlaceholderProjectMemory(t *testing.T) {
	t.Parallel()
	projDir := t.TempDir()
	workDir := t.TempDir()
	svc := NewService(projDir, nil, nil, t.TempDir())
	rec := SessionRecord{
		ID:           "ses_hoxi_mem_seed",
		Runtime:      string(RuntimeClaudeCode),
		ProjectDir:   projDir,
		WorktreePath: workDir,
		Turns: []MessageTurn{{
			Info:  MessageInfo{ID: "msg_u1", SessionID: "ses_hoxi_mem_seed", Role: "user"},
			Parts: []MessagePart{{ID: "prt_u1", SessionID: "ses_hoxi_mem_seed", MessageID: "msg_u1", Type: "text", Text: "hi"}},
		}},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(rec.ID)
	projFile := filepath.Join(projDir, ".hoxi", "memory", "project.md")
	if _, err := os.Stat(projFile); !os.IsNotExist(err) {
		t.Fatalf("project.md must not be seeded when the project has none, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(projDir, ".opencode")); !os.IsNotExist(err) {
		t.Fatalf("memory sync must never create .opencode, stat err = %v", err)
	}
	if got := runtimeMemoryContext(rec); strings.Contains(got, "<project_memory>") {
		t.Fatalf("expected no project memory block, got: %s", got)
	}

	// A pre-migration .opencode/memory/project.md is still honored and copied
	// forward once so the user does not lose long-lived project rules.
	legacyProj := filepath.Join(projDir, ".opencode", "memory", "project.md")
	if err := os.MkdirAll(filepath.Dir(legacyProj), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyProj, []byte("# Project\n- Real rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(rec.ID)
	raw, err := os.ReadFile(projFile)
	if err != nil || !strings.Contains(string(raw), "Real rule") {
		t.Fatalf("expected legacy project.md migrated to .hoxi/memory, got %q, %v", raw, err)
	}
}

func TestRuntimeMemoryContextReadsLegacyOpencodeMemory(t *testing.T) {
	t.Parallel()
	projDir := t.TempDir()
	legacySessDir := filepath.Join(projDir, ".opencode", "memory", "sessions")
	if err := os.MkdirAll(legacySessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacySessDir, "ses_legacy.md"), []byte("## 8. Next Steps\n- Resume legacy work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := SessionRecord{ID: "ses_legacy", ProjectDir: projDir, WorktreePath: projDir}
	got := runtimeMemoryContext(rec)
	if !strings.Contains(got, "Resume legacy work") {
		t.Fatalf("expected legacy session memory to be injected, got: %s", got)
	}
}

func TestProjectMemoryRootForIsolatedSession(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	wt := filepath.Join(root, ".hoxi", "worktrees", "ses_wt")
	svc := NewService(root, nil, nil, t.TempDir())
	rec := SessionRecord{ID: "ses_wt", ProjectDir: root, WorktreePath: wt, SandboxMode: "worktree"}
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	rec.Turns = []MessageTurn{{
		Info:  MessageInfo{ID: "msg_u1", SessionID: "ses_wt", Role: "user"},
		Parts: []MessagePart{{ID: "prt_u1", SessionID: "ses_wt", MessageID: "msg_u1", Type: "text", Text: "hello"}},
	}}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(rec.ID)
	if _, err := os.Stat(filepath.Join(root, ".hoxi", "memory", "sessions", "ses_wt.md")); err != nil {
		t.Fatalf("isolated session memory must live at the project root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".hoxi", "memory")); !os.IsNotExist(err) {
		t.Fatalf("worktree must not hold its own memory copy, stat err = %v", err)
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

func TestDeleteConversationNotifiesRootAndSubagentChildren(t *testing.T) {
	repo := t.TempDir()
	svc := NewService(repo, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	var deleted []string
	svc.SetSessionDeletedHook(func(id string) {
		if _, ok := svc.Store().Meta(id); ok {
			t.Errorf("hook for %s ran before its transcript was deleted", id)
		}
		deleted = append(deleted, id)
	})
	for _, rec := range []SessionRecord{
		{ID: "ses_cld_root", Runtime: string(RuntimeClaudeCode), ProjectDir: repo, WorktreePath: repo},
		{ID: "ses_cld_child", Runtime: string(RuntimeClaudeCode), ProjectDir: repo, WorktreePath: repo, ParentID: "ses_cld_root", Subagent: true},
		{ID: "ses_cld_other", Runtime: string(RuntimeClaudeCode), ProjectDir: repo, WorktreePath: repo},
	} {
		if err := svc.Store().Put(rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.DeleteConversation(context.Background(), "ses_cld_root"); err != nil {
		t.Fatal(err)
	}
	sort.Strings(deleted)
	if strings.Join(deleted, ",") != "ses_cld_child,ses_cld_root" {
		t.Fatalf("hook calls = %v, want root and its subagent child", deleted)
	}
	if _, ok := svc.Store().Meta("ses_cld_other"); !ok {
		t.Fatal("unrelated sessions must survive")
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
