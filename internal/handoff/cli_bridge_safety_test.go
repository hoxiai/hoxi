package handoff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/provider"
)

func TestClaudeBridgeTextOnlyExecution(t *testing.T) {
	binDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args")
	cwdPath := filepath.Join(t.TempDir(), "cwd")
	t.Setenv("PATH", binDir)
	t.Setenv("BRIDGE_ARGS", argsPath)
	t.Setenv("BRIDGE_CWD", cwdPath)
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$BRIDGE_ARGS\"\npwd > \"$BRIDGE_CWD\"\nprintf '%s\\n' '{\"type\":\"result\",\"result\":\"text reply\"}'\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(0, "", t.TempDir(), nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	for _, stream := range []string{"false", "true"} {
		body := `{"model":"test-model","stream":` + stream + `,"sessionID":"untrusted","directory":"/untrusted","messages":[{"role":"user","content":"change files"}],"tools":[{"type":"function"}]}`
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/bridge/claude-code/v1/chat/completions?directory=/untrusted", strings.NewReader(body)))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "text reply") {
			t.Fatalf("stream=%s: HTTP %d: %s", stream, rec.Code, rec.Body.String())
		}
		args, err := os.ReadFile(argsPath)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"-p", "change files", "--output-format", "json", "--tools", "", "--disallowedTools", "*", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--disable-slash-commands", "--settings", `{"disableAllHooks":true}`, "--model", "test-model"}
		if got := strings.Split(strings.TrimSuffix(string(args), "\n"), "\n"); !reflect.DeepEqual(got, want) {
			t.Fatalf("unsafe or unexpected arguments: %q", got)
		}
		cwd, err := os.ReadFile(cwdPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(cwd)) == svc.rootDir {
			t.Fatal("model request incorrectly bound to gateway root")
		}
	}
}

func TestAntigravityBridgeFailsClosed(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "executed")
	t.Setenv("GEMINI_HOME", binDir)
	t.Setenv("PATH", binDir)
	t.Setenv("BRIDGE_MARKER", marker)
	if err := os.WriteFile(filepath.Join(binDir, "agy"), []byte("#!/bin/sh\nprintf executed > \"$BRIDGE_MARKER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(0, "", t.TempDir(), nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/bridge/gemini-antigravity/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"edit files"}]}`)))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "text-only execution") {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("agy must not execute: %v", err)
	}
}

func TestBridgeModelCapabilities(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	svc := NewService(0, "", root, provider.NewService(0, "", root, nil))
	providers := map[string]interface{}{}
	for pid, url := range map[string]string{
		"claude-code": "http://127.0.0.1:1/api/hoxi/bridge/claude-code/v1",
		"google":      "http://127.0.0.1:1/api/hoxi/bridge/gemini-antigravity/v1",
		"grok-cli":    "http://127.0.0.1:1/api/hoxi/bridge/grok-cli/v1",
		"direct":      "https://api.example/v1",
	} {
		providers[pid] = map[string]interface{}{"name": "Remote", "options": map[string]interface{}{"baseURL": url}, "models": map[string]interface{}{"existing": map[string]interface{}{"tool_call": true}}}
	}
	if err := svc.persistHoxiConfigDoc(map[string]interface{}{"provider": providers}); err != nil {
		t.Fatal(err)
	}
	svc.SetWSBaseURL("ws://127.0.0.1:2")
	// A handoff may add a previously unknown model after bridge registration.
	svc.ensureHandoffModelReady(ExternalToolGemini, "google", "new-model", nil)
	doc := svc.loadHoxiConfigDoc()
	raw, _ := json.Marshal(doc)
	for pid, p := range doc["provider"].(map[string]interface{}) {
		for id, m := range p.(map[string]interface{})["models"].(map[string]interface{}) {
			want := pid != "claude-code" && pid != "google"
			if got := m.(map[string]interface{})["tool_call"]; got != want {
				t.Fatalf("%s/%s tool_call=%v, want %v: %s", pid, id, got, want, raw)
			}
		}
	}
}

func TestImportedBridgeCapabilities(t *testing.T) {
	root := t.TempDir()
	home := setupSyntheticCLIHome(t, root)
	t.Setenv("PATH", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	svc := NewService(0, "", root, provider.NewService(0, "", root, nil))
	for _, textOnly := range []bool{true, false} {
		claudeKey, geminiKey := "sk-ant-api-test", "gemini-api-test"
		if textOnly {
			claudeKey, geminiKey = "sk-ant-oat-test", "ya29.test"
		}
		t.Setenv("ANTHROPIC_AUTH_TOKEN", claudeKey)
		t.Setenv("ANTHROPIC_API_KEY", claudeKey)
		t.Setenv("GEMINI_API_KEY", geminiKey)
		settings := `{"model":"test-model","env":{"ANTHROPIC_AUTH_TOKEN":"` + claudeKey + `"}}`
		if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, resp := range []LocalCLIImportResponse{svc.importClaudeCLIProvider(), svc.importGeminiCLIProvider()} {
			if !resp.OK || len(resp.Models) == 0 {
				t.Fatalf("import failed: %+v", resp)
			}
			p := svc.loadHoxiProvidersMap()[resp.ProviderID].(map[string]interface{})
			for id, m := range p["models"].(map[string]interface{}) {
				if got := m.(map[string]interface{})["tool_call"]; got != !textOnly {
					t.Fatalf("textOnly=%v: %s/%s tool_call=%v", textOnly, resp.ProviderID, id, got)
				}
			}
		}
	}
}
