package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

func newMockOpenCodeServer(t *testing.T) (*httptest.Server, int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/dispose":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/global/config":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"provider": {
					"custom-oai": {
						"models": {
							"gpt-4o": {"name": "GPT-4o"},
							"gpt-3.5": {"name": "GPT-3.5"}
						}
					}
				}
			}`))
		case "/provider":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"all": [
					{
						"id": "custom-oai",
						"key": "sk-from-opencode",
						"models": {
							"gpt-4o": {"name": "GPT-4o"},
							"gpt-3.5": {"name": "GPT-3.5"}
						}
					},
					{
						"id": "opt-key-prov",
						"options": {"apiKey": "sk-from-options"}
					}
				]
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	return srv, port
}

func newMockLLMServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models", "/models":
			auth := r.Header.Get("Authorization")
			if auth == "Bearer bad-key" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"Invalid key"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o","name":"GPT-4o"},{"id":"o3-mini","name":"o3-mini"}]}`))

		case "/v1/chat/completions", "/chat/completions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			model, _ := body["model"].(string)
			if model == "fail-model" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"message":"Model overloaded"}}`))
				return
			}
			// Check if this is an audit request or session title request or probe
			msgs, _ := body["messages"].([]any)
			var prompt string
			if len(msgs) > 0 {
				if m0, ok := msgs[0].(map[string]any); ok {
					prompt, _ = m0["content"].(string)
				}
			}
			if strings.Contains(prompt, "Senior Code Auditor") {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"score\": 94, \"pass\": true, \"summary\": \"Well structured changes\", \"issues\": []}"}}]}`))
				return
			}
			if strings.Contains(prompt, "Summarize") || strings.Contains(prompt, "总结") {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"重构 Provider 单元测试"}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))

		case "/v1/messages":
			if r.Header.Get("x-api-key") == "" || r.Header.Get("anthropic-version") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"Missing anthropic headers"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"content":[{"text":"{\"score\": 91, \"pass\": true, \"summary\": \"Anthropic audit pass\", \"issues\": []}"}]}`))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func doMuxReq(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if s, ok := body.(string); ok {
			buf.WriteString(s)
		} else {
			_ = json.NewEncoder(&buf).Encode(body)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAuditResultStatsSerializeZeroScores(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	_, ocPort := newMockOpenCodeServer(t)
	svc := NewService(ocPort, "secret", workspaceDir, nil)
	svc.stats = &modelStatsStore{records: make(map[string]*providerTelemetryStats)}
	svc.RecordAuditResult("worker", "model", 95, true, "Previous audit")
	svc.RecordAuditResult("worker", "model", 0, false, "Incorrect implementation")
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	stats := svc.stats.records["worker"]
	model := stats.Models["model"]
	if stats.AuditTotalCount != 2 || model.AuditTotalCount != 2 || len(stats.AuditScores) != 2 || stats.AuditScores[1] != 0 ||
		len(model.AuditScores) != 2 || model.AuditScores[1] != 0 {
		t.Fatalf("zero score was not recorded correctly: %+v, %+v", stats, model)
	}
	rec := doMuxReq(t, mux, http.MethodGet, "/api/hoxi/provider/telemetry", nil)
	if strings.Count(rec.Body.String(), `"lastAuditScore":0`) != 2 {
		t.Fatalf("provider and model must serialize lastAuditScore zero: %s", rec.Body.String())
	}
}

func TestAssistRoutesForwardToInjectedHandlers(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	svc := NewService(0, "secret", workspaceDir, nil)
	svc.SetAssistHandler(AssistSummarizeTitle, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": r.URL.Path})
	})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	rec := doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/summarize-title", map[string]any{"prompt": "x"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"path":"/api/hoxi/provider/summarize-title"`) {
		t.Fatalf("assist route must forward to the injected handler: %d %s", rec.Code, rec.Body.String())
	}
	for _, action := range []string{AssistAudit, AssistGenerateCommitMessage, AssistEnhancePrompt} {
		rec := doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/"+action, map[string]any{})
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("unwired assist route %s must report 503, got %d", action, rec.Code)
		}
	}
}

