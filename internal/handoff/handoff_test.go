package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/provider"
)

func setupMockOpenCodeServerAndDB(t *testing.T) (int, string, func()) {
	t.Helper()
	// Keep the fixture isolated from Hoxi's process-level database overrides.
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", "")
	xdgDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdgDir)
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	dbPath := filepath.Join(xdgDir, "opencode", "opencode.db")
	runSQLite(t, dbPath, `
CREATE TABLE session (
	id TEXT PRIMARY KEY,
	title TEXT,
	time_created INTEGER,
	time_updated INTEGER,
	data TEXT
);
CREATE TABLE message (
	id TEXT PRIMARY KEY,
	session_id TEXT,
	time_created INTEGER,
	time_updated INTEGER,
	data TEXT
);
CREATE TABLE part (
	id TEXT PRIMARY KEY,
	message_id TEXT,
	session_id TEXT,
	time_created INTEGER,
	time_updated INTEGER,
	data TEXT
);
`)

	seq := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		seq++
		sID := "ses_mock_" + strconv.Itoa(seq)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":        sID,
			"title":     body["title"],
			"directory": r.URL.Query().Get("directory"),
		})
	})
	mux.HandleFunc("/auth/openai", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	})
	mux.HandleFunc("/auth/google", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	})
	mux.HandleFunc("/global/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{})
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen 127.0.0.1:0 failed: %v", err)
	}
	srv := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: mux},
	}
	srv.Start()
	port := listener.Addr().(*net.TCPAddr).Port
	return port, dbPath, srv.Close
}

func TestHandoffSessionsAndPreviewEndpoints(t *testing.T) {
	workspaceDir := t.TempDir()
	setupSyntheticCLIHome(t, workspaceDir)

	svc := NewService(0, "testpass", workspaceDir, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// Method not allowed on GET-only endpoints
	for _, ep := range []string{"/api/hoxi/handoff/sessions", "/api/hoxi/handoff/preview"} {
		req := httptest.NewRequest(http.MethodPost, ep, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 for POST %s, got %d", ep, rec.Code)
		}
	}

	// 1. All sessions
	reqAll := httptest.NewRequest(http.MethodGet, "/api/hoxi/handoff/sessions", nil)
	recAll := httptest.NewRecorder()
	mux.ServeHTTP(recAll, reqAll)
	if recAll.Code != http.StatusOK {
		t.Fatalf("expected 200 from sessions, got %d", recAll.Code)
	}
	var allResp HandoffSessionsResponse
	if err := json.Unmarshal(recAll.Body.Bytes(), &allResp); err != nil {
		t.Fatalf("unmarshal sessions: %v", err)
	}
	if len(allResp.Sessions) != 8 {
		t.Fatalf("expected 8 sessions across all 8 sources, got %d", len(allResp.Sessions))
	}

	// 2. Filter by source, query, onlyCurrent, limit
	reqFiltered := httptest.NewRequest(http.MethodGet, "/api/hoxi/handoff/sessions?source=claude-code&query=Refactor&onlyCurrent=true&limit=1", nil)
	recFiltered := httptest.NewRecorder()
	mux.ServeHTTP(recFiltered, reqFiltered)
	var filtResp HandoffSessionsResponse
	_ = json.Unmarshal(recFiltered.Body.Bytes(), &filtResp)
	if len(filtResp.Sessions) != 1 || filtResp.Sessions[0].Source != ExternalToolClaude {
		t.Fatalf("unexpected filtered sessions: %+v", filtResp.Sessions)
	}

	// Filter onlyCurrent=1 across all sources
	reqCur := httptest.NewRequest(http.MethodGet, "/api/hoxi/handoff/sessions?onlyCurrent=1&limit=2", nil)
	recCur := httptest.NewRecorder()
	mux.ServeHTTP(recCur, reqCur)
	var curResp HandoffSessionsResponse
	_ = json.Unmarshal(recCur.Body.Bytes(), &curResp)
	if len(curResp.Sessions) != 2 {
		t.Fatalf("expected limit=2 current sessions, got %d", len(curResp.Sessions))
	}

	// 3. Preview missing id -> 400
	reqNoID := httptest.NewRequest(http.MethodGet, "/api/hoxi/handoff/preview?source=claude-code", nil)
	recNoID := httptest.NewRecorder()
	mux.ServeHTTP(recNoID, reqNoID)
	if recNoID.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when id missing, got %d", recNoID.Code)
	}

	// 4. Preview non-existent session -> 404
	reqNotFound := httptest.NewRequest(http.MethodGet, "/api/hoxi/handoff/preview?source=grok-cli&id=missing-id", nil)
	recNotFound := httptest.NewRecorder()
	mux.ServeHTTP(recNotFound, reqNotFound)
	if recNotFound.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing session, got %d", recNotFound.Code)
	}

	// 5. Preview valid session for each source
	previewCases := []struct {
		source ExternalToolID
		id     string
	}{
		{ExternalToolClaude, "claude-sess-1"},
		{ExternalToolCodex, "codex-sess-1"},
		{ExternalToolGemini, "gemini-sess-1"},
		{ExternalToolGrok, "grok-sess-1"},
		{ExternalToolCodeBuddy, "cb-sess-1"},
		{ExternalToolTrae, "trae-sess-1"},
		{ExternalToolQoder, "qoder-sess-1"},
		{ExternalToolOpenCode, "opencode-sess-1"},
	}
	for _, tc := range previewCases {
		u := "/api/hoxi/handoff/preview?source=" + string(tc.source) + "&id=" + tc.id
		req := httptest.NewRequest(http.MethodGet, u, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("preview %s/%s failed with %d: %s", tc.source, tc.id, rec.Code, rec.Body.String())
		}
		var detail ExternalSessionDetail
		if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
			t.Fatalf("unmarshal preview %s: %v", tc.source, err)
		}
		if detail.Summary.ID != tc.id || len(detail.Turns) == 0 {
			t.Fatalf("unexpected preview detail for %s: %+v", tc.source, detail)
		}
	}
}

