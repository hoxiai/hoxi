package provider

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectConfigNeverPollutesGlobalOrOtherWorkspace(t *testing.T) {
	_, root := setupIsolatedEnv(t)
	other := t.TempDir()
	svc := NewService(0, "", root, nil)
	if err := os.WriteFile(filepath.Join(root, "hoxi.json"), []byte(`{"mcp":{"root":{"type":"local","command":["root"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "hoxi.json"), []byte(`{"mcp":{"other":{"type":"local","command":["other"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := svc.LoadProjectConfigDoc(other)
	if err != nil {
		t.Fatal(err)
	}
	servers := doc["mcp"].(map[string]any)
	if servers["other"] == nil || servers["root"] != nil {
		t.Fatal("cross-workspace project leak", servers)
	}
	if svc.LoadHoxiConfigDoc()["mcp"] != nil {
		t.Fatal("shared project leaked into persistent settings")
	}
	if err := svc.UpdateHoxiConfigDoc(func(doc map[string]interface{}) error { doc["small_model"] = "test/small"; return nil }); err != nil {
		t.Fatal(err)
	}
	if readJSONMapFile(userHoxiModelsPath())["mcp"] != nil {
		t.Fatal("shared project persisted globally")
	}
}

func TestNativeDirectoryConfigReadIsLiveAndReadOnly(t *testing.T) {
	_, root := setupIsolatedEnv(t)
	svc := NewService(0, "", root, nil)
	path := filepath.Join(root, "hoxi.json")
	for _, raw := range []string{`{"permission":{"bash":"ask"}}`, `{"permission":{"bash":"deny"}}`, `{"unknown":true}`} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRecorder()
		svc.handleGlobalConfigProxy(r, httptest.NewRequest(http.MethodGet, "/api/hoxi/provider/config?directory="+url.QueryEscape(root), nil))
		want := http.StatusOK
		if raw == `{"unknown":true}` {
			want = http.StatusBadRequest
		}
		if r.Code != want {
			t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
		}
		if svc.LoadHoxiConfigDoc()["permission"] != nil {
			t.Fatal("GET polluted local state")
		}
	}
}
