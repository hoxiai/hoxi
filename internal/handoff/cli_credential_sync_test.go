package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/provider"
	"github.com/hoxiai/hoxi/internal/testutil/execstub"
)

func TestMain(m *testing.M) {
	execstub.Main()
	os.Exit(m.Run())
}

func TestReadGrokAPIKeySkipsExpiredEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	doc := map[string]interface{}{
		"expired": map[string]string{
			"key":        "key-expired",
			"expires_at": now.Add(-time.Hour).Format(time.RFC3339),
		},
		"fresh": map[string]string{
			"key":        "key-fresh",
			"expires_at": now.Add(time.Hour).Format(time.RFC3339),
		},
	}
	raw, _ := json.Marshal(doc)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readGrokAPIKey(path, now); got != "key-fresh" {
		t.Fatalf("expected the unexpired key, got %q", got)
	}

	// Once nothing is still valid, fall back to the newest key so a request
	// fails loudly instead of the provider vanishing.
	stale := map[string]interface{}{
		"older": map[string]string{"key": "key-older", "expires_at": now.Add(-2 * time.Hour).Format(time.RFC3339)},
		"newer": map[string]string{"key": "key-newer", "expires_at": now.Add(-time.Minute).Format(time.RFC3339)},
	}
	raw, _ = json.Marshal(stale)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readGrokAPIKey(path, now); got != "key-newer" {
		t.Fatalf("expected the newest expired key as fallback, got %q", got)
	}

	// A key the CLI stores without an expiry is always usable, and multiple
	// static keys are selected deterministically by sorted map key.
	if err := os.WriteFile(path, []byte(`{"zeta":{"key":"key-z"},"alpha":{"key":"key-a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if got := readGrokAPIKey(path, now); got != "key-a" {
			t.Fatalf("expected deterministic static key 'key-a', got %q", got)
		}
	}
}

// recordingAuthServer counts how often the engine receives an openai credential.
type recordingAuthServer struct {
	mu       sync.Mutex
	puts     []map[string]interface{}
	failNext bool
	close    func()
	port     int
}

func newRecordingAuthServer(t *testing.T) *recordingAuthServer {
	t.Helper()
	rec := &recordingAuthServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/openai", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		if rec.failNext {
			rec.failNext = false
			rec.mu.Unlock()
			writeJSONError(w, http.StatusServiceUnavailable, "engine starting")
			return
		}
		rec.puts = append(rec.puts, body)
		rec.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	rec.close = srv.Close
	rec.port = srv.Listener.Addr().(*net.TCPAddr).Port
	return rec
}

func (r *recordingAuthServer) putCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.puts)
}

func (r *recordingAuthServer) lastAccess() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.puts) == 0 {
		return ""
	}
	return asString(r.puts[len(r.puts)-1]["access"])
}

// writeCodexAuth writes a ChatGPT OAuth credential the way the Codex CLI does.
func writeCodexAuth(t *testing.T, dir, access string) {
	t.Helper()
	doc := map[string]interface{}{
		"auth_mode": "chatgpt",
		"tokens": map[string]string{
			"access_token":  access,
			"refresh_token": "refresh-" + access,
			"account_id":    "acct-1",
		},
	}
	raw, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSyncCodexCredentialFollowsCLIAndNeverRefreshes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCodexAuth(t, codexDir, "access-old")

	rec := newRecordingAuthServer(t)
	root := t.TempDir()
	svc := NewService(rec.port, "testpass", root, provider.NewService(0, "", root, nil))
	t.Cleanup(svc.StopCredentialSync)

	// Not imported yet: the sync must not touch an unrelated openai provider.
	svc.syncCodexCredential()
	if got := rec.putCount(); got != 0 {
		t.Fatalf("sync before import pushed %d credentials", got)
	}

	// An unrelated user-configured "openai" provider must also be left alone.
	if err := svc.persistHoxiConfigDoc(map[string]interface{}{
		"provider": map[string]interface{}{"openai": map[string]interface{}{"name": "OpenAI"}},
	}); err != nil {
		t.Fatal(err)
	}
	svc.syncCodexCredential()
	if got := rec.putCount(); got != 0 {
		t.Fatalf("sync with user OpenAI provider pushed %d credentials", got)
	}

	if err := svc.persistHoxiConfigDoc(map[string]interface{}{
		"provider": map[string]interface{}{"openai": map[string]interface{}{"name": "OpenAI Codex (Local)"}},
	}); err != nil {
		t.Fatal(err)
	}

	// If the engine is temporarily unavailable, codexAuthApplied is not marked
	// applied so the next tick retries.
	rec.mu.Lock()
	rec.failNext = true
	rec.mu.Unlock()
	svc.syncCodexCredential()
	if got := rec.putCount(); got != 0 {
		t.Fatalf("failed engine PUT should not record a put, got %d", got)
	}

	svc.syncCodexCredential()
	if rec.lastAccess() != "access-old" {
		t.Fatalf("expected the CLI access token to be forwarded on retry, got %q", rec.lastAccess())
	}

	// The refresh token is forwarded verbatim and the expiry is left unknown:
	// inventing one is what made the engine refresh and invalidate the CLI.
	rec.mu.Lock()
	pushed := rec.puts[0]
	rec.mu.Unlock()
	if asString(pushed["refresh"]) != "refresh-access-old" {
		t.Fatalf("refresh token must be forwarded verbatim, got %q", asString(pushed["refresh"]))
	}
	if pushed["expires"] != float64(0) {
		t.Fatalf("expiry must stay unknown so Hoxi never refreshes, got %#v", pushed["expires"])
	}

	// Unchanged file: nothing is pushed again.
	svc.syncCodexCredential()
	if got := rec.putCount(); got != 1 {
		t.Fatalf("unchanged credential was re-pushed, count %d", got)
	}

	// The CLI rotated the access token; Hoxi follows without writing back.
	before, err := os.ReadFile(filepath.Join(codexDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeCodexAuth(t, codexDir, "access-new")
	svc.syncCodexCredential()
	if rec.lastAccess() != "access-new" {
		t.Fatalf("expected the rotated access token, got %q", rec.lastAccess())
	}
	after, err := os.ReadFile(filepath.Join(codexDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) == string(after) {
		t.Fatal("test setup failed to rotate the CLI credential")
	}

	// The durable copy lives in the engine auth file, and only there.
	engineAuth, err := os.ReadFile(filepath.Join(hoxipaths.EngineOpenCodeDir(), "auth.json"))
	if err != nil {
		t.Fatalf("engine auth.json not written: %v", err)
	}
	var stored map[string]map[string]interface{}
	if err := json.Unmarshal(engineAuth, &stored); err != nil {
		t.Fatal(err)
	}
	if asString(stored["openai"]["access"]) != "access-new" {
		t.Fatalf("engine auth not updated, got %s", engineAuth)
	}
}

func TestCodexImportReportsUnknownExpiry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCodexAuth(t, codexDir, "access-1")

	rec := newRecordingAuthServer(t)
	root := t.TempDir()
	svc := NewService(rec.port, "testpass", root, provider.NewService(0, "", root, nil))
	resp := svc.importCodexCLIProvider()
	if !resp.OK {
		t.Fatalf("import failed: %+v", resp)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.puts) != 1 || rec.puts[0]["expires"] != float64(0) {
		t.Fatalf("import must not invent an expiry, got %#v", rec.puts)
	}
}

func TestStartAndStopCredentialSyncRaceFree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	rec := newRecordingAuthServer(t)
	root := t.TempDir()
	svc := NewService(rec.port, "testpass", root, provider.NewService(0, "", root, nil))
	for i := 0; i < 5; i++ {
		svc.startCredentialSync()
		svc.StopCredentialSync()
	}
}

func writeGrokAuthFile(t *testing.T, authPath, key, refreshToken string, expiresAt time.Time) {
	t.Helper()
	doc := map[string]interface{}{
		"grok.com": map[string]string{
			"key":           key,
			"refresh_token": refreshToken,
			"expires_at":    expiresAt.UTC().Format(time.RFC3339),
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGrokCLIRefreshesExpiredTokenAndSyncsProactively(t *testing.T) {
	home := t.TempDir()
	grokDir := filepath.Join(home, ".grok")
	binDir := filepath.Join(grokDir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GROK_HOME", grokDir)

	authPath := filepath.Join(grokDir, "auth.json")
	freshExpiry := time.Now().Add(6 * time.Hour).UTC().Format(time.RFC3339)

	stubScript := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "models" ]; then
  cat > %q <<'EOF'
{"grok.com":{"key":"key-refreshed-by-cli","refresh_token":"rt-fresh","expires_at":"%s"}}
EOF
  exit 0
fi
exit 1
`, authPath, freshExpiry)
	stubPath := execstub.Write(t, binDir, "grok", stubScript)
	t.Setenv("GROK_BIN", stubPath)

	// 1. Expired token is refreshed automatically via `grok models`.
	writeGrokAuthFile(t, authPath, "key-expired", "rt-old", time.Now().Add(-15*time.Minute))
	creds := loadGrokLocalCredentialsWithRefresh(context.Background())
	if creds.APIKey != "key-refreshed-by-cli" {
		t.Fatalf("expected expired token to be refreshed by grok CLI, got %q", creds.APIKey)
	}

	// 2. Background syncGrokCredential refreshes a token inside the 5-minute proactive window.
	grokRefreshMu.Lock()
	lastGrokRefreshAt = time.Time{}
	grokRefreshMu.Unlock()

	proactiveExpiry := time.Now().Add(6 * time.Hour).UTC().Format(time.RFC3339)
	proactiveScript := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "models" ]; then
  cat > %q <<'EOF'
{"grok.com":{"key":"key-proactive-sync","refresh_token":"rt-proactive","expires_at":"%s"}}
EOF
  exit 0
