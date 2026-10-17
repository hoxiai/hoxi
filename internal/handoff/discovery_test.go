package handoff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoveryPreservesStatsOrderAndFreshness(t *testing.T) {
	dir := t.TempDir()
	setupSyntheticCLIHome(t, dir)
	g := NewService(0, "test", dir, nil)
	request := func(path string, result any) {
		t.Helper()
		w := httptest.NewRecorder()
		gMux := http.NewServeMux()
		g.RegisterRoutes(gMux)
		gMux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("request failed: %d %s", w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), result); err != nil {
			t.Fatal(err)
		}
	}
	var all, filtered HandoffSessionsResponse
	request("/api/hoxi/handoff/sessions", &all)
	request("/api/hoxi/handoff/sessions?source=claude-code", &filtered)
	if len(all.Sources) != 4 || !reflect.DeepEqual(all.Sources, filtered.Sources) {
		t.Fatal("source filter changed full source stats")
	}
	if len(filtered.Sessions) != 1 || filtered.Sessions[0].Source != ExternalToolClaude {
		t.Fatalf("unexpected filtered sessions: %+v", filtered.Sessions)
	}
	var status LocalCLIStatusResponse
	request("/api/hoxi/local-cli/status", &status)
	if len(status.Tools) != 4 {
		t.Fatalf("unexpected tools: %+v", status.Tools)
	}
	for i, id := range []ExternalToolID{ExternalToolGrok, ExternalToolClaude, ExternalToolCodex, ExternalToolGemini} {
		if status.Tools[i].ID != id || status.Tools[i].SessionCount != all.Sources[id].SessionCount {
			t.Fatalf("tool order or stats changed: %+v", status.Tools[i])
		}
	}
	// External changes must be visible on the very next request.
	projects := filepath.Join(claudeHomeDir(), "projects")
	entries, err := os.ReadDir(projects)
	if err != nil || len(entries) == 0 {
		t.Fatalf("missing synthetic projects: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projects, entries[0].Name(), "new-session.jsonl"), []byte("{\"type\":\"user\",\"message\":{\"content\":\"new external session\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var fresh HandoffSessionsResponse
	request("/api/hoxi/handoff/sessions?source=codex-cli", &fresh)
	if fresh.Sources[ExternalToolClaude].SessionCount != all.Sources[ExternalToolClaude].SessionCount+1 {
		t.Fatal("filtered request did not freshly scan every source")
	}
	request("/api/hoxi/local-cli/status", &status)
	if status.Tools[1].SessionCount != fresh.Sources[ExternalToolClaude].SessionCount {
		t.Fatal("CLI status did not freshly scan external sessions")
	}
}
