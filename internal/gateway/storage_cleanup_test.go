package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/codeindex"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/workspace"
)

func TestSystemStorageInspectAndClean(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOXI_HOME", home)
	wsRoot := t.TempDir()

	// 1. Seed SQLite hoxi.db with transient event bloat + durable session/message rows.
	dbPath := hoxipaths.DBPath()
	seedSQL := `
CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT);
CREATE TABLE event (id INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT, data TEXT);
CREATE TABLE event_sequence (seq INTEGER);
INSERT INTO session (id, title) VALUES ('ses_keep', 'Keep Me');
INSERT INTO message (id, session_id, data) VALUES ('msg_keep', 'ses_keep', '{"role":"assistant"}');
`
	if out, err := exec.Command("sqlite3", dbPath, seedSQL).CombinedOutput(); err != nil {
		t.Fatalf("seed sqlite: %v: %s", err, out)
	}
	var insertEvents strings.Builder
	insertEvents.WriteString("BEGIN;")
	for i := 0; i < 32; i++ {
		insertEvents.WriteString("INSERT INTO event (type, data) VALUES ('message.updated.1', hex(zeroblob(32768)));")
	}
	insertEvents.WriteString("INSERT INTO event_sequence (seq) VALUES (32); COMMIT;")
	if out, err := exec.Command("sqlite3", dbPath, insertEvents.String()).CombinedOutput(); err != nil {
		t.Fatalf("insert events: %v: %s", err, out)
	}

	// 2. Seed runner session store with redundant turns.jsonl upserts + leftover .tmp file.
	sessionsDir := filepath.Join(home, "sessions")
	runnerSvc := runner.NewService(wsRoot, nil, nil, sessionsDir)
	sessID := "ses_cld_storage_test"
	if err := runnerSvc.Store().Put(runner.SessionRecord{ID: sessID, Runtime: "claude-code", Title: "Storage"}); err != nil {
		t.Fatal(err)
	}
	runnerSvc.Store().UpsertTurnInfo(sessID, runner.MessageInfo{ID: "m1", SessionID: sessID, Role: "assistant"})
	for i := 0; i < 12; i++ {
		runnerSvc.Store().UpsertTurnPart(sessID, runner.MessagePart{
			ID:        "p1",
			SessionID: sessID,
			MessageID: "m1",
			Type:      "text",
			Text:      strings.Repeat("T", 2048*(i+1)),
		})
	}
	runnerSvc.Store().Flush()
	if err := os.WriteFile(filepath.Join(sessionsDir, sessID, "turns.jsonl.tmp"), []byte(strings.Repeat("X", 4096)), 0o600); err != nil {
		t.Fatal(err)
	}

	// 3. Seed engine logs & tool-output.
	logFile := filepath.Join(home, "engine-data", "opencode", "log", "2026-01-01.log")
	toolFile := filepath.Join(home, "engine-data", "opencode", "tool-output", "tool_abc")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(toolFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logFile, []byte(strings.Repeat("L", 16*1024)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toolFile, []byte(strings.Repeat("O", 16*1024)), 0o600); err != nil {
		t.Fatal(err)
	}

	// 4. Seed code-index cache + read-only git tmp_pack_* in workspace.
	codeIndexDir := filepath.Join(home, "code-index")
	if err := os.MkdirAll(codeIndexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codeIndexDir, "cached.json"), []byte(strings.Repeat("C", 8192)), 0o600); err != nil {
		t.Fatal(err)
	}
	packDir := filepath.Join(wsRoot, ".git", "objects", "pack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpPack := filepath.Join(packDir, "tmp_pack_aborted123")
	if err := os.WriteFile(tmpPack, []byte(strings.Repeat("P", 32*1024)), 0o444); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{
		rootDir:    wsRoot,
		mux:        http.NewServeMux(),
		runner:     runnerSvc,
		workspaces: workspace.NewStore(wsRoot),
		codeIndex:  codeindex.NewService(codeIndexDir),
	}
	g.mux.HandleFunc("/api/hoxi/system/storage", g.handleSystemStorage)
	g.mux.HandleFunc("/api/hoxi/system/storage/clean", g.handleSystemStorageClean)

	// Inspect before clean.
	req := httptest.NewRequest(http.MethodGet, "/api/hoxi/system/storage", nil)
	rec := httptest.NewRecorder()
	g.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/hoxi/system/storage status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var before SystemStorageStats
	if err := json.Unmarshal(rec.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.DatabaseBytes < 2*1024*1024 {
		t.Fatalf("expected DatabaseBytes >= 2MB before clean, got %d", before.DatabaseBytes)
	}
	if before.EngineLogsBytes < 32*1024 {
		t.Fatalf("expected EngineLogsBytes >= 32KB before clean, got %d", before.EngineLogsBytes)
	}
	if before.CacheIndexBytes < 40*1024 {
		t.Fatalf("expected CacheIndexBytes >= 40KB before clean, got %d", before.CacheIndexBytes)
	}

	// Execute clean.
	cleanReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/system/storage/clean", nil)
	cleanRec := httptest.NewRecorder()
	g.mux.ServeHTTP(cleanRec, cleanReq)
	if cleanRec.Code != http.StatusOK {
		t.Fatalf("POST /api/hoxi/system/storage/clean status = %d, body = %s", cleanRec.Code, cleanRec.Body.String())
	}
	var result SystemStorageCleanResult
	if err := json.Unmarshal(cleanRec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.FreedBytes < 1500*1024 {
		t.Fatalf("expected FreedBytes >= 1.5MB, got %d", result.FreedBytes)
	}
	if result.CompactedSessions != 1 {
		t.Fatalf("expected CompactedSessions = 1, got %d", result.CompactedSessions)
	}
	if result.Storage.EngineLogsBytes != 0 {
		t.Fatalf("expected EngineLogsBytes = 0 after clean, got %d", result.Storage.EngineLogsBytes)
	}
	if result.Storage.CacheIndexBytes != 0 {
		t.Fatalf("expected CacheIndexBytes = 0 after clean, got %d", result.Storage.CacheIndexBytes)
	}
	if _, err := os.Stat(tmpPack); !os.IsNotExist(err) {
		t.Fatalf("expected read-only tmp_pack_* removed, stat err = %v", err)
	}

	// Verify durable session & message rows in hoxi.db are preserved.
	out, err := exec.Command("sqlite3", dbPath, "SELECT COUNT(*) FROM session; SELECT COUNT(*) FROM message; SELECT COUNT(*) FROM event;").CombinedOutput()
	if err != nil {
		t.Fatalf("verify sqlite after clean: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "1\n1\n0" {
		t.Fatalf("expected session=1, message=1, event=0 after clean, got %q", got)
	}

	// Verify runner session turn content is preserved after compaction.
	gotRec, ok := runnerSvc.Store().Get(sessID)
	if !ok || len(gotRec.Turns) != 1 || len(gotRec.Turns[0].Parts) != 1 || len(gotRec.Turns[0].Parts[0].Text) != 12*2048 {
		t.Fatalf("expected compacted session turn preserved intact, got ok=%v turns=%d", ok, len(gotRec.Turns))
	}

	// Verify remote callers are rejected with 403 Forbidden (both allow-list and handler defense-in-depth).
	tunnelReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/system/storage/clean", nil)
	tunnelRec := httptest.NewRecorder()
	g.serveTunnelRequest(tunnelRec, tunnelReq)
	if tunnelRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 from tunnel allow-list, got %d", tunnelRec.Code)
	}

	remoteReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/system/storage/clean", nil)
	remoteReq = remoteReq.WithContext(context.WithValue(remoteReq.Context(), remoteSourceKey{}, remoteSourceTunnel))
	remoteRec := httptest.NewRecorder()
	g.handleSystemStorageClean(remoteRec, remoteReq)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for remote storage clean, got %d", remoteRec.Code)
	}
}
