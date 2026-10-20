package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The AI helpers moved into their domain packages; their public routes must
// keep answering under /api/hoxi/provider/.
func TestAssistRoutesKeepProviderPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	gw := New(0, "test-pass", t.TempDir(), nil, nil)
	for _, tc := range []struct {
		path, body, want string
	}{
		{"/api/hoxi/provider/summarize-title", `{"prompt":"Fix the login button alignment on macOS.","locale":"en-US"}`, "Fix the login button alignment on macOS"},
		{"/api/hoxi/provider/enhance-prompt", `{"prompt":"retry"}`, "No model is available for prompt enhancement"},
		{"/api/hoxi/provider/generate-commit-message", `{"diffSummary":"M a.go"}`, "LLM commit synthesis unavailable"},
		{"/api/hoxi/provider/audit", `{}`, "Observer model is not configured"},
	} {
		req := localRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		gw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s: unexpected response %d %s", tc.path, rec.Code, rec.Body.String())
		}
	}
}