func TestHandoffImportEndpoint(t *testing.T) {
	workspaceDir := t.TempDir()
	setupSyntheticCLIHome(t, workspaceDir)
	port, dbPath, closeSrv := setupMockOpenCodeServerAndDB(t)
	defer closeSrv()

	if gotDB := OpenCodeDataDBPath(); gotDB != dbPath {
		t.Fatalf("expected OpenCodeDataDBPath() = %s, got %s", dbPath, gotDB)
	}

	provSvc := provider.NewService(port, "testpass", workspaceDir, nil)
	svc := NewService(port, "testpass", workspaceDir, provSvc)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// Method not allowed & invalid JSON
	recGet := httptest.NewRecorder()
	mux.ServeHTTP(recGet, httptest.NewRequest(http.MethodGet, "/api/hoxi/handoff/import", nil))
	if recGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET import, got %d", recGet.Code)
	}
	recBadJSON := httptest.NewRecorder()
	mux.ServeHTTP(recBadJSON, httptest.NewRequest(http.MethodPost, "/api/hoxi/handoff/import", strings.NewReader("{bad")))
	if recBadJSON.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", recBadJSON.Code)
	}

	// Import all 8 sources
	importCases := []HandoffImportRequest{
		{Source: ExternalToolClaude, SessionID: "claude-sess-1", TargetDirectory: workspaceDir},
		{Source: ExternalToolCodex, SessionID: "codex-sess-1", TargetDirectory: workspaceDir},
		{Source: ExternalToolGemini, SessionID: "gemini-sess-1", TargetDirectory: workspaceDir, PreferredProviderID: "google", PreferredModelID: "gemini-2.5-pro"},
		{Source: ExternalToolGrok, SessionID: "grok-sess-1", TargetDirectory: workspaceDir},
		{Source: ExternalToolCodeBuddy, SessionID: "cb-sess-1", TargetDirectory: workspaceDir},
		{Source: ExternalToolTrae, SessionID: "trae-sess-1", TargetDirectory: workspaceDir},
		{Source: ExternalToolQoder, SessionID: "qoder-sess-1", TargetDirectory: workspaceDir},
		{Source: ExternalToolOpenCode, SessionID: "opencode-sess-1", TargetDirectory: workspaceDir},
	}

	for _, ic := range importCases {
		body, _ := json.Marshal(ic)
		req := httptest.NewRequest(http.MethodPost, "/api/hoxi/handoff/import", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("import %s HTTP %d: %s", ic.Source, rec.Code, rec.Body.String())
		}
		var resp HandoffImportResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal import response: %v", err)
		}
		if !resp.OK || resp.ImportedTurns == 0 || resp.MemoryPath == "" {
			t.Fatalf("import %s failed: %+v", ic.Source, resp)
		}
		memAbs := filepath.Join(workspaceDir, resp.MemoryPath)
		if _, err := os.Stat(memAbs); err != nil {
			t.Fatalf("expected memory file at %s: %v", memAbs, err)
		}
	}

	// Verify message and part rows were inserted in SQLite DB
	msgCountOut, err := exec.Command("sqlite3", dbPath, "SELECT count(*) FROM message;").Output()
	if err != nil || strings.TrimSpace(string(msgCountOut)) == "0" {
		t.Fatalf("expected messages in sqlite db, got %q (err=%v)", string(msgCountOut), err)
	}
	partCountOut, err := exec.Command("sqlite3", dbPath, "SELECT count(*) FROM part;").Output()
	if err != nil || strings.TrimSpace(string(partCountOut)) == "0" {
		t.Fatalf("expected parts in sqlite db, got %q (err=%v)", string(partCountOut), err)
	}

	// Test insertNormalizedTurnsToOpenCodeDB with empty turns & assistant-first turns
	emptyDetail := &ExternalSessionDetail{
		Summary: ExternalSessionSummary{ID: "empty-1", Title: "Empty Session", SourceLabel: "Grok CLI"},
	}
	if err := insertNormalizedTurnsToOpenCodeDB("ses_empty", workspaceDir, "grok-cli", "grok-4", emptyDetail); err != nil {
		t.Fatalf("insertNormalizedTurnsToOpenCodeDB empty failed: %v", err)
	}
	asstFirstDetail := &ExternalSessionDetail{
		Summary: ExternalSessionSummary{ID: "asst-1", Title: "Assistant First"},
		Turns: []NormalizedTurn{
			{
				Role:      "assistant",
				Reasoning: "Initial reasoning",
				Tools:     []NormalizedToolCall{{CallID: "", Tool: "bash", Output: "ok"}},
			},
		},
	}
	if err := insertNormalizedTurnsToOpenCodeDB("ses_asst_first", workspaceDir, "grok-cli", "grok-4", asstFirstDetail); err != nil {
		t.Fatalf("insertNormalizedTurnsToOpenCodeDB assistant-first failed: %v", err)
	}
}

