package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/sqlitecli"
)

type captureNativeEngine struct {
	mu   sync.Mutex
	reqs []runner.NativeTurnRequest
}

func (m *captureNativeEngine) RunTurn(_ context.Context, req runner.NativeTurnRequest) error {
	m.mu.Lock()
	m.reqs = append(m.reqs, req)
	m.mu.Unlock()
	if req.Emit != nil {
		req.Emit(runner.NativeEvent{Kind: runner.NativeEventTextStart, Step: 1, PartID: "prt_nat_reply_1"})
		req.Emit(runner.NativeEvent{Kind: runner.NativeEventTextDelta, Step: 1, PartID: "prt_nat_reply_1", Text: "Continuing natively with prior ls -la tool context!"})
		req.Emit(runner.NativeEvent{Kind: runner.NativeEventTextEnd, Step: 1, PartID: "prt_nat_reply_1", Text: "Continuing natively with prior ls -la tool context!"})
		req.Emit(runner.NativeEvent{Kind: runner.NativeEventTurnFinish, Step: 1, Finish: "stop"})
	}
	return nil
}

func (m *captureNativeEngine) Summarize(_ context.Context, _, _, _ string) (string, error) {
	return "summary", nil
}

func TestDaemonlessHandoffDirectNativeImportAndContinue(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOXI_HOME", filepath.Join(homeDir, ".hoxi"))
	// Strip PATH so no external `opencode` or `sqlite3` binary can be invoked.
	t.Setenv("PATH", t.TempDir())

	projDir := filepath.Join(homeDir, "my-monorepo")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Seed Codex CLI state_5.sqlite + rollout JSONL using pure-Go SQLite.
	codexBase := filepath.Join(homeDir, ".codex")
	codexDir := filepath.Join(codexBase, "sessions", "2026", "11", "16")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rolloutLines := []string{
		fmt.Sprintf(`{"timestamp":"2026-11-16T10:00:00Z","type":"session_meta","payload":{"id":"codex-native-1","cwd":%q,"cli_version":"0.42.0","git":{"branch":"feat/native"}}}`, projDir),
		`{"timestamp":"2026-11-16T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Inspect gateway routing and fix handoff"}]}}`,
		`{"timestamp":"2026-11-16T10:00:02Z","type":"response_item","payload":{"type":"reasoning","summary":[{"text":"Checking materialize.go first."}]}}`,
		`{"timestamp":"2026-11-16T10:00:03Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call_cdx_1","arguments":"{\"cmd\":\"ls -la\"}"}}`,
		`{"timestamp":"2026-11-16T10:00:04Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_cdx_1","output":"gateway.go\nmaterialize.go"}}`,
		`{"timestamp":"2026-11-16T10:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":"Found materialize.go; ready to wire native store."}}`,
	}
	rolloutPath := filepath.Join(codexDir, "rollout-2026-11-16T10-00-00-codex-native-1.jsonl")
	if err := os.WriteFile(rolloutPath, []byte(strings.Join(rolloutLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDB := filepath.Join(codexBase, "state_5.sqlite")
	if err := sqlitecli.WithImmediateTx(context.Background(), stateDB, 5*time.Second, func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `
CREATE TABLE threads (
	id TEXT PRIMARY KEY, rollout_path TEXT, created_at INTEGER, updated_at INTEGER,
	cwd TEXT, title TEXT, git_branch TEXT, git_sha TEXT, model TEXT, first_user_message TEXT, archived INTEGER
);`); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx,
			`INSERT INTO threads VALUES ('codex-native-1', ?, 1763287200, 1763287260, ?, 'Codex Native Handoff', 'feat/native', 'abc12345', 'gpt-5-codex', 'Inspect gateway routing and fix handoff', 0);`,
			rolloutPath, projDir,
		)
		return err
	}); err != nil {
		t.Fatalf("seed codex state_5.sqlite: %v", err)
	}

	// 2. Also seed a Claude Code session JSONL to verify multi-CLI native handoff.
	encodedHint := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, projDir)
	claudeProjDir := filepath.Join(homeDir, ".claude", "projects", encodedHint)
	if err := os.MkdirAll(claudeProjDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeLines := []string{
		fmt.Sprintf(`{"type":"custom-title","customTitle":"Claude Native Handoff","cwd":%q,"gitBranch":"main","timestamp":"2026-11-16T10:00:00Z"}`, projDir),
		`{"type":"user","timestamp":"2026-11-16T10:00:01Z","message":{"content":[{"type":"text","text":"Fix system_prompt.go rule limit"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-16T10:00:03Z","message":{"model":"claude-sonnet-4-5","content":[{"type":"tool_use","id":"toolu_cld_1","name":"Edit","input":{"file_path":"system_prompt.go"}},{"type":"text","text":"Edited system_prompt.go."}]}}`,
		`{"type":"user","timestamp":"2026-11-16T10:00:04Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_cld_1","content":"Updated system_prompt.go"}]}}`,
	}
	if err := os.WriteFile(filepath.Join(claudeProjDir, "claude-native-1.jsonl"), []byte(strings.Join(claudeLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	gw := New(0, "", projDir, nil, nil)
	defer gw.CloseBrowser()

	eng := &captureNativeEngine{}
	gw.runner.SetNativeEngine(eng)

	doJSON := func(method, path string, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req := httptest.NewRequest(method, path, &buf)
		req.Host = "127.0.0.1:5400"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hoxi-Token", gw.GatewayToken())
		rec := httptest.NewRecorder()
		gw.ServeHTTP(rec, req)
		return rec
	}

	// Verify preview reports TargetRuntime="native" without OpenCode.
	prevRec := doJSON(http.MethodGet, "/api/hoxi/handoff/preview?source=codex-cli&id=codex-native-1", nil)
	if prevRec.Code != http.StatusOK {
		t.Fatalf("preview failed: %d %s", prevRec.Code, prevRec.Body.String())
	}
	var prevDoc struct {
		Fidelity struct {
			TargetRuntime     string `json:"targetRuntime"`
			RetainedToolCalls int    `json:"retainedToolCalls"`
		} `json:"fidelity"`
	}
	if err := json.Unmarshal(prevRec.Body.Bytes(), &prevDoc); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if prevDoc.Fidelity.TargetRuntime != "native" || prevDoc.Fidelity.RetainedToolCalls != 1 {
		t.Fatalf("expected targetRuntime=native and 1 tool call, got %+v", prevDoc.Fidelity)
	}

	// Import both Codex and Claude Code sessions directly into Hoxi native store!
	for _, tc := range []struct {
		src, id, prov, mod, wantCallID string
	}{
		{"codex-cli", "codex-native-1", "openai", "gpt-5-mini", "call_cdx_1"},
		{"claude-code", "claude-native-1", "anthropic", "claude-sonnet-4-5", "toolu_cld_1"},
	} {
		impRec := doJSON(http.MethodPost, "/api/hoxi/handoff/import", map[string]any{
			"source":              tc.src,
			"sessionID":           tc.id,
			"targetDirectory":     projDir,
			"preferredProviderID": tc.prov,
			"preferredModelID":    tc.mod,
		})
		if impRec.Code != http.StatusOK {
			t.Fatalf("import %s HTTP %d: %s", tc.src, impRec.Code, impRec.Body.String())
		}
		var impResp struct {
			OK      bool           `json:"ok"`
			Error   string         `json:"error"`
			Session map[string]any `json:"session"`
		}
		if err := json.Unmarshal(impRec.Body.Bytes(), &impResp); err != nil || !impResp.OK {
			t.Fatalf("handoff import %s failed without OpenCode: %+v (raw=%s)", tc.src, impResp, impRec.Body.String())
		}
		newSid, _ := impResp.Session["id"].(string)
		if !strings.HasPrefix(newSid, "ses_nat_") || impResp.Session["runtime"] != "native" {
			t.Fatalf("expected native ses_nat_ session for %s, got %+v", tc.src, impResp.Session)
		}
		if projID, _ := impResp.Session["projectID"].(string); strings.TrimSpace(projID) == "" {
			t.Fatalf("expected non-empty projectID attribution on imported session: %+v", impResp.Session)
		}

		// Verify opencode.db was NEVER created!
		if _, err := os.Stat(filepath.Join(homeDir, ".hoxi", "engine-data", "opencode", "opencode.db")); !os.IsNotExist(err) {
			t.Fatalf("expected zero opencode.db creation during native handoff import")
		}

		// Verify imported message & tool call history via unified /api/hoxi/sessions/<id>/message.
		msgRec := doJSON(http.MethodGet, "/api/hoxi/sessions/"+newSid+"/message?directory="+projDir, nil)
		if msgRec.Code != http.StatusOK || !strings.Contains(msgRec.Body.String(), tc.wantCallID) {
			t.Fatalf("imported %s transcript missing tool call %s: %d %s", tc.src, tc.wantCallID, msgRec.Code, msgRec.Body.String())
		}

		if tc.src == "codex-cli" {
			// Continue working in the handed-off session on NativeEngine!
			promptRec := doJSON(http.MethodPost, "/api/hoxi/sessions/"+newSid+"/prompt?directory="+projDir, map[string]any{
				"text":  "Now proceed with step 2",
				"model": map[string]string{"providerID": tc.prov, "modelID": tc.mod},
			})
			if promptRec.Code != http.StatusOK {
				t.Fatalf("continue prompt failed: %d %s", promptRec.Code, promptRec.Body.String())
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if !gw.runner.IsSessionBusy(newSid) {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}

	eng.mu.Lock()
	gotCalls := len(eng.reqs)
	var firstReq runner.NativeTurnRequest
	if gotCalls > 0 {
		firstReq = eng.reqs[0]
	}
	eng.mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("expected 1 native engine turn, got %d", gotCalls)
	}
	histRaw, _ := json.Marshal(firstReq.History)
	if !strings.Contains(string(histRaw), "Inspect gateway routing") ||
		!strings.Contains(string(histRaw), "call_cdx_1") ||
		!strings.Contains(string(histRaw), "materialize.go") {
		t.Fatalf("expected native turn history to include imported user prompt and tool call/result, got: %s", string(histRaw))
	}

	// Also verify native /lsp, /provider/auth, and /config/providers on this OpenCode-free gateway!
	if err := os.WriteFile(filepath.Join(projDir, "tsconfig.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lspRec := doJSON(http.MethodGet, "/lsp?directory="+projDir, nil)
	if lspRec.Code != http.StatusOK || !strings.Contains(lspRec.Body.String(), "typescript") {
		t.Fatalf("expected native /lsp to report TypeScript language diagnostics entry, got %d %s", lspRec.Code, lspRec.Body.String())
	}
	pAuthRec := doJSON(http.MethodGet, "/provider/auth", nil)
	if pAuthRec.Code != http.StatusOK || !strings.Contains(pAuthRec.Body.String(), "openrouter") {
		t.Fatalf("expected native /provider/auth without OpenCode, got %d %s", pAuthRec.Code, pAuthRec.Body.String())
	}
	cfgProvRec := doJSON(http.MethodGet, "/config/providers", nil)
	if cfgProvRec.Code != http.StatusOK || !strings.Contains(cfgProvRec.Body.String(), "default") {
		t.Fatalf("expected native /config/providers without OpenCode, got %d %s", cfgProvRec.Code, cfgProvRec.Body.String())
	}
}
