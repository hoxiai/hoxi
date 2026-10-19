package handoff

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/provider"
)

func TestDiscoveryPreservesStatsOrderAndFreshness(t *testing.T) {
	dir := t.TempDir()
	setupSyntheticCLIHome(t, dir)
	g := NewService(0, "test", dir, nil)
	request := func(path string, result any) {
		t.Helper()
		w := httptest.NewRecorder()
		gMux := http.NewServeMux()
		g.RegisterRoutes(gMux)
		gMux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("request failed: %d %s", w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), result); err != nil {
			t.Fatal(err)
		}
	}
	var all, filtered HandoffSessionsResponse
	request("/api/hoxi/handoff/sessions", &all)
	request("/api/hoxi/handoff/sessions?source=claude-code", &filtered)
	if len(all.Sources) != 7 || !reflect.DeepEqual(all.Sources, filtered.Sources) {
		t.Fatal("source filter changed full source stats")
	}
	if len(filtered.Sessions) != 1 || filtered.Sessions[0].Source != ExternalToolClaude {
		t.Fatalf("unexpected filtered sessions: %+v", filtered.Sessions)
	}
	var status LocalCLIStatusResponse
	request("/api/hoxi/local-cli/status", &status)
	if len(status.Tools) != 7 {
		t.Fatalf("unexpected tools: %+v", status.Tools)
	}
	for i, id := range []ExternalToolID{
		ExternalToolGrok,
		ExternalToolClaude,
		ExternalToolCodex,
		ExternalToolGemini,
		ExternalToolCodeBuddy,
		ExternalToolTrae,
		ExternalToolQoder,
	} {
		if status.Tools[i].ID != id || status.Tools[i].SessionCount != all.Sources[id].SessionCount {
			t.Fatalf("tool order or stats changed: %+v", status.Tools[i])
		}
	}
	// External changes must be visible on the very next request.
	projects := filepath.Join(claudeHomeDir(), "projects")
	entries, err := os.ReadDir(projects)
	if err != nil || len(entries) == 0 {
		t.Fatalf("missing synthetic projects: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projects, entries[0].Name(), "new-session.jsonl"), []byte("{\"type\":\"user\",\"message\":{\"content\":\"new external session\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var fresh HandoffSessionsResponse
	request("/api/hoxi/handoff/sessions?source=codex-cli", &fresh)
	if fresh.Sources[ExternalToolClaude].SessionCount != all.Sources[ExternalToolClaude].SessionCount+1 {
		t.Fatal("filtered request did not freshly scan every source")
	}
	request("/api/hoxi/local-cli/status", &status)
	if status.Tools[1].SessionCount != fresh.Sources[ExternalToolClaude].SessionCount {
		t.Fatal("CLI status did not freshly scan external sessions")
	}
}

func TestLocalCLIOAuthOnlyImportAndConnectedState(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := setupSyntheticCLIHome(t, workspaceDir)
	port, _, closeSrv := setupMockOpenCodeServerAndDB(t)
	defer closeSrv()

	// Replace settings/auth with OAuth-only formats (no API keys in settings.json)
	_ = os.WriteFile(filepath.Join(homeDir, ".claude", "settings.json"), []byte(`{"model":"opus[1m]","env":{"HTTP_PROXY":"http://127.0.0.1:7890"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(homeDir, ".claude", ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-test"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(homeDir, ".claude.json"), []byte(`{"additionalModelOptionsCache":[{"value":"claude-fable-5-1[1m]"}]}`), 0o644)

	_ = os.WriteFile(filepath.Join(homeDir, ".gemini", "settings.json"), []byte(`{"hooks":{}}`), 0o644)
	_ = os.WriteFile(filepath.Join(homeDir, ".gemini", "jetski-standalone-oauth-token"), []byte(`{"token":{"access_token":"ya29.test-antigravity"}}`), 0o644)

	_ = os.WriteFile(filepath.Join(homeDir, ".codex", "auth.json"), []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"acc-tok","refresh_token":"ref-tok","account_id":"acct-1"}}`), 0o644)

	provSvc := provider.NewService(port, "testpass", workspaceDir, nil)
	svc := NewService(port, "testpass", workspaceDir, provSvc)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	for _, toolID := range []ExternalToolID{ExternalToolClaude, ExternalToolGemini} {
		b, _ := json.Marshal(LocalCLIImportRequest{ToolID: toolID})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/local-cli/import", bytes.NewReader(b)))
		var resp LocalCLIImportResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal %s import: %v", toolID, err)
		}
		if resp.OK || !strings.Contains(resp.Error, "运行时新建会话") {
			t.Fatalf("expected OAuth-only import for %s to require Runner runtime, got %+v", toolID, resp)
		}
	}

	bCodex, _ := json.Marshal(LocalCLIImportRequest{ToolID: ExternalToolCodex})
	recCodex := httptest.NewRecorder()
	mux.ServeHTTP(recCodex, httptest.NewRequest(http.MethodPost, "/api/hoxi/local-cli/import", bytes.NewReader(bCodex)))
	var codexResp LocalCLIImportResponse
	if err := json.Unmarshal(recCodex.Body.Bytes(), &codexResp); err != nil {
		t.Fatalf("unmarshal codex import: %v", err)
	}
	if !codexResp.OK || len(codexResp.Models) == 0 {
		t.Fatalf("expected OAuth-only import for codex to succeed, got %+v", codexResp)
	}
}
