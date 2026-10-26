package workapps

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type stubAuth struct {
	baseURL string
	token   string
}

func (s *stubAuth) ServerBaseURL() string      { return s.baseURL }
func (s *stubAuth) CurrentDeviceToken() string { return s.token }

func TestFetchAndConcurrentSavePreserveUpdates(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/hoxi/work/apps" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		close(entered)
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": 1,
			"apps": []map[string]any{
				{"id": "shopify", "name": "Shopify"},
			},
		})
	}))
	defer srv.Close()

	store := NewStore(filepath.Join(t.TempDir(), "work-apps.json"))
	svc := NewService(&stubAuth{baseURL: srv.URL, token: "tok"}, store)
	svc.httpClient = srv.Client()

	done := make(chan map[string]any, 1)
	go func() {
		res, err := svc.FetchApps(context.Background())
		if err != nil {
			t.Errorf("FetchApps failed: %v", err)
		}
		done <- res
	}()

	<-entered
	// Concurrent user save while FetchApps is in-flight over HTTP.
	if err := svc.SaveState(map[string]any{
		"installedIds": []string{"shopify", "custom-1"},
	}); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}
	close(release)

	res := <-done
	localState, _ := res["localState"].(map[string]any)
	if localState == nil {
		t.Fatalf("missing localState in response: %#v", res)
	}
	installed, _ := localState["installedIds"].([]any)
	if len(installed) != 2 {
		t.Fatalf("concurrent SaveState was clobbered by FetchApps: %#v", localState)
	}
	if localState["cachedItems"] == nil {
		t.Fatalf("expected cachedItems to be persisted alongside installedIds: %#v", localState)
	}

	h := NewHTTPHandler(svc)
	putReq := httptest.NewRequest(http.MethodPut, "/api/hoxi/work/apps", strings.NewReader(`{"activeAppId":"shopify"}`))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	h.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on PUT /api/hoxi/work/apps, got %d (%s)", putRec.Code, putRec.Body.String())
	}
}