func TestLLMClientResolveAndComplete(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	_, ocPort := newMockOpenCodeServer(t)
	llmSrv := newMockLLMServer(t)
	svc := NewService(ocPort, "secret", workspaceDir, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	ctx := context.Background()

	target := svc.Resolve("custom-oai", "gpt-4o", llmSrv.URL+"/v1", "")
	if !target.Ready() || target.ProviderID != "custom-oai" || target.ModelID != "gpt-4o" {
		t.Fatalf("expected a ready OpenAI-compatible target, got %+v", target)
	}
	text, err := svc.Complete(ctx, target, llm.Request{Prompt: "Summarize the request", MaxTokens: 64, Timeout: 5 * time.Second})
	if err != nil || text != "重构 Provider 单元测试" {
		t.Fatalf("OpenAI completion = %q, %v", text, err)
	}

	// Seed anthropic provider config so credentials and headers come from models.json.
	_ = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/models-config", map[string]any{
		"providerID": "anthropic",
		"config": map[string]any{
			"npm": "@ai-sdk/anthropic",
			"options": map[string]any{
				"baseURL": llmSrv.URL,
				"apiKey":  "sk-ant-test",
				"headers": map[string]any{"X-Audit-Header": "1"},
			},
		},
	})
	target = svc.Resolve("anthropic", "claude-3-7-sonnet", "", "")
	text, err = svc.Complete(ctx, target, llm.Request{Prompt: "Senior Code Auditor", Timeout: 5 * time.Second, Purpose: llm.PurposeReview})
	if err != nil || !strings.Contains(text, "Anthropic audit pass") {
		t.Fatalf("Anthropic completion = %q, %v", text, err)
	}

	invalid := svc.Resolve("observer", "model", "invalid://observer", "")
	if invalid.HasEndpoint || invalid.Ready() {
		t.Fatalf("non-HTTP endpoint must not be ready: %+v", invalid)
	}
	if _, err := svc.Complete(ctx, invalid, llm.Request{Prompt: "x"}); !errors.Is(err, llm.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable for a non-HTTP endpoint, got %v", err)
	}
	if _, err := svc.Complete(ctx, llm.Target{}, llm.Request{Prompt: "x"}); !errors.Is(err, llm.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable without a model, got %v", err)
	}
}

