package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemorySaveAndPromoteResolvesIsolatedWorktreeToProjectRoot(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	worktreeDir := filepath.Join(projectRoot, ".hoxi", "worktrees", "ses_mem_01")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gw := &Gateway{rootDir: projectRoot}

	// 1. Save session memory while passing the isolated worktree directory
	sessContent := "# Hoxi Session Memory (8-Dimension Compaction)\nSession: `ses_mem_01`\n\n## 1. Primary Request and Intent\n- Initial Request: Add memory editor\n- User Constraint: Never create .opencode in user projects\n\n## 5. Problem Solving\n- Latest Summary: Implemented atomic memory save and promote\n"
	putBody, _ := json.Marshal(map[string]any{
		"scope":     "session",
		"sessionID": "ses_mem_01",
		"directory": worktreeDir,
		"content":   sessContent,
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/hoxi/memory", bytes.NewReader(putBody))
	putRec := httptest.NewRecorder()
	gw.handleMemory(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT /api/hoxi/memory status = %d, body = %s", putRec.Code, putRec.Body.String())
	}

	// Verify session memory was written at projectRoot/.hoxi/memory/sessions/ses_mem_01.md, not inside the worktree
	wantSessFile := filepath.Join(projectRoot, ".hoxi", "memory", "sessions", "ses_mem_01.md")
	if _, err := os.Stat(wantSessFile); err != nil {
		t.Fatalf("expected session memory at project root %s: %v", wantSessFile, err)
	}
	if _, err := os.Stat(filepath.Join(worktreeDir, ".hoxi", "memory")); !os.IsNotExist(err) {
		t.Fatalf("worktree must not hold its own .hoxi/memory directory")
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".opencode")); !os.IsNotExist(err) {
		t.Fatalf("must never create .opencode in user project")
	}

	// 2. Promote session memory constraints into project.md
	promBody, _ := json.Marshal(map[string]any{
		"sessionID": "ses_mem_01",
		"directory": worktreeDir,
	})
	promReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/memory/promote", bytes.NewReader(promBody))
	promRec := httptest.NewRecorder()
	gw.handleMemoryPromote(promRec, promReq)
	if promRec.Code != http.StatusOK {
		t.Fatalf("POST /api/hoxi/memory/promote status = %d, body = %s", promRec.Code, promRec.Body.String())
	}
	var promResp struct {
		OK         bool   `json:"ok"`
		Content    string `json:"content"`
		AddedCount int    `json:"addedCount"`
	}
	if err := json.Unmarshal(promRec.Body.Bytes(), &promResp); err != nil {
		t.Fatal(err)
	}
	if !promResp.OK || promResp.AddedCount == 0 || !strings.Contains(promResp.Content, "Never create .opencode in user projects") {
		t.Fatalf("unexpected promote response: %+v", promResp)
	}

	// 3. Remote requests must be rejected with 403
	remoteReq := httptest.NewRequest(http.MethodPut, "/api/hoxi/memory", bytes.NewReader(putBody))
	remoteRec := httptest.NewRecorder()
	gw.serveRemoteRequest(remoteRec, remoteReq, remoteSourceTunnel)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for remote memory write, got %d", remoteRec.Code)
	}
}
