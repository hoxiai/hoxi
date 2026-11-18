package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/pairing"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestRemoteE2EECommitDisconnectInspectAndIdempotentReplaySingleGitCommit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	repoDir := t.TempDir()
	outsideDir := t.TempDir()
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}
	gitRevCount := func(dir string) int {
		t.Helper()
		cmd := exec.Command("git", "rev-list", "--count", "HEAD")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git rev-list --count HEAD: %v", err)
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil {
			t.Fatalf("parse rev-list count %q: %v", string(out), err)
		}
		return n
	}

	runGit(repoDir, "init")
	runGit(repoDir, "config", "user.email", "mobile@hoxi.local")
	runGit(repoDir, "config", "user.name", "Hoxi Mobile")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Hoxi Repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(repoDir, "add", "README.md")
	runGit(repoDir, "commit", "-m", "chore: initial commit")
	baseCommits := gitRevCount(repoDir)

	g := New(0, "test-pass", repoDir, nil, nil)
	defer g.CloseBrowser()

	relayToken, err := g.pairing.RelayToken()
	if err != nil {
		t.Fatalf("RelayToken: %v", err)
	}
	e2eeKey, err := g.pairing.E2EEKey()
	if err != nil {
		t.Fatalf("E2EEKey: %v", err)
	}
	clientCipher, err := pairing.NewE2EECipher(e2eeKey)
	if err != nil {
		t.Fatalf("NewE2EECipher: %v", err)
	}

	sendEncryptedRemote := func(e2eeReqID, method, target string, plainBody []byte) (int, http.Header, []byte) {
		t.Helper()
		reqPath, _, _ := strings.Cut(target, "?")
		now := time.Now().Unix()
		hasBody := len(plainBody) > 0
		hdr := clientCipher.BuildRequestHeader(method, reqPath, e2eeReqID, now, hasBody)
		var wireBody *bytes.Reader
		if hasBody {
			sealed, sealErr := clientCipher.SealRequest(
				pairing.E2EERequestMeta{ID: e2eeReqID, TS: fmt.Sprintf("%d", now), HasBody: true},
				method,
				reqPath,
				"application/json",
				plainBody,
			)
			if sealErr != nil {
				t.Fatalf("SealRequest(%s %s): %v", method, target, sealErr)
			}
			wireBody = bytes.NewReader(sealed)
		} else {
			wireBody = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, target, wireBody)
		req.Host = "abc123.node.hoxi.ai"
		req.RemoteAddr = "203.0.113.9:52000"
		req.Header.Set("X-Auth", relayToken)
		req.Header.Set(pairing.E2EEHeader, hdr)
		if hasBody {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		g.ServeIngressRequest(rec, req)
		if rec.Header().Get(pairing.E2EEHeader) != pairing.E2EEVersionHeader {
			t.Fatalf("%s %s missing E2EE response header: status=%d hdr=%q body=%s", method, target, rec.Code, rec.Header().Get(pairing.E2EEHeader), rec.Body.String())
		}
		plainResp, _, openErr := clientCipher.OpenResponse(e2eeReqID, method, reqPath, rec.Code, rec.Body.Bytes())
		if openErr != nil {
			t.Fatalf("OpenResponse(%s %s status=%d): %v", method, target, rec.Code, openErr)
		}
		return rec.Code, rec.Header(), plainResp
	}

	// 1. Mobile creates a local-sandbox session over the encrypted remote ingress.
	createPayload, _ := json.Marshal(map[string]any{
		"runtime":     "native",
		"title":       "Mobile Commit Session",
		"directory":   repoDir,
		"sandboxMode": "local",
	})
	cStatus, _, cPlain := sendEncryptedRemote("e2ee_mobile_create_1", http.MethodPost, "/api/hoxi/sessions", createPayload)
	if cStatus != http.StatusOK {
		t.Fatalf("encrypted create session = %d %s", cStatus, string(cPlain))
	}
	var session runner.SessionInfo
	if err := json.Unmarshal(cPlain, &session); err != nil {
		t.Fatal(err)
	}

	// Stage a real file change in the session repository.
	if err := os.WriteFile(filepath.Join(repoDir, "mobile_fix.go"), []byte("package main\n\nconst MobileFix = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Mobile sends POST /api/hoxi/worktree/commit with clientRequestId="mob-commit-001",
	// host commits successfully, and network drops BEFORE mobile receives the HTTP response.
	clientReqID := "mob-commit-001"
	commitMsg := "fix: mobile offline-resilient commit"
	commitPayload, _ := json.Marshal(map[string]any{
		"sessionID":       session.ID,
		"message":         commitMsg,
		"push":            false,
		"clientRequestId": clientReqID,
	})
	firstStatus, _, firstPlain := sendEncryptedRemote("e2ee_mobile_commit_attempt_1", http.MethodPost, "/api/hoxi/worktree/commit", commitPayload)
	if firstStatus != http.StatusOK {
		t.Fatalf("initial commit on host failed = %d %s", firstStatus, string(firstPlain))
	}
	var initialResult worktree.CommitAndPushResult
	if err := json.Unmarshal(firstPlain, &initialResult); err != nil {
		t.Fatal(err)
	}
	if initialResult.Commit == "" || initialResult.Deduplicated {
		t.Fatalf("unexpected initial commit result: %+v", initialResult)
	}
	if got := gitRevCount(repoDir); got != baseCommits+1 {
		t.Fatalf("git commit count after first request = %d, want %d", got, baseCommits+1)
	}

	// 3. Mobile reconnects and inspects GET /api/hoxi/requests/{id} over encrypted tunnel.
	inspectTarget := "/api/hoxi/requests/" + clientReqID + "?kind=worktree_commit&scope=" + session.ID
	inspStatus, _, inspPlain := sendEncryptedRemote("e2ee_mobile_inspect_1", http.MethodGet, inspectTarget, nil)
	if inspStatus != http.StatusOK {
		t.Fatalf("encrypted inspect = %d %s", inspStatus, string(inspPlain))
	}
	var inspResp runner.RequestInspectResponse
	if err := json.Unmarshal(inspPlain, &inspResp); err != nil {
		t.Fatal(err)
	}
	if inspResp.Status != runner.RequestStatusCompleted {
		t.Fatalf("inspect status = %q, want completed", inspResp.Status)
	}
	var inspectedCommit worktree.CommitAndPushResult
	if err := json.Unmarshal(inspResp.Result, &inspectedCommit); err != nil {
		t.Fatal(err)
	}
	if inspectedCommit.Commit != initialResult.Commit {
		t.Fatalf("inspected commit = %q, want %q", inspectedCommit.Commit, initialResult.Commit)
	}

	// 4. Even if mobile also retries POST /api/hoxi/worktree/commit with the same clientRequestId
	// (and even if another file was dirtied meanwhile), it receives the original result with deduplicated=true
	// and git history STILL has only ONE commit!
	if err := os.WriteFile(filepath.Join(repoDir, "extra_uncommitted.txt"), []byte("do not commit on replay\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	retryStatus, _, retryPlain := sendEncryptedRemote("e2ee_mobile_commit_attempt_2", http.MethodPost, "/api/hoxi/worktree/commit", commitPayload)
	if retryStatus != http.StatusOK {
		t.Fatalf("encrypted retry commit = %d %s", retryStatus, string(retryPlain))
	}
	var retryResult worktree.CommitAndPushResult
	if err := json.Unmarshal(retryPlain, &retryResult); err != nil {
		t.Fatal(err)
	}
	if !retryResult.Deduplicated || retryResult.Commit != initialResult.Commit || retryResult.ClientRequestID != clientReqID {
		t.Fatalf("unexpected retry result: %+v (want hash=%q deduplicated=true)", retryResult, initialResult.Commit)
	}
	if got := gitRevCount(repoDir); got != baseCommits+1 {
		t.Fatalf("expected exactly 1 commit in git history after reconnect replay, got %d (base=%d)", got, baseCommits)
	}

	// 5. Security guard: inspecting with an outside directory over tunnel is rejected with 403 Forbidden.
	escapeTarget := "/api/hoxi/requests/" + clientReqID + "?kind=worktree_commit&directory=" + outsideDir
	escStatus, _, _ := sendEncryptedRemote("e2ee_mobile_escape_1", http.MethodGet, escapeTarget, nil)
	if escStatus != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on outside directory inspect over tunnel, got %d", escStatus)
	}
}

func TestRequestNeedsRunnerDetectsClientRequestIDAndIdempotencyHeader(t *testing.T) {
	bodyWithID := `{"text":"hello","clientRequestId":"req_abc_1"}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/hoxi/sessions/ses_1/prompt_async", strings.NewReader(bodyWithID))
	if !requestNeedsRunner(req1, nil) {
		t.Fatal("expected requestNeedsRunner=true when JSON body carries clientRequestId")
	}
	var replay map[string]any
	if err := json.NewDecoder(req1.Body).Decode(&replay); err != nil || replay["clientRequestId"] != "req_abc_1" {
		t.Fatalf("expected request body preserved after scan, got err=%v map=%+v", err, replay)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/hoxi/sessions/ses_1/prompt_async", strings.NewReader(`{"text":"hello"}`))
	req2.Header.Set("X-Idempotency-Key", "hdr_req_1")
	if !requestNeedsRunner(req2, nil) {
		t.Fatal("expected requestNeedsRunner=true when X-Idempotency-Key header is set")
	}

	req3 := httptest.NewRequest(http.MethodPost, "/api/hoxi/sessions/ses_1/prompt_async", strings.NewReader(`{"text":"hello"}`))
	if requestNeedsRunner(req3, nil) {
		t.Fatal("expected requestNeedsRunner=false for plain unmanaged prompt without clientRequestId")
	}
}
