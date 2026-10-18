package provider

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func assertPrivateConfig(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
	}
}

func TestConfigCredentialMigrationAndUpstreamReload(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	userPath := userHoxiModelsPath()
	workspacePath := filepath.Join(workspace, ".hoxi", "models.json")
	// Real persisted compatibility: workspace values override user values, and
	// older versions duplicated literal keys and custom authentication headers.
	for _, path := range []string{userPath, workspacePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"provider":{"custom":{"options":{"apiKey":"old-key","baseURL":"https://example.test/v1","headers":{"Authorization":"Bearer header-key"}},"models":{"m1":{"name":"Model"}}}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(workspacePath, []byte(`{"provider":{"custom":{"options":{"apiKey":"workspace-key"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := EnsureHoxiModelsConfig(workspace); got != userPath {
		t.Fatalf("upstream config = %q, want %q", got, userPath)
	}
	svc := NewService(0, "password", workspace, nil)
	check := func(wantKey string) {
		t.Helper()
		for _, path := range []string{userPath, workspacePath} {
			assertPrivateConfig(t, path)
		}
		user := readJSONMapFile(userPath)
		mirror := readJSONMapFile(workspacePath)
		opts := user["provider"].(map[string]interface{})["custom"].(map[string]interface{})["options"].(map[string]interface{})
		if opts["apiKey"] != wantKey || opts["headers"] == nil {
			t.Fatalf("upstream credentials not preserved: %+v", opts)
		}
		mirrorOpts := mirror["provider"].(map[string]interface{})["custom"].(map[string]interface{})["options"].(map[string]interface{})
		if _, ok := mirrorOpts["apiKey"]; ok {
			t.Fatal("workspace contains apiKey")
		}
		if _, ok := mirrorOpts["headers"]; ok {
			t.Fatal("workspace contains headers")
		}
		if mirrorOpts["baseURL"] != "https://example.test/v1" {
			t.Fatal("workspace lost non-secret options")
		}
		if got := svc.ResolveStoredProviderKey("custom", ""); got != wantKey {
			t.Fatalf("resolved key = %q, want %q", got, wantKey)
		}
	}
	check("workspace-key")
	// Repeated initialization must not erase credentials omitted from the mirror.
	EnsureHoxiModelsConfig(workspace)
	check("workspace-key")
	doc := svc.LoadHoxiConfigDoc()
	doc["provider"].(map[string]interface{})["custom"].(map[string]interface{})["options"].(map[string]interface{})["apiKey"] = "updated-key"
	reloads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/global/dispose" || r.Method != http.MethodPost {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
		username, password, _ := r.BasicAuth()
		if username != "opencode" || password != "password" {
			t.Error("missing upstream authentication")
		}
		upstream := readJSONMapFile(userPath)
		if !reflect.DeepEqual(upstream, doc) {
			t.Error("upstream config not fully persisted before reload")
		}
		reloads++
	}))
	defer server.Close()
	svc.openCodePort = server.Listener.Addr().(*net.TCPAddr).Port
	if err := svc.PersistHoxiConfigDoc(doc); err != nil {
		t.Fatal(err)
	}
	svc.openCodePort = 0
	if reloads != 1 {
		t.Fatalf("reloads = %d, want 1", reloads)
	}
	check("updated-key")
}

func TestConfigSaveFailureDoesNotStripOnlyCredentialCopy(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	svc := NewService(0, "", workspace, nil)
	if err := writeJSONMapFile(svc.workspaceHoxiModelsPath(), map[string]interface{}{"provider": map[string]interface{}{"custom": map[string]interface{}{"options": map[string]interface{}{"apiKey": "only-copy"}}}}); err != nil {
		t.Fatal(err)
	}
	// A directory at the destination makes replacement fail even as root.
	if err := os.MkdirAll(userHoxiModelsPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	before := readJSONMapFile(svc.workspaceHoxiModelsPath())
	if err := svc.PersistHoxiConfigDoc(svc.LoadHoxiConfigDoc()); err == nil {
		t.Fatal("expected user config write failure")
	}
	EnsureHoxiModelsConfig(workspace)
	if after := readJSONMapFile(svc.workspaceHoxiModelsPath()); !reflect.DeepEqual(before, after) {
		t.Fatal("failed user save changed workspace credential copy")
	}
}

func TestConfigSamePathAndNoHomeFallback(t *testing.T) {
	home, _ := setupIsolatedEnv(t)
	svc := NewService(0, "", home, nil)
	doc := map[string]interface{}{"provider": map[string]interface{}{"custom": map[string]interface{}{"options": map[string]interface{}{"apiKey": "retained-key"}}}}
	if err := svc.PersistHoxiConfigDoc(doc); err != nil {
		t.Fatal(err)
	}
	if got := svc.ResolveStoredProviderKey("custom", ""); got != "retained-key" {
		t.Fatalf("same-path save lost key: %q", got)
	}
	path := filepath.Join(t.TempDir(), "models.json")
	if err := persistHoxiModelsFiles("", path, doc); err != nil {
		t.Fatal(err)
	}
	assertPrivateConfig(t, path)
	if !reflect.DeepEqual(readJSONMapFile(path), doc) {
		t.Fatal("no-home fallback lost upstream credentials")
	}
}