func TestModelsConfigAndProxyRoutes(t *testing.T) {
	homeDir, workspaceDir := setupIsolatedEnv(t)
	_, ocPort := newMockOpenCodeServer(t)

	// Seed an OpenCode imported provider in ~/.config/opencode/opencode.json
	ocDir := filepath.Join(homeDir, ".config", "opencode")
	_ = os.MkdirAll(ocDir, 0o755)
	_ = os.WriteFile(filepath.Join(ocDir, "opencode.json"), []byte(`{
		"provider": {
			"readonly-imp": {"options": {"apiKey": "sk-imp-secret"}}
		}
	}`), 0o644)

	// Seed ~/.local/share/opencode/auth.json for fallback key lookup
	authDir := filepath.Join(homeDir, ".local", "share", "opencode")
	_ = os.MkdirAll(authDir, 0o755)
	_ = os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(`{"auth-file-prov":{"key":"sk-from-auth-json"}}`), 0o644)

	fallbackHit := false
	fallbackProxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHit = true
		w.WriteHeader(http.StatusAccepted)
	})

	svc := NewService(ocPort, "secret", workspaceDir, fallbackProxy)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// Verify ResolveStoredProviderKey from OpenCode /provider and auth.json
	if got := svc.ResolveStoredProviderKey("custom-oai", ""); got != "sk-from-opencode" {
		t.Fatalf("ResolveStoredProviderKey(custom-oai) = %q, want sk-from-opencode", got)
	}
	if got := svc.ResolveStoredProviderKey("opt-key-prov", ""); got != "sk-from-options" {
		t.Fatalf("ResolveStoredProviderKey(opt-key-prov) = %q, want sk-from-options", got)
	}
	if got := svc.ResolveStoredProviderKey("auth-file-prov", ""); got != "sk-from-auth-json" {
		t.Fatalf("ResolveStoredProviderKey(auth-file-prov) = %q, want sk-from-auth-json", got)
	}

	// 1. GET /api/hoxi/models-config
	rec := doMuxReq(t, mux, http.MethodGet, "/api/hoxi/models-config", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "readonly-imp") {
		t.Fatalf("GET models-config unexpected: %d %s", rec.Code, rec.Body.String())
	}

	// 2. DELETE read-only imported provider should return 403
	rec = doMuxReq(t, mux, http.MethodDelete, "/api/hoxi/models-config?providerID=readonly-imp", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when deleting imported provider, got %d", rec.Code)
	}

	// 3. POST /api/hoxi/models-config (taking over readonly-imp should inherit apiKey; also add custom-oai with filtered models)
	rec = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/models-config", map[string]any{
		"providerID": "readonly-imp",
		"config": map[string]any{
			"options": map[string]any{"baseURL": "https://imp.example/v1"},
		},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "sk-imp-secret") {
		t.Fatalf("expected inherited apiKey in saved provider: %d %s", rec.Code, rec.Body.String())
	}

	rec = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/models-config", map[string]any{
		"providerID": "custom-oai",
		"config": map[string]any{
			"models": map[string]any{
				"gpt-4o": map[string]any{"name": "GPT-4o"},
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("save custom-oai failed: %d %s", rec.Code, rec.Body.String())
	}

	// 4. GET / PATCH /global/config proxy filtering
	rec = doMuxReq(t, mux, http.MethodPatch, "/global/config", map[string]any{
		"model": "custom-oai/gpt-4o",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /global/config failed: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "gpt-3.5") || !strings.Contains(rec.Body.String(), "gpt-4o") {
		t.Fatalf("expected /global/config to filter out gpt-3.5: %s", rec.Body.String())
	}

	// Routing roles must come from .hoxi/models.json even though the OpenCode
	// snapshot was loaded at boot and never received this PATCH.
	rec = doMuxReq(t, mux, http.MethodPatch, "/global/config", map[string]any{
		"small_model": "custom-oai/gpt-4o",
		"agent":       map[string]any{"build": map[string]any{"model": "custom-oai/gpt-4o"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH routing roles failed: %d %s", rec.Code, rec.Body.String())
	}
	var globalCfg map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &globalCfg); err != nil {
		t.Fatalf("invalid /global/config response: %v", err)
	}
	if globalCfg["small_model"] != "custom-oai/gpt-4o" {
		t.Fatalf("routing roles missing from /global/config: %s", rec.Body.String())
	}
	if agent, _ := globalCfg["agent"].(map[string]any); agent == nil || agent["build"] == nil {
		t.Fatalf("agent model override missing from /global/config: %s", rec.Body.String())
	}

	// Delegation policy lives on the agent's own permission.task subtree; the
	// subagent settings tab writes it and expects it back from /global/config.
	rec = doMuxReq(t, mux, http.MethodPatch, "/global/config", map[string]any{
		"agent": map[string]any{
			"build": map[string]any{"permission": map[string]any{"task": map[string]any{"*": "deny", "reviewer": "allow"}}},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH agent permission.task failed: %d %s", rec.Code, rec.Body.String())
	}
	globalCfg = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &globalCfg); err != nil {
		t.Fatalf("invalid /global/config response: %v", err)
	}
	taskValue, _ := globalCfg["agent"].(map[string]any)["build"].(map[string]any)["permission"].(map[string]any)["task"].(map[string]any)
	if taskValue["*"] != "deny" || taskValue["reviewer"] != "allow" {
		t.Fatalf("agent permission.task missing from /global/config: %s", rec.Body.String())
	}

	rec = doMuxReq(t, mux, http.MethodPatch, "/global/config", map[string]any{"small_model": nil})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear small_model failed: %d %s", rec.Code, rec.Body.String())
	}
	globalCfg = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &globalCfg); err != nil {
		t.Fatalf("invalid /global/config response: %v", err)
	}
	if _, exists := globalCfg["small_model"]; exists {
		t.Fatalf("cleared small_model still present: %s", rec.Body.String())
	}

	// 5. GET /provider proxy filtering & non-GET fallback proxy
	rec = doMuxReq(t, mux, http.MethodGet, "/provider", nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "gpt-3.5") {
		t.Fatalf("expected /provider to filter out gpt-3.5: %d %s", rec.Code, rec.Body.String())
	}
	rec = doMuxReq(t, mux, http.MethodPost, "/provider", nil)
	if rec.Code != http.StatusAccepted || !fallbackHit {
		t.Fatalf("expected non-GET /provider to delegate to proxy handler, got %d", rec.Code)
	}

	// 6. DELETE /api/hoxi/models-config
	rec = doMuxReq(t, mux, http.MethodDelete, "/api/hoxi/models-config?providerID=custom-oai", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE custom-oai failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFetchProbeAndTelemetryRoutes(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	_, ocPort := newMockOpenCodeServer(t)
	llmSrv := newMockLLMServer(t)

	t.Setenv("TEST_HOXI_API_KEY", "sk-env-resolved")

	svc := NewService(ocPort, "secret", workspaceDir, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// 1. POST /api/hoxi/provider/fetch-models
	if rec := doMuxReq(t, mux, http.MethodGet, "/api/hoxi/provider/fetch-models", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET fetch-models, got %d", rec.Code)
	}
	rec := doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/fetch-models", map[string]any{
		"baseURL": llmSrv.URL + "/v1",
		"apiKey":  "{env:TEST_HOXI_API_KEY}",
		"headers": map[string]string{"X-Custom": "1"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "gpt-4o") {
		t.Fatalf("fetch-models failed: %d %s", rec.Code, rec.Body.String())
	}

	// Auth error on fetch-models
	rec = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/fetch-models", map[string]any{
		"baseURL": llmSrv.URL + "/v1",
		"apiKey":  "bad-key",
	})
	if !strings.Contains(rec.Body.String(), `"auth":true`) {
		t.Fatalf("expected auth:true on 401 fetch-models, got %s", rec.Body.String())
	}

	// 2. POST /api/hoxi/provider/test-model (OpenAI + Anthropic + failure)
	rec = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/test-model", map[string]any{
		"providerID": "custom-oai",
		"baseURL":    llmSrv.URL + "/v1",
		"apiKey":     "********",
		"modelID":    "gpt-4o",
	})
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("test-model OpenAI failed: %s", rec.Body.String())
	}

	rec = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/test-model", map[string]any{
		"providerID": "anthropic",
		"baseURL":    llmSrv.URL,
		"apiKey":     "sk-ant-test",
		"npm":        "@ai-sdk/anthropic",
		"modelID":    "claude-3-7-sonnet",
	})
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("test-model Anthropic failed: %s", rec.Body.String())
	}

	rec = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/test-model", map[string]any{
		"providerID": "custom-oai",
		"baseURL":    llmSrv.URL + "/v1",
		"modelID":    "fail-model",
	})
	if !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), "Model overloaded") {
		t.Fatalf("expected test-model failure response, got %s", rec.Body.String())
	}

	// 3. Audit verdicts recorded through the LLM client surface in telemetry
	svc.RecordAuditResult("custom-oai", "gpt-4o", 94, true, "Well structured changes")

	// 5. GET / POST / DELETE /api/hoxi/provider/telemetry
	rec = doMuxReq(t, mux, http.MethodGet, "/api/hoxi/provider/telemetry", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "custom-oai") {
		t.Fatalf("GET telemetry failed: %d %s", rec.Code, rec.Body.String())
	}

	rec = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/telemetry", map[string]any{
		"providers": map[string]any{
			" synced-prov ": map[string]any{
				"totalTurns":   5,
				"successTurns": 5,
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST telemetry failed: %d %s", rec.Code, rec.Body.String())
	}

	rec = doMuxReq(t, mux, http.MethodDelete, "/api/hoxi/provider/telemetry?providerID=synced-prov", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE single provider telemetry failed: %d", rec.Code)
	}
	rec = doMuxReq(t, mux, http.MethodDelete, "/api/hoxi/provider/telemetry", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE all telemetry failed: %d", rec.Code)
	}
}

