package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newRelayTestService(t *testing.T, upstreamURL string) (*Service, *http.ServeMux) {
	t.Helper()
	_, workspaceDir := setupIsolatedEnv(t)
	svc := NewService(0, "secret", workspaceDir, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	mux.HandleFunc(relayModelsPath, svc.ServeRelay)
	mux.HandleFunc(relayChatPath, svc.ServeRelay)
	svc.SetRelayBaseURL("http://127.0.0.1:5410")
	if upstreamURL != "" {
		doc := map[string]interface{}{
			"provider": map[string]interface{}{
				"relay-up": map[string]interface{}{
					"npm": "@ai-sdk/openai-compatible",
					"options": map[string]interface{}{
						"baseURL": upstreamURL,
						"apiKey":  "sk-upstream-secret",
					},
					"models": map[string]interface{}{
						"gpt-test": map[string]interface{}{"name": "GPT Test"},
					},
				},
			},
		}
		if err := svc.PersistHoxiConfigDoc(doc); err != nil {
			t.Fatalf("PersistHoxiConfigDoc: %v", err)
		}
	}
	return svc, mux
}

func doRelayReq(t *testing.T, h http.Handler, method, path, token string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRelayKeyLifecycleNeverReturnsSecretTwice(t *testing.T) {
	svc, mux := newRelayTestService(t, "")

	rec := doRelayReq(t, mux, http.MethodPost, "/api/hoxi/provider/relay/keys", "", `{"label":"cline"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create key status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created relayKeyCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !strings.HasPrefix(created.Token, relayKeyTokenPrefix) {
		t.Fatalf("token must start with %q, got %q", relayKeyTokenPrefix, created.Token)
	}
	if created.Key.Label != "cline" || created.Key.ID == "" {
		t.Fatalf("unexpected key metadata: %+v", created.Key)
	}

	rec = doRelayReq(t, mux, http.MethodGet, "/api/hoxi/provider/relay/keys", "", "")
	if strings.Contains(rec.Body.String(), created.Token) {
		t.Fatalf("list response leaked the plaintext token: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), hashRelayToken(created.Token)) {
		t.Fatalf("list response leaked the token hash: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), created.Key.Prefix) {
		t.Fatalf("list response should include the display prefix: %s", rec.Body.String())
	}

	if !svc.verifyRelayToken(created.Token) {
		t.Fatal("issued token must verify")
	}
	if svc.verifyRelayToken("hk-not-a-real-key") {
		t.Fatal("unknown token must not verify")
	}
	if svc.verifyRelayToken("") {
		t.Fatal("empty token must not verify")
	}

	rec = doRelayReq(t, mux, http.MethodDelete, "/api/hoxi/provider/relay/keys?id="+created.Key.ID, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d", rec.Code)
	}
	if svc.verifyRelayToken(created.Token) {
		t.Fatal("revoked token must stop verifying")
	}
	// Revoking twice is idempotent.
	rec = doRelayReq(t, mux, http.MethodDelete, "/api/hoxi/provider/relay/keys?id="+created.Key.ID, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("second revoke status = %d", rec.Code)
	}
}

func TestRelayRejectsMissingAndInvalidKeys(t *testing.T) {
	_, mux := newRelayTestService(t, "")

	for _, token := range []string{"", "sk-upstream-secret", "hk-deadbeef"} {
		rec := doRelayReq(t, mux, http.MethodGet, "/v1/models", token, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: expected 401, got %d", token, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "sk-upstream-secret") {
			t.Fatalf("error body must not echo provider credentials: %s", rec.Body.String())
		}
	}
}

func TestRelayAcceptsLowercaseBearerAndPreflight(t *testing.T) {
	svc, mux := newRelayTestService(t, "")
	_, token, err := svc.CreateRelayKey("")
	if err != nil {
		t.Fatalf("CreateRelayKey: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, relayModelsPath, nil)
	req.Header.Set("Authorization", "bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("lowercase bearer must authenticate, got %d %s", rec.Code, rec.Body.String())
	}

	// Browser AI clients send a preflight without credentials.
	req = httptest.NewRequest(http.MethodOptions, relayChatPath, nil)
	req.Header.Set("Origin", "http://localhost:8080")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight must allow the browser origin, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestRelayModelsListHidesAnthropicAndUsesNamespacedIDs(t *testing.T) {
	svc, mux := newRelayTestService(t, "http://127.0.0.1:9")
	doc := svc.LoadHoxiConfigDoc()
	providers := doc["provider"].(map[string]interface{})
	providers["anthropic-native"] = map[string]interface{}{
		"npm":    "@ai-sdk/anthropic",
		"models": map[string]interface{}{"claude-x": map[string]interface{}{"name": "Claude X"}},
	}
	if err := svc.PersistHoxiConfigDoc(doc); err != nil {
		t.Fatalf("persist: %v", err)
	}
	info, token, err := svc.CreateRelayKey("")
	if err != nil {
		t.Fatalf("CreateRelayKey: %v", err)
	}
	_ = info

	rec := doRelayReq(t, mux, http.MethodGet, "/v1/models", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("models status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"relay-up/gpt-test"`) {
		t.Fatalf("expected namespaced model id, got %s", body)
	}
	if !strings.Contains(body, `"hoxi-auto"`) {
		t.Fatalf("expected the auto model id, got %s", body)
	}
	if strings.Contains(body, "claude-x") {
		t.Fatalf("anthropic-native providers must not be advertised: %s", body)
	}
	if strings.Contains(body, "sk-upstream-secret") {
		t.Fatalf("model list must not contain credentials: %s", body)
	}
}

func TestRelayChatForwardsBodyAndInjectsUpstreamKey(t *testing.T) {
	var gotAuth, gotPath, gotModel string
	var gotTools bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotModel, _ = payload["model"].(string)
		_, gotTools = payload["tools"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"relay-ok"}}]}`))
	}))
	defer upstream.Close()

	svc, mux := newRelayTestService(t, upstream.URL+"/v1")
	_, token, err := svc.CreateRelayKey("")
	if err != nil {
		t.Fatalf("CreateRelayKey: %v", err)
	}

	body := `{"model":"relay-up/gpt-test","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"noop"}}]}`
	rec := doRelayReq(t, mux, http.MethodPost, "/v1/chat/completions", token, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if gotModel != "gpt-test" {
		t.Fatalf("upstream model = %q, want the bare model id", gotModel)
	}
	if gotAuth != "Bearer sk-upstream-secret" {
		t.Fatalf("upstream auth = %q", gotAuth)
	}
	if !gotTools {
		t.Fatal("tools must pass through untouched")
	}
	if !strings.Contains(rec.Body.String(), "relay-ok") {
		t.Fatalf("response body not forwarded: %s", rec.Body.String())
	}
}

func TestRelayChatStreamsSSEIncrementally(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"t%d\"}}]}\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	svc, mux := newRelayTestService(t, upstream.URL+"/v1")
	_, token, err := svc.CreateRelayKey("")
	if err != nil {
		t.Fatalf("CreateRelayKey: %v", err)
	}
	rec := doRelayReq(t, mux, http.MethodPost, "/v1/chat/completions", token,
		`{"model":"relay-up/gpt-test","stream":true,"messages":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("stream status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("content type = %q", got)
	}
	out := rec.Body.String()
	for _, want := range []string{"t0", "t1", "t2", "[DONE]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stream output missing %q: %s", want, out)
		}
	}
}

func TestRelayModelResolutionRejectsUnknownAndAmbiguousIDs(t *testing.T) {
	svc, _ := newRelayTestService(t, "http://127.0.0.1:9")
	doc := svc.LoadHoxiConfigDoc()
	providers := doc["provider"].(map[string]interface{})
	providers["other-up"] = map[string]interface{}{
		"npm":     "@ai-sdk/openai-compatible",
		"options": map[string]interface{}{"baseURL": "http://127.0.0.1:9/v1"},
		"models":  map[string]interface{}{"gpt-test": map[string]interface{}{"name": "Dup"}},
	}
	if err := svc.PersistHoxiConfigDoc(doc); err != nil {
		t.Fatalf("persist: %v", err)
	}

	if _, _, err := svc.relayResolveModel("gpt-test"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
	if _, _, err := svc.relayResolveModel("nope"); err == nil {
		t.Fatal("expected unknown-model error")
	}
	if _, _, err := svc.relayResolveModel("nope-provider/gpt-test"); err == nil {
		t.Fatal("expected unknown-provider error")
	}
	pid, mid, err := svc.relayResolveModel("relay-up/gpt-test")
	if err != nil || pid != "relay-up" || mid != "gpt-test" {
		t.Fatalf("explicit resolution = %q %q %v", pid, mid, err)
	}

	// Models whose bare ID contains "/" (e.g. OpenRouter "deepseek/deepseek-chat")
	// resolve both as "openrouter/deepseek/deepseek-chat" and as "deepseek/deepseek-chat".
	providers["openrouter"] = map[string]interface{}{
		"npm":     "@ai-sdk/openai-compatible",
		"options": map[string]interface{}{"baseURL": "http://127.0.0.1:9/v1"},
		"models": map[string]interface{}{
			"deepseek/deepseek-chat": map[string]interface{}{"name": "DeepSeek Chat"},
		},
	}
	if err := svc.PersistHoxiConfigDoc(doc); err != nil {
		t.Fatalf("persist: %v", err)
	}
	pid, mid, err = svc.relayResolveModel("deepseek/deepseek-chat")
	if err != nil || pid != "openrouter" || mid != "deepseek/deepseek-chat" {
		t.Fatalf("slashed bare model resolution = %q %q %v", pid, mid, err)
	}
	pid, mid, err = svc.relayResolveModel("openrouter/deepseek/deepseek-chat")
	if err != nil || pid != "openrouter" || mid != "deepseek/deepseek-chat" {
		t.Fatalf("explicit slashed model resolution = %q %q %v", pid, mid, err)
	}
	if _, _, err := svc.relayResolveModel("opencode/gpt-5"); err == nil {
		t.Fatal("expected opencode provider to be rejected on local relay")
	}
}

func TestRelayKeyStoreBacksUpCorruptFile(t *testing.T) {
	svc, _ := newRelayTestService(t, "")
	path := relayKeysPath()
	if err := writeRelayKeyDoc(path, relayKeyDoc{Version: 1}); err != nil {
		t.Fatalf("writeRelayKeyDoc: %v", err)
	}
	if _, _, err := svc.CreateRelayKey("next"); err != nil {
		t.Fatalf("CreateRelayKey after valid file: %v", err)
	}
}
