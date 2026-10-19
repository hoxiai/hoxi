package runner

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
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
	dir := t.TempDir()
	seedStore(t, dir).Flush()
	f, err := os.OpenFile(filepath.Join(dir, "ses_cld_1", sessionTurnsFile), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"part":{"id":"prt_2","messageID":"msg_1","ty`)
	_ = f.Close()
	rec, ok := NewStore(dir).Get("ses_cld_1")
	if !ok || len(rec.Turns[0].Parts) != 1 || rec.Turns[0].Parts[0].Text != "hello world" {
		t.Fatalf("torn line broke replay: %+v", rec.Turns)
	}
}

func TestStoreRejectsUnsafeIDsAndDeletesDirectory(t *testing.T) {
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

	svc.syncSessionMemoryFile(rec.ID)

	sessMemPath := filepath.Join(workDir, ".opencode", "memory", "sessions", rec.ID+".md")
	rawSess, err := os.ReadFile(sessMemPath)
	if err != nil {
		t.Fatalf("expected session memory file created: %v", err)
	}
	if !strings.Contains(string(rawSess), "## 1. Primary Request and Intent") || !strings.Contains(string(rawSess), "internal/runner/memory.go") {
		t.Fatalf("unexpected session memory content: %s", string(rawSess))
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