func TestProviderAuthAndGlobalConfigWhenOpenCodeDown(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)

	// Pick an unused closed loopback port so OpenCode is genuinely unreachable (connection refused).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	badGatewayProxy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusBadGateway, "opencode service unavailable")
	})

	svc := NewService(closedPort, "secret", workspaceDir, badGatewayProxy)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// 1. PUT /auth/<providerID> must persist to engine auth.json and return 200 OK instead of 502.
	rec := doMuxReq(t, mux, http.MethodPut, "/auth/my-custom-gw", map[string]any{
		"type": "api",
		"key":  "sk-offline-saved-key",
	})
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "true" {
		t.Fatalf("PUT /auth/my-custom-gw when OpenCode down: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := svc.ResolveStoredProviderKey("my-custom-gw", ""); got != "sk-offline-saved-key" {
		t.Fatalf("ResolveStoredProviderKey after offline PUT /auth = %q, want sk-offline-saved-key", got)
	}

	// 2. PATCH and GET /global/config must succeed from .hoxi/models.json even when OpenCode is down.
	rec = doMuxReq(t, mux, http.MethodPatch, "/global/config", map[string]any{
		"model": "my-custom-gw/gpt-4o",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "my-custom-gw/gpt-4o") {
		t.Fatalf("PATCH /global/config when OpenCode down: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doMuxReq(t, mux, http.MethodGet, "/global/config", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "my-custom-gw/gpt-4o") {
		t.Fatalf("GET /global/config when OpenCode down: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 3. DELETE /auth/<providerID> must remove the persisted key and return 200 OK instead of 502.
	rec = doMuxReq(t, mux, http.MethodDelete, "/auth/my-custom-gw", nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "true" {
		t.Fatalf("DELETE /auth/my-custom-gw when OpenCode down: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := svc.ResolveStoredProviderKey("my-custom-gw", ""); got != "" {
		t.Fatalf("expected deleted key to be empty, got %q", got)
	}

	// 4. GET /auth/<providerID> still delegates to the upstream proxy handler.
	rec = doMuxReq(t, mux, http.MethodGet, "/auth/my-custom-gw", nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected GET /auth/my-custom-gw to delegate to proxy (502), got %d", rec.Code)
	}
}