func TestLocalCLIStatusAndImportEndpoints(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := setupSyntheticCLIHome(t, workspaceDir)
	port, _, closeSrv := setupMockOpenCodeServerAndDB(t)
	defer closeSrv()

	provSvc := provider.NewService(port, "testpass", workspaceDir, nil)
	svc := NewService(port, "testpass", workspaceDir, provSvc)
	svc.SetWSBaseURL("ws://127.0.0.1:6099")
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// 1. GET /api/hoxi/local-cli/status
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/hoxi/local-cli/status", nil)
	recStatus := httptest.NewRecorder()
	mux.ServeHTTP(recStatus, reqStatus)
	if recStatus.Code != http.StatusOK {
		t.Fatalf("status HTTP %d", recStatus.Code)
	}
	var statusResp LocalCLIStatusResponse
	if err := json.Unmarshal(recStatus.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if len(statusResp.Tools) != 8 {
		t.Fatalf("expected 8 tools, got %d", len(statusResp.Tools))
	}
	for _, tool := range statusResp.Tools {
		if !tool.Installed || tool.SessionCount != 1 {
			t.Fatalf("expected tool %s installed with 1 session, got %+v", tool.ID, tool)
		}
		if tool.AuthMode != "session-handoff" && !tool.Authenticated {
			t.Fatalf("expected CLI tool %s authenticated, got %+v", tool.ID, tool)
		}
	}

	// 2. POST /api/hoxi/local-cli/import for all 4 CLI provider tools
	for _, toolID := range []ExternalToolID{ExternalToolGrok, ExternalToolClaude, ExternalToolCodex, ExternalToolGemini} {
		b, _ := json.Marshal(LocalCLIImportRequest{ToolID: toolID})
		req := httptest.NewRequest(http.MethodPost, "/api/hoxi/local-cli/import", bytes.NewReader(b))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("import tool %s HTTP %d", toolID, rec.Code)
		}
		var resp LocalCLIImportResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if !resp.OK {
			t.Fatalf("expected import tool %s OK, got %+v", toolID, resp)
		}
	}

	// Test SetWSBaseURL with wss:// to trigger syncLocalCLIBridgeURLs
	svc.SetWSBaseURL("wss://127.0.0.1:7099")
	if got := svc.httpBridgeBaseURL(); got != "https://127.0.0.1:7099" {
		t.Fatalf("expected https://127.0.0.1:7099, got %s", got)
	}

	// Test Codex OAuth token fallback in auth.json
	_ = os.WriteFile(filepath.Join(homeDir, ".codex", "auth.json"), []byte(`{"tokens":{"access_token":"acc","refresh_token":"ref","account_id":"id1"}}`), 0o644)
	bCodex, _ := json.Marshal(LocalCLIImportRequest{ToolID: ExternalToolCodex})
	recCodex := httptest.NewRecorder()
	mux.ServeHTTP(recCodex, httptest.NewRequest(http.MethodPost, "/api/hoxi/local-cli/import", bytes.NewReader(bCodex)))
	var codexResp LocalCLIImportResponse
	_ = json.Unmarshal(recCodex.Body.Bytes(), &codexResp)
	if !codexResp.OK {
		t.Fatalf("expected codex oauth import OK, got %+v", codexResp)
	}

	// Test unsupported toolID
	bUnsupported, _ := json.Marshal(LocalCLIImportRequest{ToolID: "unknown-cli"})
	recUnsupported := httptest.NewRecorder()
	mux.ServeHTTP(recUnsupported, httptest.NewRequest(http.MethodPost, "/api/hoxi/local-cli/import", bytes.NewReader(bUnsupported)))
	var unsupResp LocalCLIImportResponse
	_ = json.Unmarshal(recUnsupported.Body.Bytes(), &unsupResp)
	if unsupResp.OK || !strings.Contains(unsupResp.Error, "Unsupported toolID") {
		t.Fatalf("expected unsupported error, got %+v", unsupResp)
	}
}

