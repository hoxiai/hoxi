package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandoffDiscoveryAndPreview(t *testing.T) {
	cwd, _ := os.Getwd()
	gw := newDesktopGateway(0, "testpass", cwd, nil, newDesktopStatusTracker())

	// 1. Test /api/hoxi/local-cli/status
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/hoxi/local-cli/status", nil)
	recStatus := httptest.NewRecorder()
	gw.ServeHTTP(recStatus, reqStatus)
	if recStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/hoxi/local-cli/status, got %d", recStatus.Code)
	}
	var statusResp LocalCLIStatusResponse
	if err := json.Unmarshal(recStatus.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("unmarshal status response: %v", err)
	}
	if len(statusResp.Tools) != 3 {
		t.Fatalf("expected 3 CLI tools in status response, got %d", len(statusResp.Tools))
	}
	for _, tool := range statusResp.Tools {
		t.Logf("CLI Tool: id=%s installed=%v auth=%v sessions=%d models=%v",
			tool.ID, tool.Installed, tool.Authenticated, tool.SessionCount, tool.Models)
	}

	// 2. Test /api/hoxi/handoff/sessions
	reqSess := httptest.NewRequest(http.MethodGet, "/api/hoxi/handoff/sessions?limit=30", nil)
	recSess := httptest.NewRecorder()
	gw.ServeHTTP(recSess, reqSess)
	if recSess.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/hoxi/handoff/sessions, got %d", recSess.Code)
	}
	var sessResp HandoffSessionsResponse
	if err := json.Unmarshal(recSess.Body.Bytes(), &sessResp); err != nil {
		t.Fatalf("unmarshal sessions response: %v", err)
	}
	t.Logf("Discovered %d external sessions (sources: %+v)", len(sessResp.Sessions), sessResp.Sources)

	// 3. Test /api/hoxi/handoff/preview on first session of each source if present
	testedSources := map[ExternalToolID]bool{}
	for _, s := range sessResp.Sessions {
		if testedSources[s.Source] {
			continue
		}
		testedSources[s.Source] = true
		detail, err := loadExternalSessionDetail(s.Source, s.ID, s.CWD, cwd)
		if err != nil {
			t.Errorf("loadExternalSessionDetail(%s, %s) failed: %v", s.Source, s.ID, err)
			continue
		}
		t.Logf("Preview [%s] id=%s title=%q turns=%d changedFiles=%d plans=%d",
			s.Source, s.ID, detail.Summary.Title, len(detail.Turns), len(detail.ChangedFiles), len(detail.PlanSteps))

		// Verify 8-Dim Handoff Memory generation
		tmpMem := filepath.Join(t.TempDir(), "ses_test.md")
		if err := writeEightDimHandoffMemory(tmpMem, "ses_test", cwd, detail); err != nil {
			t.Errorf("writeEightDimHandoffMemory failed: %v", err)
		}
	}

	// 4. Verify strictly ascending OpenCode IDs
	id1 := makeAscendingOpenCodeID("msg", 1700000000000, 1)
	id2 := makeAscendingOpenCodeID("msg", 1700000000000, 2)
	id3 := makeAscendingOpenCodeID("msg", 1700000001000, 3)
	if !(id1 < id2 && id2 < id3) {
		t.Fatalf("expected strictly ascending IDs: %s < %s < %s", id1, id2, id3)
	}
}
