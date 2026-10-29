package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTunnelRouteAllowlist(t *testing.T) {
	get, post := http.MethodGet, http.MethodPost
	type route struct{ method, path string }
	allowed := []route{
		{get, "/healthz"}, {get, "/api/hoxi/bootstrap"}, {get, "/api/hoxi/system-info"},
		{get, "/api/hoxi/provider/models"}, {get, "/api/hoxi/runner/claude-code/models"},
		{get, "/api/hoxi/runner/antigravity/models"}, {get, "/api/hoxi/runner/gemini-cli/models"},
		{get, "/api/hoxi/runner/qoder/models"},
		{get, "/api/hoxi/runner/codebuddy/models"},
		{get, "/api/hoxi/sessions"}, {post, "/api/hoxi/sessions"}, {get, "/api/hoxi/sessions/status"},
		{get, "/api/hoxi/sessions/ses_1"}, {get, "/api/hoxi/sessions/ses_1/message"},
		{get, "/api/hoxi/sessions/ses_1/diff"},
		{post, "/api/hoxi/sessions/ses_1/prompt"}, {post, "/api/hoxi/sessions/ses_1/prompt_async"},
		{post, "/api/hoxi/sessions/ses_1/abort"}, {post, "/api/hoxi/sessions/ses_1/stop-tool"},
		{post, "/api/hoxi/sessions/ses_1/revert"}, {post, "/api/hoxi/sessions/ses_1/unrevert"},
		{post, "/api/hoxi/sessions/ses_1/fork"},
		{get, "/api/hoxi/event"}, {get, "/api/hoxi/permission"}, {post, "/api/hoxi/permission/per_1/reply"},
		{get, "/api/hoxi/worktree/status"}, {get, "/api/hoxi/worktree/diff"}, {get, "/api/hoxi/worktree/branches"},
		{get, "/api/hoxi/worktree/verify-status"}, {post, "/api/hoxi/worktree/commit"}, {post, "/api/hoxi/worktree/apply"},
		{post, "/api/hoxi/attachments"}, {get, "/api/hoxi/attachments/att_1"}, {get, "/api/hoxi/attachments/att_1/content"},
		{get, "/api/hoxi/skills/list"}, {get, "/api/hoxi/skills/marketplace"}, {get, "/api/hoxi/workspaces"},
		{get, "/file/content"}, {get, "/api/file/content"}, {get, "/api/hoxi/file/content"},
		{get, "/api/hoxi/file/list"}, {get, "/api/hoxi/file/search"},
	}
	for _, r := range allowed {
		if !tunnelRouteAllowed(r.method, r.path) {
			t.Errorf("%s %s should be reachable over the tunnel", r.method, r.path)
		}
	}
	denied := []route{
		{get, "/api/hoxi/computer/apps"}, {get, "/api/hoxi/computer/permissions"}, {get, "/api/hoxi/computer/state"}, {post, "/api/hoxi/computer/action"}, {post, "/api/hoxi/computer/grant"}, {post, "/api/hoxi/computer/revoke"}, {post, "/api/hoxi/computer/pause"}, {post, "/api/hoxi/computer/stop"},
		{get, "/api/hoxi/browser/extension"}, {post, "/api/hoxi/browser/action"}, {get, "/api/hoxi/browser/stream"},
		{get, "/api/hoxi/browser/workflows"}, {post, "/api/hoxi/browser/workflows"}, {get, "/api/hoxi/browser/diagnostics"}, {post, "/api/hoxi/browser/diagnostics"},
		{post, "/api/hoxi/browser/upload"}, {post, "/api/hoxi/browser/download-save"}, {post, "/api/hoxi/browser/download-open"},
		{post, "/api/hoxi/browser/download-reveal"}, {post, "/api/hoxi/browser/download-cancel"},
		{get, "/"}, {get, "/api/hoxi/tunnel/status"}, {post, "/api/hoxi/tunnel/pair"}, {post, "/api/hoxi/tunnel/disable"},
		{post, "/api/hoxi/tunnel/pair/request"}, {post, "/api/hoxi/tunnel/pair/cancel"}, {get, "/api/hoxi/tunnel/pair/request/req_1"},
		// The LAN pairing token must never be readable from a paired GoPanel.
		{get, "/api/hoxi/pairing/qr"}, {post, "/api/hoxi/pairing/qr"}, {post, "/api/hoxi/pairing/rotate"},
		// The Cloudflare connector token and control must stay desktop-only.
		{get, "/api/hoxi/cloudflared/status"}, {post, "/api/hoxi/cloudflared/enable"}, {post, "/api/hoxi/cloudflared/disable"},
		{get, "/api/hoxi/auth"}, {get, "/api/hoxi/models-config"},
		{get, "/api/hoxi/provider/keys"}, {post, "/api/hoxi/provider/fetch-models"}, {post, "/api/hoxi/provider/test-model"},
		{get, "/api/hoxi/provider/auto-pools"}, {get, "/api/hoxi/provider/models/x"}, {get, "/provider"}, {get, "/global/config"},
		{get, "/api/hoxi/browser"}, {get, "/api/hoxi/clipboard/images"}, {get, "/api/hoxi/sessionsx"},
		{get, "/api/hoxi/sessions/../tunnel/pair"}, {get, "/session"}, {get, "/pty/1/connect"}, {get, "/config"}, {get, "/auth/openai"},
		{post, "/api/hoxi/desktop/notify"}, {post, "/api/hoxi/desktop/focus"}, {get, "/api/hoxi/dev-pulse"},
		{get, "/api/hoxi/desktop/notify/permission"}, {post, "/api/hoxi/desktop/notify/permission"},
		{get, "/api/hoxi/dev-pulse/history"}, {get, "/api/hoxi/request-log"}, {get, "/api/hoxi/request-log/content"}, {get, "/api/hoxi/request-log"}, {get, "/api/hoxi/request-log"}, {get, "/api/hoxi/request-log"}, {get, "/api/hoxi/request-log"}, {get, "/api/hoxi/request-log"},
		// The local OpenAI-compatible relay plane mints and consumes provider
		// credentials, so neither its data routes nor its key admin route may
		// ever be reachable from a paired GoPanel.
		{get, "/v1/models"}, {post, "/v1/chat/completions"}, {get, "/api/hoxi/provider/relay/keys"},
		{post, "/api/hoxi/provider/relay/keys"}, {http.MethodDelete, "/api/hoxi/provider/relay/keys"},
		// Raw OpenCode passthrough and command-running local helpers.
		{get, "/file"}, {post, "/file"}, {get, "/api/file"}, {post, "/api/hoxi/file/open"}, {post, "/api/hoxi/file/validate"},
		// Worktree routes that execute commands, write config, push, check out or delete.
		{post, "/api/hoxi/worktree/verify-run"}, {post, "/api/hoxi/worktree/verify-config"}, {post, "/api/hoxi/worktree/verify-stop"},
		{post, "/api/hoxi/worktree/push"}, {post, "/api/hoxi/worktree/checkout"}, {post, "/api/hoxi/worktree/prune-sandboxes"},
		{post, "/api/hoxi/worktree/setup-retry"}, {post, "/api/hoxi/worktree/create"}, {post, "/api/hoxi/worktree/sync"},
		{post, "/api/hoxi/worktree/revert-file"}, {post, "/api/hoxi/worktree/rename-branch"},
		{http.MethodDelete, "/api/hoxi/worktree/ses_1"}, {get, "/api/hoxi/worktree/unknown"},
		// Runner hooks and legacy runner planes.
		{post, "/api/hoxi/runner/antigravity/hook"}, {get, "/api/hoxi/runner/antigravity/hook"},
		{post, "/api/hoxi/runner/hook/pretool"}, {post, "/api/hoxi/runner/hook/claude-pretool"},
		{post, "/api/hoxi/runner/hook/gemini-pretool"},
		{get, "/api/hoxi/runner/sessions"}, {post, "/api/hoxi/runner/sessions/ses_1/prompt"},
		{get, "/api/hoxi/runner/permission"}, {post, "/api/hoxi/runner/permission/per_1/reply"}, {get, "/api/hoxi/runner/event"},
		{get, "/api/hoxi/runner/unknown/models"},
		// Workspaces are read-only remotely.
		{post, "/api/hoxi/workspaces"}, {http.MethodPut, "/api/hoxi/workspaces/ws_1"},
		{http.MethodDelete, "/api/hoxi/workspaces"}, {get, "/api/hoxi/workspaces/ws_1"}, {get, "/api/hoxi/projects/resolve"},
		// Unknown sub-routes under allowed domains default to deny.
		{post, "/api/hoxi/sessions/ses_1/runtime"}, {post, "/api/hoxi/sessions/ses_1/summarize"},
		{get, "/api/hoxi/sessions/ses_1/event"}, {get, "/api/hoxi/sessions/ses_1/message/extra"},
		{get, "/api/hoxi/sessions/ses_1/unknown"}, {get, "/api/hoxi/skills/detail"}, {get, "/api/hoxi/attachments/a/b/c"},
		// Installing a skill writes ~/.hoxi and changes agent capability.
		{post, "/api/hoxi/skills/marketplace/install"}, {post, "/api/hoxi/skills/marketplace"},
		{get, "/api/hoxi/permission/per_1"}, {get, "/api/hoxi/permission/audit"}, {http.MethodDelete, "/api/hoxi/permission/audit"},
		{get, "/api/hoxi/permission/grants"}, {http.MethodDelete, "/api/hoxi/permission/grants"},
		{get, "/api/hoxi/permission/policy"}, {http.MethodPut, "/api/hoxi/permission/policy"},
		{get, "/api/hoxi/permission/deferred"}, {post, "/api/hoxi/permission/deferred"},
		// Commit attribution is a desktop preference, never remotely toggled.
		{get, "/api/hoxi/git/attribution"}, {http.MethodPut, "/api/hoxi/git/attribution"},
		{get, "/api/hoxi/sessions//message"}, {get, "/api/hoxi/sessions/"},
		// Methods are part of the allow-list.
		{http.MethodPatch, "/api/hoxi/sessions/ses_1"}, {http.MethodDelete, "/api/hoxi/sessions/ses_1"},
		{post, "/api/hoxi/sessions/ses_1/message"}, {get, "/api/hoxi/permission/per_1/reply"},
		{post, "/api/hoxi/event"}, {post, "/file/content"}, {post, "/api/hoxi/file/list"},
		{http.MethodOptions, "/api/hoxi/sessions"}, {http.MethodHead, "/healthz"}, {get, "/api/hoxi/worktree/commit"},
	}
	for _, r := range denied {
		if tunnelRouteAllowed(r.method, r.path) {
			t.Errorf("%s %s must stay desktop-only", r.method, r.path)
		}
	}
}

