package provider

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var catalogSecrets = []string{"sk-engine-key", "sk-engine-opt", "sk-hoxi-key", "sk-imported-key", "secret.example", "X-Secret-Header"}

// assertCatalogSanitized fails unless the body only carries the catalog's own
// fields and none of the seeded credentials.
func assertCatalogSanitized(t *testing.T, raw []byte) modelCatalog {
	t.Helper()
	for _, secret := range catalogSecrets {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("catalog leaked %q: %s", secret, raw)
		}
	}
	allowed := map[string]bool{"source": true, "providers": true, "id": true, "name": true, "models": true}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				if !allowed[k] {
					t.Fatalf("catalog exposes unexpected field %q: %s", k, raw)
				}
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("invalid catalog JSON: %v", err)
	}
	walk(generic)
	var out modelCatalog
	_ = json.Unmarshal(raw, &out)
	return out
}

func seedCatalogConfig(t *testing.T, homeDir, workspaceDir string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Join(workspaceDir, ".hoxi"), 0o755)
	_ = os.WriteFile(filepath.Join(workspaceDir, ".hoxi", "models.json"), []byte(`{
		"provider": {
			"custom-oai": {
				"name": "Custom OAI",
				"options": {"apiKey": "sk-hoxi-key", "baseURL": "https://secret.example/v1", "headers": {"X-Secret-Header": "1"}},
				"models": {"gpt-4o": {"name": "GPT-4o"}}
			}
		}
	}`), 0o644)
	ocDir := filepath.Join(homeDir, ".config", "opencode")
	_ = os.MkdirAll(ocDir, 0o755)
	_ = os.WriteFile(filepath.Join(ocDir, "opencode.json"), []byte(`{
		"provider": {
			"imported": {"options": {"apiKey": "sk-imported-key"}, "models": {"m1": {}}},
			"no-models": {"options": {"apiKey": "sk-imported-key"}}
		}
	}`), 0o644)
}

func TestModelCatalogFromEngineIsSanitized(t *testing.T) {
	homeDir, workspaceDir := setupIsolatedEnv(t)
	seedCatalogConfig(t, homeDir, workspaceDir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/provider" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{
			"all": [
				{"id": "custom-oai", "name": "Custom OAI", "key": "sk-engine-key",
				 "options": {"apiKey": "sk-engine-opt", "baseURL": "https://secret.example/v1"},
				 "models": {"gpt-4o": {"name": "GPT-4o", "options": {"apiKey": "sk-engine-opt"}}, "gpt-3.5": {"name": "GPT-3.5"}}},
				{"id": "anthropic", "name": "Anthropic", "env": ["ANTHROPIC_API_KEY"], "key": "sk-engine-key",
				 "models": {"claude-sonnet": {"id": "claude-sonnet", "name": "Claude Sonnet", "cost": {"input": 3}}}},
				{"id": "unconnected", "name": "Unconnected", "models": {"x": {"name": "X"}}}
			],
			"default": {"anthropic": "claude-sonnet"},
			"connected": ["custom-oai", "anthropic"]
		}`))
	}))
	t.Cleanup(srv.Close)
	svc := NewService(srv.Listener.Addr().(*net.TCPAddr).Port, "secret", workspaceDir, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	rec := doMuxReq(t, mux, http.MethodGet, "/api/hoxi/provider/models", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET provider/models = %d %s", rec.Code, rec.Body.String())
	}
	got := assertCatalogSanitized(t, rec.Body.Bytes())
	if got.Source != "engine" || len(got.Providers) != 2 {
		t.Fatalf("unexpected engine catalog: %+v", got)
	}
	if p := got.Providers[0]; p.ID != "anthropic" || p.Name != "Anthropic" || len(p.Models) != 1 || p.Models[0] != (modelCatalogModel{ID: "claude-sonnet", Name: "Claude Sonnet"}) {
		t.Fatalf("unexpected anthropic entry: %+v", p)
	}
	// .hoxi/models.json narrows a managed provider to its selected models.
	if p := got.Providers[1]; p.ID != "custom-oai" || len(p.Models) != 1 || p.Models[0].ID != "gpt-4o" {
		t.Fatalf("unexpected custom-oai entry: %+v", p)
	}

	if rec := doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/models", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST provider/models = %d, want 405", rec.Code)
	}
}

func TestModelCatalogFallsBackToSanitizedConfig(t *testing.T) {
	homeDir, workspaceDir := setupIsolatedEnv(t)
	seedCatalogConfig(t, homeDir, workspaceDir)
	svc := NewService(0, "", workspaceDir, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	rec := doMuxReq(t, mux, http.MethodGet, "/api/hoxi/provider/models", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET provider/models = %d %s", rec.Code, rec.Body.String())
	}
	got := assertCatalogSanitized(t, rec.Body.Bytes())
	if got.Source != "config" || len(got.Providers) != 2 {
		t.Fatalf("unexpected config catalog: %+v", got)
	}
	if p := got.Providers[0]; p.ID != "custom-oai" || p.Name != "Custom OAI" || p.Models[0] != (modelCatalogModel{ID: "gpt-4o", Name: "GPT-4o"}) {
		t.Fatalf("unexpected custom-oai entry: %+v", p)
	}
	if p := got.Providers[1]; p.ID != "imported" || p.Name != "imported" || p.Models[0] != (modelCatalogModel{ID: "m1", Name: "m1"}) {
		t.Fatalf("unexpected imported entry: %+v", p)
	}
}