func TestImportOpenCodeCLIProviderSyncsModelsAndAuth(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := setupSyntheticCLIHome(t, workspaceDir)

	configDir := filepath.Join(homeDir, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir opencode config: %v", err)
	}
	configDoc := `{"model":"AINode/kimi-k3","provider":{"AINode":{"name":"AINode","npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://api.ainode.run/v1"},"models":{"kimi-k3":{"name":"kimi-k3"},"deepseek-v4.1-flash":{"name":"deepseek-v4.1-flash"}}}}}`
	configPath := filepath.Join(configDir, "opencode.json")
	if err := os.WriteFile(configPath, []byte(configDoc), 0o644); err != nil {
		t.Fatalf("write opencode config: %v", err)
	}
	dataDir := filepath.Join(homeDir, ".local", "share", "opencode")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir opencode data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "auth.json"), []byte(`{"ainode":{"type":"api","key":"sk-ainode-test"}}`), 0o600); err != nil {
		t.Fatalf("write opencode auth: %v", err)
	}

	provSvc := provider.NewService(0, "", workspaceDir, nil)
	svc := NewService(0, "", workspaceDir, provSvc)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	findOpenCode := func() LocalCLIStatusItem {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hoxi/local-cli/status", nil))
		var status LocalCLIStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
			t.Fatalf("unmarshal status: %v", err)
		}
		for _, tool := range status.Tools {
			if tool.ID == ExternalToolOpenCode {
				return tool
			}
		}
		t.Fatalf("opencode tool missing from status")
		return LocalCLIStatusItem{}
	}

	before := findOpenCode()
	if !before.Authenticated || before.Connected {
		t.Fatalf("expected authenticated but not connected before sync, got %+v", before)
	}
	if before.ProviderID != "AINode" || before.DefaultModel != "kimi-k3" {
		t.Fatalf("unexpected pre-sync target: provider=%s model=%s", before.ProviderID, before.DefaultModel)
	}

	b, _ := json.Marshal(LocalCLIImportRequest{ToolID: ExternalToolOpenCode})
	recImport := httptest.NewRecorder()
	mux.ServeHTTP(recImport, httptest.NewRequest(http.MethodPost, "/api/hoxi/local-cli/import", bytes.NewReader(b)))
	var resp LocalCLIImportResponse
	if err := json.Unmarshal(recImport.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal import: %v", err)
	}
	if !resp.OK || resp.ProviderID != "AINode" || len(resp.Models) != 2 {
		t.Fatalf("unexpected import response: %+v", resp)
	}

	rawModels, err := os.ReadFile(filepath.Join(homeDir, ".hoxi", "models.json"))
	if err != nil {
		t.Fatalf("read hoxi models: %v", err)
	}
	var modelsDoc struct {
		Provider map[string]map[string]interface{} `json:"provider"`
	}
	if err := json.Unmarshal(rawModels, &modelsDoc); err != nil {
		t.Fatalf("unmarshal hoxi models: %v", err)
	}
	if _, ok := modelsDoc.Provider["AINode"]; !ok {
		t.Fatalf("expected AINode provider in hoxi models, got %s", rawModels)
	}
	ainodeOpts, _ := modelsDoc.Provider["AINode"]["options"].(map[string]interface{})
	if ainodeOpts["apiKey"] != "sk-ainode-test" {
		t.Fatalf("expected case-insensitive auth key injection, got %v", ainodeOpts)
	}

	rawAuth, err := os.ReadFile(filepath.Join(homeDir, ".hoxi", "engine-data", "opencode", "auth.json"))
	if err != nil {
		t.Fatalf("read engine auth: %v", err)
	}
	if !strings.Contains(string(rawAuth), "sk-ainode-test") {
		t.Fatalf("expected credentials in engine auth, got %s", rawAuth)
	}

	updatedDoc := `{"model":"AINode/kimi-k3","provider":{"AINode":{"name":"AINode","npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://api.ainode.run/v1"},"models":{"kimi-k3":{"name":"kimi-k3"},"glm-5.3":{"name":"glm-5.3"}}}}}`
	if err := os.WriteFile(configPath, []byte(updatedDoc), 0o644); err != nil {
		t.Fatalf("rewrite opencode config: %v", err)
	}
	recSecond := httptest.NewRecorder()
	mux.ServeHTTP(recSecond, httptest.NewRequest(http.MethodPost, "/api/hoxi/local-cli/import", bytes.NewReader(b)))
	rawSecond, err := os.ReadFile(filepath.Join(homeDir, ".hoxi", "models.json"))
	if err != nil {
		t.Fatalf("read hoxi models after second sync: %v", err)
	}
	if !strings.Contains(string(rawSecond), "glm-5.3") {
		t.Fatalf("expected newly added model after re-sync, got %s", rawSecond)
	}

	if after := findOpenCode(); !after.Connected {
		t.Fatalf("expected opencode connected after sync, got %+v", after)
	}
}

