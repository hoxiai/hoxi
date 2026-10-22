package handoff

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/provider"
)

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