func TestTunnelRejectsEncodedPathsAndBareFileContent(t *testing.T) {
	g := New(0, "test", t.TempDir(), nil, nil)
	g.SetGatewayToken("loopback-secret")
	for _, target := range []string{
		"/api/hoxi/sessions/ses_1%2F..%2F..%2Ftunnel%2Fpair",
		"/api/hoxi/permission/per%2F1/reply",
		"/file/content",
		"/api/file/content?directory=/tmp",
		"/api/hoxi/file/content?path=",
		"/file?path=.",
	} {
		rec := httptest.NewRecorder()
		g.serveTunnelRequest(rec, localRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s over tunnel = %d, want 403", target, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodPost, "/file/content?path=a.go", strings.NewReader("{}")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /file/content over tunnel = %d, want 403", rec.Code)
	}
}

func TestTunnelCommitRejectsPush(t *testing.T) {
	for body, want := range map[string]bool{
		`{"sessionID":"ses_1","message":"m","push":false}`: true,
		`{"sessionID":"ses_1","message":"m"}`:              true,
		`{"sessionID":"ses_1","message":"m","push":true}`:  false,
		`{"push":tru`: false,
	} {
		r := localRequest(http.MethodPost, "/api/hoxi/worktree/commit", strings.NewReader(body))
		if got := tunnelBodyAllowed(r); got != want {
			t.Errorf("commit body %s allowed = %v, want %v", body, got, want)
		}
		if want {
			restored, _ := io.ReadAll(r.Body)
			if string(restored) != body {
				t.Errorf("commit body not restored: %q", restored)
			}
		}
	}
	g := New(0, "test", t.TempDir(), nil, nil)
	rec := httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodPost, "/api/hoxi/worktree/commit", strings.NewReader(`{"push":true}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("commit+push over tunnel = %d, want 403", rec.Code)
	}
}

func TestTunnelPermissionReplyRejectsAlways(t *testing.T) {
	for body, want := range map[string]bool{
		`{"reply":"once"}`:                      true,
		`{"reply":"reject","message":"reason"}`: true,
		`{"reply":"always"}`:                    false,
		`{"reply":"once","grantClass":true}`:    false,
		`{}`:                                    false,
		`{"reply":"once`:                        false,
	} {
		r := localRequest(http.MethodPost, "/api/hoxi/permission/per_rnr_1/reply", strings.NewReader(body))
		if got := tunnelBodyAllowed(r); got != want {
			t.Errorf("permission reply body %s allowed = %v, want %v", body, got, want)
		}
		if want {
			restored, _ := io.ReadAll(r.Body)
			if string(restored) != body {
				t.Errorf("permission reply body not restored: %q", restored)
			}
		}
	}
	g := New(0, "test", t.TempDir(), nil, nil)
	rec := httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodPost, "/api/hoxi/permission/per_rnr_1/reply", strings.NewReader(`{"reply":"always"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("permission reply=always over tunnel = %d, want 403", rec.Code)
	}
}

func TestTunnelRequestsAreFilteredAndNeverReceiveGatewayToken(t *testing.T) {
	g := New(0, "test", t.TempDir(), nil, nil)
	g.SetGatewayToken("loopback-secret")

	rec := httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodGet, "/api/hoxi/tunnel/status", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tunnel management over tunnel = %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodGet, "/api/hoxi/bootstrap", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("bootstrap over tunnel = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["gatewayToken"]; ok || rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("bootstrap must not expose the loopback gateway token over the tunnel")
	}

	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/bootstrap", nil))
	if !json.Valid(rec.Body.Bytes()) || rec.Header().Get("Set-Cookie") == "" {
		t.Fatal("local bootstrap should still issue the gateway token")
	}
}

func TestTunnelModelListIsSanitized(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".hoxi"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"provider":{"custom":{"name":"Custom","options":{"apiKey":"sk-tunnel-secret","baseURL":"https://secret.example/v1"},"models":{"m1":{"name":"Model One"}}}}}`
	if err := os.WriteFile(filepath.Join(root, ".hoxi", "models.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	g := New(0, "test", root, nil, nil)

	rec := httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodGet, "/api/hoxi/models-config", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("models-config over tunnel = %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	g.serveTunnelRequest(rec, localRequest(http.MethodGet, "/api/hoxi/provider/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("provider/models over tunnel = %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range []string{"sk-tunnel-secret", "secret.example", "apiKey", "baseURL", "options"} {
		if strings.Contains(body, secret) {
			t.Fatalf("tunnel model list leaked %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, `"id":"m1"`) || !strings.Contains(body, `"name":"Model One"`) {
		t.Fatalf("tunnel model list missing configured model: %s", body)
	}
}

func TestTunnelRoutesMatchContractDoc(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "gopanel-hoxi-remote-tunnel.md"))
	if err != nil {
		t.Fatalf("read tunnel contract doc: %v", err)
	}
	doc := string(raw)
	for _, r := range tunnelRoutes {
		exact := r.method + " " + r.pattern
		if strings.Contains(doc, "`"+exact+"`") {
			continue
		}
		// Check brace-expanded shorthand in the doc, e.g. `POST /api/hoxi/sessions/*/{prompt,...,stop-tool,...}`.
		matched := false
		for _, seg := range strings.Split(doc, "`") {
			if !strings.HasPrefix(seg, r.method+" /") {
				continue
			}
			open, close := strings.IndexByte(seg, '{'), strings.IndexByte(seg, '}')
			if open < 0 || close < open {
				continue
			}
			for _, item := range strings.Split(seg[open+1:close], ",") {
				if seg[:open]+strings.TrimSpace(item)+seg[close+1:] == exact {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			t.Errorf("tunnel route %q in tunnel.go is not documented in docs/gopanel-hoxi-remote-tunnel.md", exact)
		}
	}
}

// A JSON body labelled multipart/form-data must not skip the remote directory
// guard; only the attachment upload route actually parses multipart.
func TestTunnelMultipartLabelCannotSkipDirectoryGuard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	root, outside := t.TempDir(), t.TempDir()
	g := New(0, "test", root, nil, nil)
	defer g.CloseBrowser()
	raw, _ := json.Marshal(map[string]interface{}{"runtime": "claude-code", "directory": outside, "sandboxMode": "local"})
	for _, contentType := range []string{"application/json", "multipart/form-data; boundary=audit"} {
		t.Run(contentType, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/hoxi/sessions", bytes.NewReader(raw))
			r.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			g.serveTunnelRequest(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("outside directory accepted with content type %q: HTTP %d %s", contentType, w.Code, w.Body.String())
			}
		})
	}
}
