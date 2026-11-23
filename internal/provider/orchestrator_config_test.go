package provider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrchestratorConfigPersistenceAndValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	svc := NewService(0, "", t.TempDir(), nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// 1. Corrupt file is backed up safely instead of failing or clobbering silently.
	cfgPath := filepath.Join(home, ".hoxi", "orchestrator.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	got := svc.LoadOrchestratorConfig()
	if got != (OrchestratorConfig{}) {
		t.Fatalf("expected empty config after corrupt reset, got %+v", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(cfgPath))
	backedUp := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "orchestrator.json.corrupt-") {
			backedUp = true
			break
		}
	}
	if !backedUp {
		t.Fatalf("expected corrupt orchestrator.json to be backed up")
	}

	// 2. Valid PUT saves atomically and normalizes effort.
	putReq := func(method string, payload map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(payload)
		req := httptest.NewRequest(method, "/api/hoxi/provider/orchestrator", bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := putReq(http.MethodPut, map[string]any{
		"orchestratorModel":  "anthropic/claude-opus-4-6",
		"orchestratorEffort": "xhigh",
		"executorModel":      "google/gemini-2.5-flash",
		"executorEffort":     "MEDIUM",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT orchestrator config failed: %d %s", rec.Code, rec.Body.String())
	}

	// 3. GET returns persisted config.
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/hoxi/provider/orchestrator", nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET orchestrator config failed: %d %s", getRec.Code, getRec.Body.String())
	}
	var resp struct {
		OK     bool               `json:"ok"`
		Config OrchestratorConfig `json:"config"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if resp.Config.OrchestratorModel != "anthropic/claude-opus-4-6" ||
		resp.Config.OrchestratorEffort != "xhigh" ||
		resp.Config.ExecutorModel != "google/gemini-2.5-flash" ||
		resp.Config.ExecutorEffort != "medium" {
		t.Fatalf("unexpected persisted config: %+v", resp.Config)
	}

	// 4. Reject virtual providers, malformed specs, unsupported efforts, and unknown fields.
	badCases := []map[string]any{
		{"orchestratorModel": "hoxi-auto/frontier"},
		{"executorModel": "hoxi-orchestrator/default"},
		{"executorModel": "missing-slash"},
		{"executorEffort": "ultra-mega"},
		{"unknownField": "x"},
	}
	for _, tc := range badCases {
		bad := putReq(http.MethodPatch, tc)
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %v, got %d %s", tc, bad.Code, bad.Body.String())
		}
	}

	// 5. Clearing via null works while preserving untouched fields.
	clr := putReq(http.MethodPatch, map[string]any{"executorEffort": nil})
	if clr.Code != http.StatusOK {
		t.Fatalf("PATCH clear failed: %d %s", clr.Code, clr.Body.String())
	}
	after := svc.LoadOrchestratorConfig()
	if after.ExecutorEffort != "" || after.ExecutorModel != "google/gemini-2.5-flash" {
		t.Fatalf("unexpected config after clear: %+v", after)
	}
}
