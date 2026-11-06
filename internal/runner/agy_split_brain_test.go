package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/testutil/execstub"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func requireSQLite3(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not available")
	}
	return path
}

// writeAgySQLiteStore creates a SQLite store with agy's steps table holding the
// given number of rows, matching the empty shell agy leaves on a split.
func writeAgySQLiteStore(t *testing.T, sqlite, dbPath string, steps int) {
	t.Helper()
	sql := "PRAGMA journal_mode=WAL;" +
		"CREATE TABLE trajectory_meta (trajectory_id text PRIMARY KEY, cascade_id text);" +
		"INSERT INTO trajectory_meta VALUES ('t1', 'c1');" +
		"CREATE TABLE steps (idx integer PRIMARY KEY, step_type integer NOT NULL DEFAULT 0, step_payload blob);"
	for i := 0; i < steps; i++ {
		sql += "INSERT INTO steps (idx, step_type) VALUES (" + strconv.Itoa(i) + ", 15);"
	}
	if out, err := exec.Command(sqlite, dbPath, sql).CombinedOutput(); err != nil {
		t.Fatalf("create sqlite store: %v (%s)", err, out)
	}
}

func writeAgyProtobufStore(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("proto-trajectory-data"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestQuarantineEmptyAgySQLiteStore(t *testing.T) {
	sqlite := requireSQLite3(t)

	t.Run("empty store is moved aside with its sidecars", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("GEMINI_HOME", home)
		convDir := filepath.Join(home, "antigravity-cli", "conversations")
		if err := os.MkdirAll(convDir, 0o755); err != nil {
			t.Fatal(err)
		}
		pbPath := filepath.Join(convDir, "conv-empty.pb")
		dbPath := filepath.Join(convDir, "conv-empty.db")
		writeAgyProtobufStore(t, pbPath)
		writeAgySQLiteStore(t, sqlite, dbPath, 0)
		if !hasAgySplitBrainConversation("conv-empty") {
			t.Fatal("expected the fixture to be a split conversation")
		}

		if !quarantineEmptyAgySQLiteStore("conv-empty") {
			t.Fatal("expected the empty SQLite store to be quarantined")
		}
		if hasAgySplitBrainConversation("conv-empty") {
			t.Fatal("expected the conversation to no longer be split")
		}
		for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("expected %s to be moved aside, stat err=%v", p, err)
			}
		}
		if !nonEmptyRegularFile(pbPath) {
			t.Fatal("expected the Protobuf store to be untouched")
		}
		backups, _ := filepath.Glob(filepath.Join(convDir, "conv-empty.db*"+agyEmptyStoreSuffix+"*"))
		if len(backups) != 3 {
			t.Fatalf("expected the store, WAL and shared-memory files to be kept as backups, got %v", backups)
		}
	})

	t.Run("stores that cannot be proven empty stay in place", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("GEMINI_HOME", home)
		convDir := filepath.Join(home, "antigravity-cli", "conversations")
		if err := os.MkdirAll(convDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeAgyProtobufStore(t, filepath.Join(convDir, "conv-steps.pb"))
		writeAgySQLiteStore(t, sqlite, filepath.Join(convDir, "conv-steps.db"), 2)
		writeAgyProtobufStore(t, filepath.Join(convDir, "conv-stub.pb"))
		if err := os.WriteFile(filepath.Join(convDir, "conv-stub.db"), []byte("not a sqlite database"), 0o600); err != nil {
			t.Fatal(err)
		}

		for _, id := range []string{"conv-steps", "conv-stub"} {
			if quarantineEmptyAgySQLiteStore(id) {
				t.Fatalf("%s: expected no quarantine", id)
			}
			if !nonEmptyRegularFile(filepath.Join(convDir, id+".db")) {
				t.Fatalf("%s: expected the SQLite store to stay in place", id)
			}
		}
	})

	t.Run("missing sqlite3 keeps the store", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("GEMINI_HOME", home)
		convDir := filepath.Join(home, "antigravity-cli", "conversations")
		if err := os.MkdirAll(convDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeAgyProtobufStore(t, filepath.Join(convDir, "conv-nocli.pb"))
		writeAgySQLiteStore(t, sqlite, filepath.Join(convDir, "conv-nocli.db"), 0)
		orig := countAgyStoreSteps
		countAgyStoreSteps = func(string) (int, error) { return 0, errors.New("sqlite3: not found") }
		t.Cleanup(func() { countAgyStoreSteps = orig })

		if quarantineEmptyAgySQLiteStore("conv-nocli") {
			t.Fatal("expected no quarantine without sqlite3")
		}
		if !nonEmptyRegularFile(filepath.Join(convDir, "conv-nocli.db")) {
			t.Fatal("expected the SQLite store to stay in place")
		}
	})
}

