package provider

import (
	"bytes"
	"encoding/json"
	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func TestOfficialLateConnectCannotRestoreLoggedOutCredentials(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	started, resume := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/hoxi/member/models" {
			_, _ = w.Write([]byte(`{"gatewayBaseUrl":"https://api.ainode.run/v1","models":[{"modelName":"m1"}]}`))
			return
		}
		close(started)
		<-resume
		_, _ = w.Write([]byte(`{"rawKey":"sk-old-account"}`))
	}))
	defer server.Close()
	svc := NewService(0, "", t.TempDir(), http.NotFoundHandler())
	auth := &fakeAuthSource{site: server.URL, token: "old-token", onClear: svc.ClearOfficialProvider}
	svc.SetAuthSource(auth)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.handleOfficialConnect(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	}()
	<-started
	auth.InvalidateDeviceToken("old-token", server.URL)
	close(resume)
	<-done
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("late connect must be rejected: %d %s", rec.Code, rec.Body.String())
	}
	providers := svc.LoadHoxiConfigDoc()["provider"].(map[string]interface{})
	if _, ok := providers[officialAinodeProviderID]; ok {
		t.Fatal("late connect restored logged-out credentials")
	}
	if key := svc.ResolveStoredProviderKey(officialAinodeProviderID, ""); key != "" {
		t.Fatal("late connect left a usable key")
	}
}

func TestOfficialStale401DoesNotClearNewAccount(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	started, resume := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-resume
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	svc := NewService(0, "", t.TempDir(), http.NotFoundHandler())
	auth := &fakeAuthSource{site: server.URL, token: "old", onClear: svc.ClearOfficialProvider}
	svc.SetAuthSource(auth)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.handleOfficialModels(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	<-started
	auth.mu.Lock()
	auth.token = "new"
	auth.mu.Unlock()
	if err := svc.persistOfficialProvider("https://api.ainode.run/v1", "sk-new", []officialModelView{{ID: "new-model"}}); err != nil {
		t.Fatal(err)
	}
	close(resume)
	<-done
	if key := svc.ResolveStoredProviderKey(officialAinodeProviderID, ""); key != "sk-new" {
		t.Fatal("old account 401 removed the new account key")
	}
}

func TestOfficialCatalogFailuresPreserveExistingModels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"unavailable", 503, `{"error":"temporary outage"}`},
		{"malformed", 200, `broken json`},
		{"missing models", 200, `{"code":500,"message":"error"}`},
		{"empty models", 200, `{"models":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOXI_HOME", t.TempDir())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/hoxi/member/models" {
					t.Error("failed catalog must not request or mint keys")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			svc := NewService(0, "", t.TempDir(), http.NotFoundHandler())
			svc.SetAuthSource(&fakeAuthSource{site: server.URL, token: "token"})
			if err := svc.persistOfficialProvider("https://api.ainode.run/v1", "sk-existing", []officialModelView{{ID: "existing-model"}}); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			svc.handleOfficialConnect(rec, httptest.NewRequest(http.MethodPost, "/", nil))
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("expected 502, got %d %s", rec.Code, rec.Body.String())
			}
			doc := svc.LoadHoxiProvidersMap()[officialAinodeProviderID].(map[string]interface{})
			if _, ok := doc["models"].(map[string]interface{})["existing-model"]; !ok {
				t.Fatal("catalog failure erased existing model")
			}
			if key := svc.ResolveStoredProviderKey(officialAinodeProviderID, ""); key != "sk-existing" {
				t.Fatal("catalog failure changed existing key")
			}
		})
	}
}

func TestOfficialCleanupFailureBlocksResidualKeyAndCanRetry(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires POSIX owner permission checks")
	}
	home := t.TempDir()
	t.Setenv("HOXI_HOME", home)
	svc := NewService(0, "", t.TempDir(), http.NotFoundHandler())
	if err := svc.persistOfficialProvider("https://api.ainode.run/v1", "sk-old", []officialModelView{{ID: "m1"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(home, 0700)
	if err := svc.ClearOfficialProvider(); err == nil {
		t.Fatal("cleanup failure must be reported")
	}
	raw, err := os.ReadFile(userHoxiModelsPath())
	if err != nil || !bytes.Contains(raw, []byte("sk-old")) {
		t.Fatal("test did not retain the blocked on-disk key")
	}
	if key := svc.ResolveStoredProviderKey(officialAinodeProviderID, ""); key != "" {
		t.Fatal("residual key must be blocked")
	}
	if _, key, _, _ := svc.resolveProviderConnection(officialAinodeProviderID, "", ""); key != "" {
		t.Fatal("connection resolver bypassed the block")
	}
	if _, ok := svc.LoadHoxiProvidersMap()[officialAinodeProviderID]; ok {
		t.Fatal("blocked provider remains visible")
	}
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := svc.ClearOfficialProvider(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	doc := svc.LoadHoxiConfigDoc()["provider"].(map[string]interface{})
	if _, ok := doc[officialAinodeProviderID]; ok {
		t.Fatal("retry left provider on disk")
	}
}

func TestOfficialCleanupPreservesCorruptEngineCredentials(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	svc := NewService(0, "", t.TempDir(), http.NotFoundHandler())
	if err := svc.persistOfficialProvider("https://api.ainode.run/v1", "sk-old", []officialModelView{{ID: "m1"}}); err != nil {
		t.Fatal(err)
	}
	path := hoxipaths.EngineAuthPath()
	if err := os.WriteFile(path, []byte(`{"broken":`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.ClearOfficialProvider(); err == nil {
		t.Fatal("corrupt auth must produce a cleanup error")
	}
	data, _ := os.ReadFile(path)
	if string(data) != `{"broken":` {
		t.Fatal("corrupt auth was overwritten")
	}
}

func TestOfficialBlockedProviderHiddenFromEngineCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"all":[{"id":"hoxi-official","models":{"m":{}}},{"id":"custom","models":{}}],"connected":["hoxi-official","custom"]}`))
	}))
	defer server.Close()
	t.Setenv("HOXI_HOME", t.TempDir())
	svc := NewService(0, "", t.TempDir(), http.NotFoundHandler())
	svc.openCode = ocapi.New(server.URL, "")
	svc.officialBlocked.Store(true)
	rec := httptest.NewRecorder()
	svc.handleProviderProxy(rec, httptest.NewRequest(http.MethodGet, "/provider", nil))
	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), "hoxi-official") {
		t.Fatalf("blocked provider exposed: %s", rec.Body.String())
	}
}
