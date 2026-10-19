package provider

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestAuditQualityRequiresValidObserverScore(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	_, ocPort := newMockOpenCodeServer(t)
	for _, tc := range []struct {
		name    string
		content string
		status  int
		baseURL string
		ok      bool
	}{
		{name: "zero", content: `{"score":0,"summary":"Incorrect implementation"}`, ok: true},
		{name: "malformed", content: "Looks good overall"},
		{name: "missing score", content: `{"summary":"Looks good"}`},
		{name: "out of range", content: `{"score":101}`},
		{name: "nonfinite", content: `{"score":NaN}`},
		{name: "observer failure", status: http.StatusServiceUnavailable},
		{name: "invalid endpoint", baseURL: "invalid://observer"},
	} {
		for _, lspErrors := range []int{0, 1} {
			t.Run(tc.name+strings.Repeat(" with static deduction", lspErrors), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.status != 0 {
						w.WriteHeader(tc.status)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"choices": []any{map[string]any{"message": map[string]any{"content": tc.content}}},
					})
				}))
				defer server.Close()
				baseURL := server.URL + "/v1"
				if tc.baseURL != "" {
					baseURL = tc.baseURL
				}
				svc := NewService(ocPort, "secret", workspaceDir, nil)
				svc.telemetry = &providerTelemetryStore{records: make(map[string]*providerTelemetryStats)}
				svc.telemetry.recordAuditResult("worker", "model", 95, true, "Previous audit")
				before, _ := json.Marshal(svc.telemetry.records)
				mux := http.NewServeMux()
				svc.RegisterRoutes(mux)
				rec := doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/audit", auditCodeRequest{
					WorkerProviderID: "worker", WorkerModelID: "model",
					ObserverProviderID: "observer", ObserverModelID: "model",
					ObserverBaseURL: baseURL, LspErrorCount: lspErrors,
				})
				var result auditCodeResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if rec.Code != http.StatusOK || result.OK != tc.ok {
					t.Fatalf("unexpected audit response: %d %s", rec.Code, rec.Body.String())
				}
				if !tc.ok {
					after, _ := json.Marshal(svc.telemetry.records)
					if result.Error == "" || !bytes.Equal(before, after) {
						t.Fatalf("failed observer must return error and leave telemetry unchanged: %s, %s", rec.Body.String(), after)
					}
					return
				}
				stats := svc.telemetry.records["worker"]
				model := stats.Models["model"]
				if result.Score != 0 || result.Pass || stats.AuditTotalCount != 2 || model.AuditTotalCount != 2 ||
					len(stats.AuditScores) != 2 || stats.AuditScores[1] != 0 || len(model.AuditScores) != 2 || model.AuditScores[1] != 0 {
					t.Fatalf("zero score was not recorded correctly: %+v, %+v, %+v", result, stats, model)
				}
				telemetry := doMuxReq(t, mux, http.MethodGet, "/api/hoxi/provider/telemetry", nil)
				if strings.Count(telemetry.Body.String(), `"lastAuditScore":0`) != 2 {
					t.Fatalf("provider and model must serialize lastAuditScore zero: %s", telemetry.Body.String())
				}
			})
		}
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

func TestFetchProbeAuditSummarizeAndTelemetryRoutes(t *testing.T) {
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

	// Seed anthropic provider config in Hoxi models.json so resolveProviderConnection resolves credentials & headers
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

	// 3. POST /api/hoxi/provider/audit (OpenAI & Anthropic observer + static rules)
	rec = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/audit", map[string]any{
		"workerProviderID":   "custom-oai",
		"workerModelID":      "gpt-4o",
		"observerProviderID": "custom-oai",
		"observerModelID":    "gpt-4o",
		"observerBaseURL":    llmSrv.URL + "/v1",
		"userPrompt":         "Add feature X",
		"diffs": []map[string]any{
			{"file": "main.go", "after": "package main\nfunc main() {}\n", "additions": 2},
		},
		"locale": "en-US",
	})
	if !strings.Contains(rec.Body.String(), `"pass":true`) || !strings.Contains(rec.Body.String(), `"score":94`) {
		t.Fatalf("audit OpenAI failed: %s", rec.Body.String())
	}

	rec = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/audit", map[string]any{
		"workerProviderID":   "anthropic",
		"workerModelID":      "claude-3-7-sonnet",
		"observerProviderID": "anthropic",
		"observerModelID":    "claude-3-7-sonnet",
		"userPrompt":         "Fix bug",
		"lspErrorCount":      1,
		"lspErrorsSummary":   "type error",
		"locale":             "zh-CN",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"score":76`) {
		t.Fatalf("audit Anthropic unexpected response: %d %s", rec.Code, rec.Body.String())
	}

	// 4. POST /api/hoxi/provider/summarize-title (mock LLM + fallback)
	rec = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/summarize-title", map[string]any{
		"providerID": "custom-oai",
		"modelID":    "gpt-4o",
		"baseURL":    llmSrv.URL + "/v1",
		"prompt":     "请帮我重构 provider 单元测试",
		"locale":     "zh-CN",
	})
	if !strings.Contains(rec.Body.String(), `"ok":true`) || !strings.Contains(rec.Body.String(), "重构 Provider 单元测试") {
		t.Fatalf("summarize-title LLM failed: %s", rec.Body.String())
	}

	rec = doMuxReq(t, mux, http.MethodPost, "/api/hoxi/provider/summarize-title", map[string]any{
		"prompt": "Fix the login button alignment on macOS.",
		"locale": "en-US",
	})
	if !strings.Contains(rec.Body.String(), "Fix the login button alignment on macOS") {
		t.Fatalf("summarize-title fallback failed: %s", rec.Body.String())
	}

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