fi
exit 1
`, authPath, proactiveExpiry)
	if err := os.Remove(stubPath); err != nil {
		t.Fatal(err)
	}
	stubPath = execstub.Write(t, binDir, "grok", proactiveScript)
	t.Setenv("GROK_BIN", stubPath)

	writeGrokAuthFile(t, authPath, "key-expiring-soon", "rt-soon", time.Now().Add(2*time.Minute))
	root := t.TempDir()
	svc := NewService(0, "", root, provider.NewService(0, "", root, nil))
	if err := svc.persistHoxiConfigDoc(map[string]interface{}{
		"provider": map[string]interface{}{
			"grok-cli": map[string]interface{}{"name": "Grok CLI (Local)"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	svc.syncGrokCredential(context.Background())
	if got := readGrokAPIKey(authPath, time.Now()); got != "key-proactive-sync" {
		t.Fatalf("expected background syncGrokCredential to refresh expiring token, got %q", got)
	}
}

func TestGrokCLIBridgeRetriesOnceOn401WithForcedRefresh(t *testing.T) {
	home := t.TempDir()
	grokDir := filepath.Join(home, ".grok")
	binDir := filepath.Join(grokDir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GROK_HOME", grokDir)

	authPath := filepath.Join(grokDir, "auth.json")
	// Token looks unexpired locally (+2h), but upstream rejects it with 401.
	writeGrokAuthFile(t, authPath, "key-revoked-upstream", "rt-revoked", time.Now().Add(2*time.Hour))

	freshExpiry := time.Now().Add(6 * time.Hour).UTC().Format(time.RFC3339)
	stubScript := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "models" ] && [ -n "$GROK_AUTH_EARLY_INVALIDATION_SECS" ]; then
  cat > %q <<'EOF'
{"grok.com":{"key":"key-rotated-on-401","refresh_token":"rt-rotated","expires_at":"%s"}}
EOF
  exit 0
fi
exit 1
`, authPath, freshExpiry)
	stubPath := execstub.Write(t, binDir, "grok", stubScript)
	t.Setenv("GROK_BIN", stubPath)

	var seenAuthHeaders []string
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		seenAuthHeaders = append(seenAuthHeaders, auth)
		mu.Unlock()
		if auth != "Bearer key-rotated-on-401" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Unauthorized: Invalid or expired credentials"}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("HOXI_GROK_UPSTREAM_URL", upstream.URL)

	root := t.TempDir()
	svc := NewService(0, "", root, provider.NewService(0, "", root, nil))
	reqBody := []byte(`{"model":"grok-4.7","stream":false,"messages":[{"role":"user","content":"ping"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/bridge/grok-cli/v1/chat/completions", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()
	svc.handleGrokCLIBridge(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after 401 refresh retry, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "recovered") {
		t.Fatalf("expected recovered response content, got %s", rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seenAuthHeaders) != 2 || seenAuthHeaders[0] != "Bearer key-revoked-upstream" || seenAuthHeaders[1] != "Bearer key-rotated-on-401" {
		t.Fatalf("unexpected upstream auth sequence: %#v", seenAuthHeaders)
	}
}
