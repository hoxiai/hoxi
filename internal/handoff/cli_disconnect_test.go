package handoff

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoxiai/hoxi/internal/provider"
)

func TestLocalCLIDisconnectAndCleanOverwriteReimport(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := setupSyntheticCLIHome(t, workspaceDir)

	configDir := filepath.Join(homeDir, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "opencode.json")
	initialCfg := `{"model":"mygw/m-stale","provider":{"mygw":{"name":"MyGW","npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://old.gw.test/v1","apiKey":"sk-old-gw"},"models":{"m-stale":{"name":"Stale"},"m-keep":{"name":"Keep"}}}}}`
	if err := os.WriteFile(configPath, []byte(initialCfg), 0o600); err != nil {
		t.Fatal(err)
	}

	provSvc := provider.NewService(0, "", workspaceDir, nil)
	svc := NewService(0, "", workspaceDir, provSvc)
	provSvc.SetOnProviderDisconnected(svc.NotifyProviderDisconnected)
	mux := http.NewServeMux()
	provSvc.RegisterRoutes(mux)
	svc.RegisterRoutes(mux)

	getStatus := func(id ExternalToolID) LocalCLIStatusItem {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hoxi/local-cli/status", nil))
		var out LocalCLIStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal status: %v", err)
		}
		for _, item := range out.Tools {
			if item.ID == id {
				return item
			}
		}
		t.Fatalf("tool %s not found in status", id)
		return LocalCLIStatusItem{}
	}

	postJSON := func(path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw)))
		return rec
	}

	// 1. Import Grok CLI & OpenCode CLI -> both become Connected.
	if rec := postJSON("/api/hoxi/local-cli/import", LocalCLIImportRequest{ToolID: ExternalToolGrok}); rec.Code != http.StatusOK {
		t.Fatalf("import grok failed: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON("/api/hoxi/local-cli/import", LocalCLIImportRequest{ToolID: ExternalToolOpenCode}); rec.Code != http.StatusOK {
		t.Fatalf("import opencode failed: %d %s", rec.Code, rec.Body.String())
	}
	if !getStatus(ExternalToolGrok).Connected || !getStatus(ExternalToolOpenCode).Connected {
		t.Fatalf("expected grok and opencode connected after import")
	}

	// 2. Disconnect Grok via provider disconnect -> LocalCLI status immediately flips to Connected=false, Authenticated=true.
	if rec := postJSON("/api/hoxi/provider/disconnect", map[string]any{"providerID": "grok-cli"}); rec.Code != http.StatusOK {
		t.Fatalf("disconnect grok-cli failed: %d %s", rec.Code, rec.Body.String())
	}
	grokAfterDel := getStatus(ExternalToolGrok)
	if grokAfterDel.Connected || !grokAfterDel.Authenticated {
		t.Fatalf("expected grok disconnected & authenticated for re-import, got %+v", grokAfterDel)
	}

	// 3. Re-import Grok -> Connected=true again!
	if rec := postJSON("/api/hoxi/local-cli/import", LocalCLIImportRequest{ToolID: ExternalToolGrok}); rec.Code != http.StatusOK {
		t.Fatalf("re-import grok failed: %d %s", rec.Code, rec.Body.String())
	}
	if !getStatus(ExternalToolGrok).Connected {
		t.Fatalf("expected grok re-connected after re-import")
	}

	// 4. Disconnect OpenCode CLI via /api/hoxi/local-cli/disconnect -> Connected=false, external opencode.json untouched.
	if rec := postJSON("/api/hoxi/local-cli/disconnect", LocalCLIDisconnectRequest{ToolID: ExternalToolOpenCode}); rec.Code != http.StatusOK {
		t.Fatalf("disconnect opencode-cli failed: %d %s", rec.Code, rec.Body.String())
	}
	ocAfterDel := getStatus(ExternalToolOpenCode)
	if ocAfterDel.Connected || !ocAfterDel.Authenticated {
		t.Fatalf("expected opencode disconnected & authenticated after local-cli/disconnect, got %+v", ocAfterDel)
	}
	if diskRaw, _ := os.ReadFile(configPath); string(diskRaw) != initialCfg {
		t.Fatalf("external opencode.json was mutated on disconnect: %s", string(diskRaw))
	}

	// 5. Update external opencode.json (remove m-stale, change baseURL) and re-import with Overwrite=true -> stale model purged, new URL applied!
	updatedCfg := `{"model":"mygw/m-fresh","provider":{"mygw":{"name":"MyGW v2","npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://new.gw.test/v1","apiKey":"sk-new-gw"},"models":{"m-fresh":{"name":"Fresh"}}}}}`
	if err := os.WriteFile(configPath, []byte(updatedCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	recReimp := postJSON("/api/hoxi/local-cli/import", LocalCLIImportRequest{ToolID: ExternalToolOpenCode, Overwrite: true})
	var reimpResp LocalCLIImportResponse
	if err := json.Unmarshal(recReimp.Body.Bytes(), &reimpResp); err != nil || !reimpResp.OK {
		t.Fatalf("overwrite re-import failed: %d %s", recReimp.Code, recReimp.Body.String())
	}
	if !getStatus(ExternalToolOpenCode).Connected {
		t.Fatalf("expected opencode connected after clean re-import")
	}
	hoxiMap := provSvc.LoadHoxiProvidersMap()
	mygw, _ := hoxiMap["mygw"].(map[string]any)
	opts, _ := mygw["options"].(map[string]any)
	models, _ := mygw["models"].(map[string]any)
	if opts["baseURL"] != "https://new.gw.test/v1" {
		t.Fatalf("expected overwritten baseURL https://new.gw.test/v1, got %v", opts["baseURL"])
	}
	if _, hasStale := models["m-stale"]; hasStale {
		t.Fatalf("expected stale model m-stale purged on overwrite re-import, got %v", models)
	}
	if _, hasFresh := models["m-fresh"]; !hasFresh {
		t.Fatalf("expected fresh model m-fresh present, got %v", models)
	}
}
