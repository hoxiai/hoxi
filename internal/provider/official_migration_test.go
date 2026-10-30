package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeLegacyOfficialConfig(t *testing.T, providerID, name string) {
	t.Helper()
	path := userHoxiModelsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := map[string]interface{}{
		"provider": map[string]interface{}{
			providerID: map[string]interface{}{
				"name": name,
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]interface{}{
					"apiKey":  "sk-legacy",
					"baseURL": "https://api.hoxi.ai/v1",
				},
				"models": map[string]interface{}{
					"glm-5.3-flash": map[string]interface{}{"name": "GLM 5.3 Flash"},
				},
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSetAuthSourceMigratesLegacyOfficialProvider(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	writeLegacyOfficialConfig(t, "ainode", officialProviderName)

	svc := NewService(0, "", workspace, nil)
	svc.SetAuthSource(&fakeAuthSource{site: "https://hoxi.ai", token: "device-token"})

	providers, _ := svc.LoadHoxiConfigDoc()["provider"].(map[string]interface{})
	if _, stale := providers["ainode"]; stale {
		t.Fatal("legacy ainode provider must be renamed, not duplicated")
	}
	official, ok := providers[officialProviderID].(map[string]interface{})
	if !ok {
		t.Fatalf("official provider missing after migration: %+v", providers)
	}
	if baseline := providerAPIKey(official); baseline != "sk-legacy" {
		t.Fatalf("migrated apiKey = %q, want sk-legacy", baseline)
	}
	if key := svc.ResolveStoredProviderKey(officialProviderID, ""); key != "sk-legacy" {
		t.Fatalf("migrated provider not resolvable: %q", key)
	}
}

func TestSetAuthSourceKeepsLegacyOfficialProviderWhenSignedOut(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	writeLegacyOfficialConfig(t, "ainode", officialProviderName)

	svc := NewService(0, "", workspace, nil)
	svc.SetAuthSource(&fakeAuthSource{site: "https://hoxi.ai"})

	providers, _ := svc.LoadHoxiConfigDoc()["provider"].(map[string]interface{})
	if _, ok := providers[officialProviderID]; ok {
		t.Fatal("signed-out account must not gain the official provider")
	}
	if _, ok := providers["ainode"]; !ok {
		t.Fatal("signed-out migration must not touch legacy config")
	}
}

func TestSetAuthSourceLeavesThirdPartyAinodeGateway(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	writeLegacyOfficialConfig(t, "ainode", "My AINode Gateway")

	svc := NewService(0, "", workspace, nil)
	svc.SetAuthSource(&fakeAuthSource{site: "https://hoxi.ai", token: "device-token"})

	providers, _ := svc.LoadHoxiConfigDoc()["provider"].(map[string]interface{})
	if _, ok := providers[officialProviderID]; ok {
		t.Fatal("user-defined ainode gateway must not be migrated")
	}
	if _, ok := providers["ainode"]; !ok {
		t.Fatal("user-defined ainode gateway must stay in place")
	}
}
