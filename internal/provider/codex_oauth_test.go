package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

func buildCodexTestJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal jwt claims: %v", err)
	}
	return hdr + "." + base64.RawURLEncoding.EncodeToString(raw) + ".sig"
}

func codexHTTPResponse(status int, contentType, body string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", contentType)
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestCodexOAuthNativeStreamCompleteProbeAndSecurityBoundaries(t *testing.T) {
	hoxiHome := t.TempDir()
	codexHome := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("HOXI_HOME", hoxiHome)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("HOME", t.TempDir())

	validJWT := buildCodexTestJWT(t, map[string]any{
		"exp": time.Now().Add(2 * time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_jwt_fallback",
		},
	})

	// Mock strict ChatGPT Codex backend (https://chatgpt.com/backend-api/codex/responses)
	mockRT := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://chatgpt.com/backend-api/codex/responses" {
			return codexHTTPResponse(http.StatusNotFound, "application/json", fmt.Sprintf(`{"detail":"unexpected url %s"}`, r.URL.String())), nil
		}
		if gotAuth := r.Header.Get("Authorization"); gotAuth != "Bearer "+validJWT {
			return codexHTTPResponse(http.StatusUnauthorized, "application/json", `{"detail":"bad bearer token"}`), nil
		}
		if gotAcct := r.Header.Get("ChatGPT-Account-Id"); gotAcct != "acct_pro_42" {
			return codexHTTPResponse(http.StatusForbidden, "application/json", `{"detail":"missing ChatGPT-Account-Id"}`), nil
		}
		if r.Header.Get("originator") != "codex_cli_rs" || r.Header.Get("version") == "" {
			return codexHTTPResponse(http.StatusForbidden, "application/json", `{"detail":"missing codex originator/version headers"}`), nil
		}

		rawBody, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(rawBody, &payload); err != nil {
			return codexHTTPResponse(http.StatusBadRequest, "application/json", `{"detail":"invalid json"}`), nil
		}
		for _, forbidden := range []string{"max_output_tokens", "max_tokens", "max_completion_tokens", "temperature"} {
			if _, exists := payload[forbidden]; exists {
				return codexHTTPResponse(http.StatusBadRequest, "application/json", fmt.Sprintf(`{"detail":"Unsupported parameter: %s"}`, forbidden)), nil
			}
		}
		instr, _ := payload["instructions"].(string)
		if strings.TrimSpace(instr) == "" {
			return codexHTTPResponse(http.StatusBadRequest, "application/json", `{"detail":"Instructions are required"}`), nil
		}
		if storeVal, ok := payload["store"].(bool); !ok || storeVal {
			return codexHTTPResponse(http.StatusBadRequest, "application/json", `{"detail":"Store must be set to false"}`), nil
		}
		if streamVal, ok := payload["stream"].(bool); !ok || !streamVal {
			return codexHTTPResponse(http.StatusBadRequest, "application/json", `{"detail":"Stream must be set to true"}`), nil
		}
		inpList, isList := payload["input"].([]any)
		if !isList || len(inpList) == 0 {
			return codexHTTPResponse(http.StatusBadRequest, "application/json", `{"detail":"Input must be a list"}`), nil
		}

		sseBody := "event: response.output_text.delta\n" +
			`data: {"type":"response.output_text.delta","delta":"Codex "}` + "\n\n" +
			"event: response.output_text.delta\n" +
			`data: {"type":"response.output_text.delta","delta":"OAuth OK"}` + "\n\n" +
			"event: response.completed\n" +
			`data: {"type":"response.completed","response":{"usage":{"input_tokens":12,"output_tokens":4}}}` + "\n\n"
		return codexHTTPResponse(http.StatusOK, "text/event-stream", sseBody), nil
	})

	mockTransport := &http.Transport{}
	mockTransport.RegisterProtocol("https", mockRT)
	origTransport := providerTransport
	origTitleTransport := titleTransport
	providerTransport = mockTransport
	titleTransport = mockTransport
	t.Cleanup(func() {
		providerTransport = origTransport
		titleTransport = origTitleTransport
	})

	svc := NewService(0, "", wsDir, nil)

	// Seed ~/.hoxi/models.json exactly as importCodexCLIProvider writes it (no baseURL, no apiKey)
	modelsDoc := map[string]any{
		"provider": map[string]any{
			"openai": map[string]any{
				"name": "OpenAI Codex (Local)",
				"npm":  "@ai-sdk/openai",
				"models": map[string]any{
					"gpt-6.2-astra": map[string]any{
						"name":      "GPT-6.2 Astra (Codex)",
						"tool_call": true,
						"reasoning": true,
					},
				},
				"options": map[string]any{
					"defaultModel": "gpt-6.2-astra",
					"codexVersion": "0.165.0",
				},
			},
		},
	}
	if err := svc.PersistHoxiConfigDoc(modelsDoc); err != nil {
		t.Fatalf("persist models.json: %v", err)
	}
	if err := svc.persistEngineProviderAuthMap("openai", map[string]any{
		"type":      "oauth",
		"access":    validJWT,
		"refresh":   "ref_initial",
		"expires":   time.Now().Add(2 * time.Hour).UnixMilli(),
		"accountId": "acct_pro_42",
	}); err != nil {
		t.Fatalf("persist auth.json: %v", err)
	}

	// 1. Native StreamTarget + OpenStream (even if caller body originally contained max_output_tokens and string input)
	st := svc.ResolveStreamTarget("openai", "gpt-6.2-astra")
	if st.Protocol != llm.ProtocolOpenAIResponses {
		t.Fatalf("expected ProtocolOpenAIResponses, got %q", st.Protocol)
	}
	if st.BaseURL != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("expected https://chatgpt.com/backend-api/codex, got %q", st.BaseURL)
	}
	rawCallerBody := []byte(`{"model":"gpt-6.2-astra","input":[{"role":"user","content":"hello"}],"max_output_tokens":4096,"stream":true,"store":false}`)
	streamResp, err := svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target: st,
		Body:   rawCallerBody,
	})
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	streamBytes, _ := io.ReadAll(streamResp.Body)
	_ = streamResp.Body.Close()
	if streamResp.StatusCode != http.StatusOK || !strings.Contains(string(streamBytes), "Codex ") {
		t.Fatalf("unexpected stream response status=%d body=%s", streamResp.StatusCode, string(streamBytes))
	}

	// 2. Complete() (lightweight LLM calls like session title generation)
	llmTarget := svc.Resolve("openai", "gpt-6.2-astra", "", "")
	gotText, err := svc.Complete(context.Background(), llmTarget, llm.Request{
		Prompt:    "Generate short title",
		MaxTokens: 32,
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if gotText != "Codex OAuth OK" {
		t.Fatalf("Complete text = %q, want %q", gotText, "Codex OAuth OK")
	}

	// 3. Settings "Test Connection" endpoint with default UI placeholder BaseURL ("https://api.openai.com/v1") and masked key
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	probeBody, _ := json.Marshal(testProviderModelRequest{
		ProviderID: "openai",
		BaseURL:    "https://api.openai.com/v1",
		APIKey:     "********",
		NPM:        "@ai-sdk/openai",
		ModelID:    "gpt-6.2-astra",
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/provider/test-model", bytes.NewReader(probeBody)))
	var probeRes testProviderModelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &probeRes); err != nil {
		t.Fatalf("unmarshal probe response: %v", err)
	}
	if !probeRes.OK {
		t.Fatalf("expected probe OK, got error: %s", probeRes.Error)
	}

	// 4. Security rejection: untrusted hintBaseURL must NEVER receive the ChatGPT OAuth token or headers
	_, leakedKey, _, leakedHeaders := svc.resolveEffectiveProviderConnection("openai", "https://attacker.hoxi.ai/v1", "@ai-sdk/openai")
	if leakedKey != "" || len(leakedHeaders) != 0 {
		t.Fatalf("OAuth credential leaked to untrusted hintBaseURL: key=%q headers=%v", leakedKey, leakedHeaders)
	}

	// 5. Explicit developer API key in options must stay on standard https://api.openai.com/v1 (never hit ChatGPT OAuth backend)
	modelsDoc["provider"].(map[string]any)["openai"].(map[string]any)["options"].(map[string]any)["apiKey"] = "sk-explicit-dev-key"
	if err := svc.PersistHoxiConfigDoc(modelsDoc); err != nil {
		t.Fatalf("rewrite models.json: %v", err)
	}
	devURL, devKey, _, devHeaders := svc.resolveEffectiveProviderConnection("openai", "", "")
	if devURL != "https://api.openai.com/v1" || devKey != "sk-explicit-dev-key" || devHeaders["ChatGPT-Account-Id"] != "" {
		t.Fatalf("explicit API key was misrouted: url=%q key=%q headers=%v", devURL, devKey, devHeaders)
	}
}

func TestCodexOAuthAdoptsRotatedLocalCLICredentialWithoutPoisoningRevoked(t *testing.T) {
	hoxiHome := t.TempDir()
	codexHome := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("HOXI_HOME", hoxiHome)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("HOME", t.TempDir())

	svc := NewService(0, "", wsDir, nil)
	if err := svc.PersistHoxiConfigDoc(map[string]any{
		"provider": map[string]any{
			"openai": map[string]any{
				"name": "OpenAI Codex (Local)",
				"npm":  "@ai-sdk/openai",
				"models": map[string]any{
					"gpt-6.2-astra": map[string]any{"name": "GPT-6.2 Astra"},
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Engine auth.json has an expired/revoked token from an old session
	if err := svc.persistEngineProviderAuthMap("openai", map[string]any{
		"type":          "oauth",
		"revoked":       true,
		"revokedReason": "invalid_grant",
	}); err != nil {
		t.Fatal(err)
	}

	// Meanwhile ~/.codex/auth.json has a fresh JWT after the user ran `codex login` in terminal
	freshJWT := buildCodexTestJWT(t, map[string]any{
		"exp": time.Now().Add(3 * time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_fresh_cli",
		},
	})
	cliAuth := fmt.Sprintf(`{"auth_mode":"chatgpt","tokens":{"access_token":%q,"refresh_token":"ref_fresh_cli","account_id":"acct_fresh_cli"}}`, freshJWT)
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(cliAuth), 0o600); err != nil {
		t.Fatal(err)
	}

	gotURL, gotTok, _, gotHeaders := svc.resolveEffectiveProviderConnection("openai", "", "")
	if gotTok != freshJWT {
		t.Fatalf("expected fresh CLI JWT to be adopted, got %q", gotTok)
	}
	if gotURL != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("expected https://chatgpt.com/backend-api/codex, got %q", gotURL)
	}
	if gotHeaders["ChatGPT-Account-Id"] != "acct_fresh_cli" {
		t.Fatalf("expected ChatGPT-Account-Id acct_fresh_cli, got %v", gotHeaders)
	}
}
