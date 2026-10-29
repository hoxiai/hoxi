package provider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeAuthSource struct {
	site        string
	token       string
	invalidated bool
}

func (f *fakeAuthSource) ServerBaseURL() string { return f.site }
func (f *fakeAuthSource) CurrentDeviceToken() string {
	return f.token
}
func (f *fakeAuthSource) InvalidateDeviceToken(token, baseURL string) {
	f.invalidated = true
}

func newOfficialTestSite(t *testing.T, unauthorized bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unauthorized {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/hoxi/member/models":
			if r.Header.Get("Authorization") != "Bearer hox_dt_test" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":             0,
				"gatewayAvailable": true,
				"gatewayBaseUrl":   "https://api.ainode.run",
				"models": []map[string]any{
					{"modelName": "claude-sonnet-4-6", "displayName": "Claude Sonnet 4.6", "vendor": "Anthropic", "billingMode": "token", "inputPointsPerM": 30000, "outputPointsPerM": 150000},
					{"modelName": "gpt-5.4", "displayName": "GPT-5.4", "vendor": "OpenAI", "billingMode": "token", "inputUsdPerM": 2.5, "outputUsdPerM": 10},
					{"modelName": "disabled-model", "enabled": false},
				},
			})

		case "/api/hoxi/member/keys":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "rawKey": "sk-hoxi-official-key"})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestOfficialModelsPreview(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	server := newOfficialTestSite(t, false)

	svc := NewService(0, "", t.TempDir(), http.NotFoundHandler())
	svc.SetAuthSource(&fakeAuthSource{site: server.URL, token: "hox_dt_test"})

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hoxi/provider/official/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp officialModelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Authenticated || !resp.GatewayAvailable || resp.BaseURL != "https://api.ainode.run/v1" {
		t.Fatalf("unexpected envelope: %+v", resp)
	}
	if len(resp.Models) != 2 {
		t.Fatalf("expected 2 enabled models, got %d: %+v", len(resp.Models), resp.Models)
	}
	if resp.Models[1].InputPointsPerM != 25000 || resp.Models[1].OutputPointsPerM != 100000 {
		t.Fatalf("expected USD fallback converted to points, got %+v", resp.Models[1])
	}
}

func TestOfficialConnectProvisionsAinodeProvider(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	server := newOfficialTestSite(t, false)

	svc := NewService(0, "", t.TempDir(), http.NotFoundHandler())
	svc.SetAuthSource(&fakeAuthSource{site: server.URL, token: "hox_dt_test"})

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/provider/official/connect", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp officialConnectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ProviderID != "hoxi-official" || resp.ModelCount != 2 {
		t.Fatalf("unexpected connect response: %+v", resp)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("sk-hoxi-official-key")) || bytes.Contains(rec.Body.Bytes(), []byte("hox_dt_test")) {
		t.Fatalf("connect response must not leak credentials: %s", rec.Body.String())
	}

	doc := svc.LoadHoxiConfigDoc()
	provMap, _ := doc["provider"].(map[string]interface{})
	ainode, _ := provMap["hoxi-official"].(map[string]interface{})
	opts, _ := ainode["options"].(map[string]interface{})
	if got, _ := opts["apiKey"].(string); got != "sk-hoxi-official-key" {
		t.Fatalf("expected persisted member key, got %q", got)
	}
	if got, _ := opts["baseURL"].(string); got != "https://api.ainode.run/v1" {
		t.Fatalf("expected persisted baseURL, got %q", got)
	}
	models, _ := ainode["models"].(map[string]interface{})
	if len(models) != 2 {
		t.Fatalf("expected exactly 2 enabled models persisted (excluding disabled-model), got %d: %+v", len(models), models)
	}
	sonnet, ok := models["claude-sonnet-4-6"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected persisted claude-sonnet-4-6 model, got %+v", models)
	}
	sonnetCost, _ := sonnet["cost"].(map[string]interface{})
	if sonnetCost["input"] != 3.0 || sonnetCost["output"] != 15.0 {
		t.Fatalf("expected points converted to USD cost (3.0/15.0), got %+v", sonnetCost)
	}
	if _, hasDisabled := models["disabled-model"]; hasDisabled {
		t.Fatalf("disabled-model must not be persisted into models.json")
	}
	if _, ok := doc["mcp"]; ok {
		t.Fatalf("official connect must not persist MCP config")
	}

	// Clearing the official provider removes both models.json entry and engine auth.json key.
	if gotKey := svc.ResolveStoredProviderKey("hoxi-official", "https://api.ainode.run/v1"); gotKey != "sk-hoxi-official-key" {
		t.Fatalf("expected stored key before clear, got %q", gotKey)
	}
	svc.ClearOfficialProvider()
	docAfter := svc.LoadHoxiConfigDoc()
	provMapAfter, _ := docAfter["provider"].(map[string]interface{})
	if _, exists := provMapAfter["hoxi-official"]; exists {
		t.Fatalf("expected hoxi-official removed from models.json after ClearOfficialProvider")
	}
	if gotKey := svc.ResolveStoredProviderKey("hoxi-official", "https://api.ainode.run/v1"); gotKey != "" {
		t.Fatalf("expected engine auth.json key cleared, got %q", gotKey)
	}
}

func TestOfficialRoutesRequireLogin(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	svc := NewService(0, "", t.TempDir(), http.NotFoundHandler())
	svc.SetAuthSource(&fakeAuthSource{site: "https://hoxi.ai", token: ""})

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	modelsRec := httptest.NewRecorder()
	mux.ServeHTTP(modelsRec, httptest.NewRequest(http.MethodGet, "/api/hoxi/provider/official/models", nil))
	if modelsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 anonymous preview, got %d", modelsRec.Code)
	}
	var preview officialModelsResponse
	_ = json.Unmarshal(modelsRec.Body.Bytes(), &preview)
	if preview.Authenticated {
		t.Fatalf("expected authenticated=false when logged out")
	}

	connectRec := httptest.NewRecorder()
	mux.ServeHTTP(connectRec, httptest.NewRequest(http.MethodPost, "/api/hoxi/provider/official/connect", nil))
	if connectRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when logged out, got %d", connectRec.Code)
	}
}

func TestOfficialConnectInvalidatesRevokedToken(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	server := newOfficialTestSite(t, true)

	auth := &fakeAuthSource{site: server.URL, token: "hox_dt_test"}
	svc := NewService(0, "", t.TempDir(), http.NotFoundHandler())
	svc.SetAuthSource(auth)

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/provider/official/connect", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on revoked token, got %d", rec.Code)
	}
	if !auth.invalidated {
		t.Fatalf("expected device token to be invalidated on 401")
	}
	doc := svc.LoadHoxiConfigDoc()
	provMap, _ := doc["provider"].(map[string]interface{})
	if _, exists := provMap["hoxi-official"]; exists {
		t.Fatalf("expected hoxi-official removed on 401")
	}
}