func TestGrokCLIBridgeEndpoints(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := setupSyntheticCLIHome(t, workspaceDir)

	svc := NewService(0, "testpass", workspaceDir, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// 1. GET /api/hoxi/bridge/grok-cli/v1/models
	reqModels := httptest.NewRequest(http.MethodGet, "/api/hoxi/bridge/grok-cli/v1/models", nil)
	recModels := httptest.NewRecorder()
	mux.ServeHTTP(recModels, reqModels)
	if recModels.Code != http.StatusOK {
		t.Fatalf("expected 200 from grok models, got %d", recModels.Code)
	}
	var modelsDoc map[string]interface{}
	_ = json.Unmarshal(recModels.Body.Bytes(), &modelsDoc)
	dataArr, _ := modelsDoc["data"].([]interface{})
	if len(dataArr) != 2 {
		t.Fatalf("expected 2 non-hidden grok models, got %v", dataArr)
	}

	// 2. POST /api/hoxi/bridge/grok-cli/v1/chat/completions with canceled context (exercises payload rewrite & headers without external network wait)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	chatBody := `{"model":"grok-4","stream":false,"messages":[{"role":"user","content":"ping"}]}`
	reqChat := httptest.NewRequest(http.MethodPost, "/api/hoxi/bridge/grok-cli/v1/chat/completions", strings.NewReader(chatBody)).WithContext(ctx)
	recChat := httptest.NewRecorder()
	mux.ServeHTTP(recChat, reqChat)
	if recChat.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 with canceled context, got %d", recChat.Code)
	}

	// 3. POST /api/hoxi/bridge/grok-cli/v1/chat/completions without credentials -> 401
	_ = os.Remove(filepath.Join(homeDir, ".grok", "auth.json"))
	reqNoAuth := httptest.NewRequest(http.MethodPost, "/api/hoxi/bridge/grok-cli/v1/chat/completions", strings.NewReader(chatBody))
	recNoAuth := httptest.NewRecorder()
	mux.ServeHTTP(recNoAuth, reqNoAuth)
	if recNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when auth.json missing, got %d", recNoAuth.Code)
	}
}
