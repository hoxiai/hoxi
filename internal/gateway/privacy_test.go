package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/privacy"
)

func TestAIConsentClassification(t *testing.T) {
	blocked := []string{
		"/session/s/message", "/session/s/prompt", "/session/s/prompt_async",
		"/session/s/command", "/session/s/summarize", "/session/s/shell", "/session/s/init",
		"/api/hoxi/runner/sessions/s/prompt", "/api/hoxi/runner/sessions/s/prompt_async",
		"/api/hoxi/provider/test-model", "/api/hoxi/provider/audit",
		"/api/hoxi/provider/summarize-title", "/api/hoxi/provider/generate-commit-message",
		"/api/hoxi/bridge/grok-cli/v1/chat/completions",
		"/api/hoxi/bridge/grok-cli/v1/responses",
		"/api/hoxi/bridge/grok-cli/",
	}
	for _, p := range append(blocked, "/api/session/s/message", "/api/session/s/command", "/api/session/s/shell", "/api/session/s/prompt_async", "/api/session/s/summarize", "/api/session/s/init") {
		if !requiresAIConsent(http.MethodPost, p) {
			t.Errorf("execution not guarded: %s", p)
		}
		if requiresAIConsent(http.MethodOptions, p) {
			t.Errorf("preflight blocked: %s", p)
		}
	}
	allowed := []struct{ method, path string }{
		{"GET", "/session/s/message"}, {"GET", "/api/session/s/message"},
		{"POST", "/session/s/fork"}, {"POST", "/api/session/s/fork"},
		{"POST", "/session/s/abort"}, {"POST", "/api/session/s/abort"},
		{"POST", "/session"}, {"PATCH", "/session/s"},
		{"DELETE", "/session/s/message/m"}, {"POST", "/session/s/revert"},
		{"GET", "/api/hoxi/runner/sessions/s/message"},
		{"POST", "/api/hoxi/runner/sessions/s/abort"},
		{"POST", "/api/hoxi/runner/sessions"},
		{"POST", "/api/hoxi/provider/fetch-models"},
		{"PUT", "/api/hoxi/models-config"}, {"PATCH", "/global/config"},
		{"GET", "/api/hoxi/privacy"}, {"PUT", "/api/hoxi/privacy"},
		{"GET", "/api/hoxi/bridge/grok-cli/v1/models"},
		{"GET", "/assets/main.js"}, {"GET", "/event"},
	}
	for _, tt := range allowed {
		if requiresAIConsent(tt.method, tt.path) {
			t.Errorf("non-execution blocked: %s %s", tt.method, tt.path)
		}
	}
	// The Grok bridge forwards even GET bodies to arbitrary upstream paths.
	if !requiresAIConsent("GET", "/api/hoxi/bridge/grok-cli/v1/chat/completions") {
		t.Fatal("Grok forwarding bypass")
	}
}

func TestPrivacyEndpointAndGuard(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "privacy.json")
	g := &Gateway{mux: http.NewServeMux(), privacy: privacy.New(statePath)}
	g.mux.HandleFunc("/api/hoxi/privacy", g.handlePrivacy)
	calls := 0
	g.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	})
	request := func(method, p, body string, code int) *httptest.ResponseRecorder {
		t.Helper()
		r := localRequest(method, p, strings.NewReader(body))
		r.Header.Set("Origin", "wails://wails")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != code {
			t.Fatalf("%s %s: got %d, want %d: %s", method, p, w.Code, code, w.Body.String())
		}
		return w
	}
	checkStatus := func(accepted bool) {
		t.Helper()
		w := request("GET", "/api/hoxi/privacy", "", 200)
		var status map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if len(status) != 2 || status["version"] != privacy.Version || status["accepted"] != accepted {
			t.Fatalf("unexpected endpoint schema: %v", status)
		}
	}
	checkStatus(false)
	w := request("POST", "/api/session/s/message", `{}`, 403)
	if calls != 0 || !strings.Contains(w.Body.String(), `"code":"privacy_consent_required"`) || w.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatal("guard must run before handler and preserve CORS")
	}
	for _, body := range []string{`{}`, `null`, `{"version":"old","accepted":true}`, `{"version":"2026-10-18","accepted":null}`, `{"version":"2026-10-18","accepted":"true"}`, `{"version":"2026-10-18","accepted":true} true`} {
		request("PUT", "/api/hoxi/privacy", body, 400)
		checkStatus(false)
	}
	request("POST", "/api/hoxi/privacy", `{}`, 405)
	request("PUT", "/api/hoxi/privacy", `{"version":"2026-10-18","accepted":true}`, 200)
	checkStatus(true)
	request("POST", "/api/session/s/message", `{}`, 204)
	request("PUT", "/api/hoxi/privacy", `{"version":"2026-10-18","accepted":false}`, 200)
	g.privacy = privacy.New(statePath)
	checkStatus(false)
	for _, p := range []string{"/session/s/message", "/api/hoxi/runner/sessions/s/prompt", "/api/hoxi/provider/audit", "/api/hoxi/bridge/grok-cli/v1/chat/completions"} {
		request("POST", p, `{}`, 403)
	}
	if calls != 1 {
		t.Fatalf("denied requests reached handler: %d", calls)
	}
	request("POST", "/session/s/abort", `{}`, 204)
	request("POST", "/session/s/fork", `{}`, 204)
	request("GET", "/session/s/message", "", 204)
	g.privacy = privacy.New(t.TempDir()) // A directory cannot be replaced by the state file.
	request("PUT", "/api/hoxi/privacy", `{"version":"2026-10-18","accepted":true}`, 500)
	checkStatus(false)
	request("POST", "/session/s/message", `{}`, 403)
	g.privacy = nil
	request("POST", "/session/s/message", `{}`, 403)
}
