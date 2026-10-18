package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpdaterCheckerWithMockServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/hoxi/upgrade" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":         0,
			"version":      "0.3.0",
			"versionCode":  300000,
			"needUpdate":   true,
			"title":        "Hoxi v0.3.0 Released",
			"releaseNotes": "- Feature: One-click auth\n- Feature: Auto update",
			"downloadUrl":  "https://gitcode.com/hoxiai/hoxi/releases/download/v0.3.0/Hoxi.dmg",
			"mirrors": map[string]string{
				"cn":     "https://gitcode.com/hoxiai/hoxi/releases/download/v0.3.0/Hoxi.dmg",
				"global": "https://github.com/hoxiai/hoxi/releases/download/v0.3.0/Hoxi.dmg",
			},
			"mandatory": false,
			"sha256":    "abcdef1234567890",
		})
	}))
	defer ts.Close()

	checker := NewChecker("0.1.0", nil)
	checker.serverBaseURL = ts.URL

	res, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected check error: %v", err)
	}

	if !res.NeedUpdate {
		t.Errorf("expected needUpdate=true, got false")
	}
	if res.LatestVersion != "0.3.0" {
		t.Errorf("expected version 0.3.0, got %s", res.LatestVersion)
	}
	if res.Mirrors.CN == "" || res.Mirrors.Global == "" {
		t.Errorf("expected both mirrors populated, got %+v", res.Mirrors)
	}
	if res.SHA256 != "abcdef1234567890" {
		t.Errorf("expected sha256 abcdef1234567890, got %s", res.SHA256)
	}
}