func TestRunnerAgySplitBrainEmptyStoreResumesSameConversation(t *testing.T) {
	sqlite := requireSQLite3(t)
	repoDir := initTestGitRepo(t)
	svc := NewService(repoDir, worktree.NewManager(repoDir), statusbar.NewTracker(), filepath.Join(t.TempDir(), "runner-sessions.json"))
	defer svc.CloseAll()
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	createBody, _ := json.Marshal(map[string]string{
		"runtime":     "antigravity",
		"title":       "Empty Store Split-Brain Test",
		"modelID":     "gemini-4-argon-high",
		"directory":   repoDir,
		"sandboxMode": "worktree",
	})
	recCreate := httptest.NewRecorder()
	mux.ServeHTTP(recCreate, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions", bytes.NewReader(createBody)))
	if recCreate.Code != http.StatusOK {
		t.Fatalf("create session failed (%d): %s", recCreate.Code, recCreate.Body.String())
	}
	var created SessionInfo
	if err := json.Unmarshal(recCreate.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	agyDir := filepath.Join(home, "bin")
	convDir := filepath.Join(home, "antigravity-cli", "conversations")
	for _, dir := range []string{agyDir, convDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, _, _ = svc.store.Update(created.ID, func(r *SessionRecord) {
		r.ExternalID = "conv-split-empty"
		r.ContextPending = false
		r.Turns = []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_u_1", SessionID: created.ID, Role: "user"},
				Parts: []MessagePart{{ID: "prt_u_1", Type: "text", Text: "规划数据库备份方案"}},
			},
			{
				Info:  MessageInfo{ID: "msg_a_1", SessionID: created.ID, Role: "assistant"},
				Parts: []MessagePart{{ID: "prt_a_1", Type: "text", Text: "PLAN_BODY_MARKER"}},
			},
		}
	})
	writeAgyProtobufStore(t, filepath.Join(convDir, "conv-split-empty.pb"))
	writeAgySQLiteStore(t, sqlite, filepath.Join(convDir, "conv-split-empty.db"), 0)

	argsFile := filepath.Join(created.Directory, ".split_args")
	stdinFile := filepath.Join(created.Directory, ".split_stdin")
	t.Setenv("GEMINI_HOME", home)
	t.Setenv("PATH", agyDir+string(os.PathListSeparator)+filepath.Dir(sqlite)+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("TEST_SPLIT_ARGS", argsFile)
	t.Setenv("TEST_SPLIT_STDIN", stdinFile)
	execstub.Write(t, agyDir, "agy", `#!/bin/sh
printf '%s\n' "$@" > "$TEST_SPLIT_ARGS"
cat > "$TEST_SPLIT_STDIN"
printf '%s\n' '{"conversation_id":"conv-split-empty","event":"init","init":{"model":"gemini-4-argon-high","permission_mode":"accept-edits"}}'
printf '%s\n' '{"conversation_id":"conv-split-empty","event":"step_update","step_update":{"conversation_id":"conv-split-empty","state":"DONE","step_index":1,"step_type":"agent_response","text_delta":"RESUMED"}}'
printf '%s\n' '{"conversation_id":"conv-split-empty","event":"result","result":{"conversation_id":"conv-split-empty","response":"RESUMED","status":"DONE"}}'
`)

	ch, unsub := svc.Subscribe(created.ID)
	defer unsub()
	recPrompt := httptest.NewRecorder()
	mux.ServeHTTP(recPrompt, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+created.ID+"/prompt", strings.NewReader(`{"text":"开始做"}`)))
	if recPrompt.Code != http.StatusOK {
		t.Fatalf("prompt failed (%d): %s", recPrompt.Code, recPrompt.Body.String())
	}
	deadline := time.After(5 * time.Second)
	for idle := false; !idle; {
		select {
		case evt := <-ch:
			idle = evt.Type == "session.idle"
		case <-deadline:
			t.Fatal("timed out waiting for session.idle")
		}
	}

	argsRaw, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(argsRaw), "--conversation\nconv-split-empty") {
		t.Fatalf("expected the turn to resume --conversation conv-split-empty, got args:\n%s", argsRaw)
	}
	stdinRaw, _ := os.ReadFile(stdinFile)
	if strings.Contains(string(stdinRaw), "Continue the existing conversation") || strings.Contains(string(stdinRaw), "PLAN_BODY_MARKER") {
		t.Fatalf("expected no history replay when resuming the intact conversation, got stdin:\n%s", stdinRaw)
	}
	if rec, ok := svc.store.Get(created.ID); !ok || rec.ExternalID != "conv-split-empty" || rec.ContextPending {
		t.Fatalf("expected the session to keep its conversation, got externalID=%q contextPending=%v", rec.ExternalID, rec.ContextPending)
	}
	if hasAgySplitBrainConversation("conv-split-empty") {
		t.Fatal("expected the empty SQLite store to be moved aside")
	}
}
