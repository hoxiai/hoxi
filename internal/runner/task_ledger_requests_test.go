package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/testutil/execstub"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestRequestLedgerContentDigestConflictAcrossPromptPermissionCommitAndInspect(t *testing.T) {
	repoDir := initTestGitRepo(t)
	stateRoot := t.TempDir()
	storeDir := filepath.Join(stateRoot, "sessions")

	binRoot := t.TempDir()
	claudeDir := filepath.Join(binRoot, "bin")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", binRoot)
	t.Setenv("PATH", agyBinPathWithSystem(claudeDir))
	execstub.Write(t, claudeDir, "claude", `#!/bin/sh
cat >/dev/null
printf '%s\n' '{"type":"result","result":"OK","is_error":false}'
`)

	wtMgr := worktree.NewManager(repoDir)
	svc := NewService(repoDir, wtMgr, statusbar.NewTracker(), storeDir)
	defer svc.CloseAll()

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	wtMgr.RegisterRoutes(mux)

	info := createSandboxSession(t, svc, string(RuntimeClaudeCode), repoDir, "local")

	// 1. Prompt: same clientRequestId + same payload deduplicates; different payload returns 409 request_conflict.
	promptBodyA := `{"text":"implement alpha","clientRequestId":"req-prompt-1"}`
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+info.ID+"/prompt_async", strings.NewReader(promptBodyA)))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first prompt status = %d body=%s", rec1.Code, rec1.Body.String())
	}
	var firstReceipt PromptReceipt
	if err := json.Unmarshal(rec1.Body.Bytes(), &firstReceipt); err != nil {
		t.Fatal(err)
	}
	if firstReceipt.Deduplicated || firstReceipt.MessageID == "" {
		t.Fatalf("unexpected first prompt receipt: %+v", firstReceipt)
	}

	// Wait for fast stub turn to finish so turn lock/process is idle.
	for deadline := time.Now().Add(3 * time.Second); svc.isSessionBusy(info.ID) && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}

	rec1Dup := httptest.NewRecorder()
	mux.ServeHTTP(rec1Dup, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+info.ID+"/prompt_async", strings.NewReader(promptBodyA)))
	if rec1Dup.Code != http.StatusOK || rec1Dup.Header().Get("X-Hoxi-Deduplicated") != "true" {
		t.Fatalf("duplicate prompt status=%d hdr=%q body=%s", rec1Dup.Code, rec1Dup.Header().Get("X-Hoxi-Deduplicated"), rec1Dup.Body.String())
	}
	var dupReceipt PromptReceipt
	if err := json.Unmarshal(rec1Dup.Body.Bytes(), &dupReceipt); err != nil {
		t.Fatal(err)
	}
	if !dupReceipt.Deduplicated || dupReceipt.MessageID != firstReceipt.MessageID {
		t.Fatalf("expected deduplicated original receipt, got %+v (want messageId=%q)", dupReceipt, firstReceipt.MessageID)
	}

	promptBodyB := `{"text":"implement beta instead","clientRequestId":"req-prompt-1"}`
	rec1Conflict := httptest.NewRecorder()
	mux.ServeHTTP(rec1Conflict, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+info.ID+"/prompt_async", strings.NewReader(promptBodyB)))
	assertCodedConflict(t, rec1Conflict, ErrCodeRequestConflict)

	// Inspect with matching digest returns 200 completed; mismatched digest returns 409 request_conflict.
	goodPromptDigest := PromptRequestDigest(info.ID, PromptRequest{Text: "implement alpha"})
	badPromptDigest := PromptRequestDigest(info.ID, PromptRequest{Text: "implement beta instead"})

	inspOK := httptest.NewRecorder()
	mux.ServeHTTP(inspOK, httptest.NewRequest(http.MethodGet, "/api/hoxi/requests/req-prompt-1?kind=prompt&scope="+info.ID+"&digest="+goodPromptDigest, nil))
	if inspOK.Code != http.StatusOK {
		t.Fatalf("inspect matching digest = %d %s", inspOK.Code, inspOK.Body.String())
	}
	var inspPayload RequestInspectResponse
	if err := json.Unmarshal(inspOK.Body.Bytes(), &inspPayload); err != nil {
		t.Fatal(err)
	}
	if inspPayload.Status != RequestStatusCompleted {
		t.Fatalf("inspect status = %q, want completed", inspPayload.Status)
	}

	inspBad := httptest.NewRecorder()
	mux.ServeHTTP(inspBad, httptest.NewRequest(http.MethodGet, "/api/hoxi/requests/req-prompt-1?kind=prompt&scope="+info.ID+"&digest="+badPromptDigest, nil))
	assertCodedConflict(t, inspBad, ErrCodeRequestConflict)

	// 2. Permission reply: same clientRequestId + different reply returns 409 request_conflict;
	// a second device replying with a DIFFERENT clientRequestId after settlement returns 409 already_answered (or 404 on runner).
	permID := runnerPermissionID + "conflict_test_1"
	replyCh := make(chan approvalReply, 1)
	svc.gate.mu.Lock()
	svc.gate.pending[permID] = &pendingApproval{
		sid:   info.ID,
		req:   map[string]interface{}{"id": permID, "sessionID": info.ID, "permission": "bash", "directory": repoDir},
		reply: replyCh,
		done:  make(chan struct{}),
	}
	svc.gate.mu.Unlock()

	permRec1 := httptest.NewRecorder()
	mux.ServeHTTP(permRec1, httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/"+permID+"/reply", strings.NewReader(`{"reply":"once","clientRequestId":"req-perm-1"}`)))
	if permRec1.Code != http.StatusOK {
		t.Fatalf("first permission reply = %d %s", permRec1.Code, permRec1.Body.String())
	}
	// Cross-device race while waiter has not yet drained replyCh: second device with different clientRequestId gets 409 already_answered.
	permCrossDevice := httptest.NewRecorder()
	mux.ServeHTTP(permCrossDevice, httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/"+permID+"/reply", strings.NewReader(`{"reply":"reject","clientRequestId":"req-perm-device-b"}`)))
	assertCodedConflict(t, permCrossDevice, ErrCodeAlreadyAnswered)

	// Simulate pending map cleaned up after waiter consumes reply.
	svc.gate.mu.Lock()
	delete(svc.gate.pending, permID)
	svc.gate.mu.Unlock()

	permDup := httptest.NewRecorder()
	mux.ServeHTTP(permDup, httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/"+permID+"/reply", strings.NewReader(`{"reply":"once","clientRequestId":"req-perm-1"}`)))
	if permDup.Code != http.StatusOK || permDup.Header().Get("X-Hoxi-Deduplicated") != "true" {
		t.Fatalf("duplicate permission reply = %d hdr=%q body=%s", permDup.Code, permDup.Header().Get("X-Hoxi-Deduplicated"), permDup.Body.String())
	}

	permConflict := httptest.NewRecorder()
	mux.ServeHTTP(permConflict, httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/"+permID+"/reply", strings.NewReader(`{"reply":"reject","message":"changed mind","clientRequestId":"req-perm-1"}`)))
	assertCodedConflict(t, permConflict, ErrCodeRequestConflict)

	// Also verify OpenCode permission path: once device A settles per_oc_race_1,
	// device A's exact retry deduplicates (200), payload mismatch returns 409 request_conflict,
	// and device B with a different clientRequestId gets 409 already_answered.
	ocPermID := "per_oc_race_1"
	ocDigestA := PermissionReplyDigest(ocPermID, "once", "", false)
	svc.ledger.CompleteRequest(RequestKindPermissionReply, ocPermID, info.ID, "req-oc-dev-a", ocDigestA, http.StatusOK, []byte("true"), map[string]string{"reply": "once"})
	svc.settleOpenCodePermission(ocPermID)

	ocDup := httptest.NewRecorder()
	mux.ServeHTTP(ocDup, httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/"+ocPermID+"/reply?directory="+repoDir, strings.NewReader(`{"reply":"once","clientRequestId":"req-oc-dev-a"}`)))
	if ocDup.Code != http.StatusOK || ocDup.Header().Get("X-Hoxi-Deduplicated") != "true" {
		t.Fatalf("OpenCode device A replay = %d hdr=%q body=%s", ocDup.Code, ocDup.Header().Get("X-Hoxi-Deduplicated"), ocDup.Body.String())
	}
	ocMismatch := httptest.NewRecorder()
	mux.ServeHTTP(ocMismatch, httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/"+ocPermID+"/reply?directory="+repoDir, strings.NewReader(`{"reply":"reject","clientRequestId":"req-oc-dev-a"}`)))
	assertCodedConflict(t, ocMismatch, ErrCodeRequestConflict)

	ocRecB := httptest.NewRecorder()
	mux.ServeHTTP(ocRecB, httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/"+ocPermID+"/reply?directory="+repoDir, strings.NewReader(`{"reply":"reject","clientRequestId":"req-oc-dev-b"}`)))
	assertCodedConflict(t, ocRecB, ErrCodeAlreadyAnswered)

	// 3. Worktree commit: same clientRequestId + different commit message returns 409 request_conflict.
	if err := os.WriteFile(filepath.Join(repoDir, "feature.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitBodyA, _ := json.Marshal(map[string]any{
		"sessionID":       info.ID,
		"message":         "feat: add feature v1",
		"push":            false,
		"clientRequestId": "req-commit-1",
	})
	cRec1 := httptest.NewRecorder()
	mux.ServeHTTP(cRec1, httptest.NewRequest(http.MethodPost, "/api/hoxi/worktree/commit", bytes.NewReader(commitBodyA)))
	if cRec1.Code != http.StatusOK {
		t.Fatalf("first worktree commit = %d %s", cRec1.Code, cRec1.Body.String())
	}

	commitBodyB, _ := json.Marshal(map[string]any{
		"sessionID":       info.ID,
		"message":         "feat: totally different commit message",
		"push":            false,
		"clientRequestId": "req-commit-1",
	})
	cRecConflict := httptest.NewRecorder()
	mux.ServeHTTP(cRecConflict, httptest.NewRequest(http.MethodPost, "/api/hoxi/worktree/commit", bytes.NewReader(commitBodyB)))
	assertCodedConflict(t, cRecConflict, ErrCodeRequestConflict)
}

func TestCrashDuringAdmittedCommitSettlesToOutcomeUnknownWithEvidence(t *testing.T) {
	repoDir := initTestGitRepo(t)
	stateRoot := t.TempDir()
	storeDir := filepath.Join(stateRoot, "sessions")

	wtMgr1 := worktree.NewManager(repoDir)
	svc1 := NewService(repoDir, wtMgr1, statusbar.NewTracker(), storeDir)
	info := createSandboxSession(t, svc1, string(RuntimeClaudeCode), repoDir, "local")

	preHead := worktree.ObserveGitHead(repoDir)
	if preHead == "" {
		t.Fatal("expected initial git HEAD")
	}

	// Simulate Hoxi recording status="admitted" on disk right before executing git commit,
	// git commit succeeding on disk, and Hoxi crashing (SIGKILL) before CompleteCommit runs.
	commitMsg := "fix: critical bug before power loss"
	scope := worktree.CommitRequestScope(info.ID, repoDir)
	digest := worktree.CommitRequestDigest(scope, commitMsg, false, nil)
	adm := svc1.AdmitCommit(scope, info.ID, "req-crash-commit-1", digest, map[string]string{
		"preHead": preHead,
		"workDir": repoDir,
	})
	if adm.State != worktree.CommitAdmissionNew {
		t.Fatalf("expected new admission, got %+v", adm)
	}

	if err := os.WriteFile(filepath.Join(repoDir, "crashed_change.txt"), []byte("committed before crash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitInDir(t, repoDir, "add", "crashed_change.txt")
	runGitInDir(t, repoDir, "commit", "-m", commitMsg)
	commitsBeforeRestart := gitCommitCount(t, repoDir)
	svc1.CloseAll()

	// Boot a fresh Service + worktree.Manager against the same state directory.
	wtMgr2 := worktree.NewManager(repoDir)
	svc2 := NewService(repoDir, wtMgr2, statusbar.NewTracker(), storeDir)
	defer svc2.CloseAll()

	mux2 := http.NewServeMux()
	svc2.RegisterRoutes(mux2)
	wtMgr2.RegisterRoutes(mux2)

	// 1. Inspect must report status == "outcome_unknown" with live git evidence showing HEAD advanced.
	inspRec := httptest.NewRecorder()
	mux2.ServeHTTP(inspRec, httptest.NewRequest(http.MethodGet, "/api/hoxi/requests/req-crash-commit-1?kind=worktree_commit&scope="+info.ID, nil))
	if inspRec.Code != http.StatusOK {
		t.Fatalf("inspect after crash = %d %s", inspRec.Code, inspRec.Body.String())
	}
	var insp RequestInspectResponse
	if err := json.Unmarshal(inspRec.Body.Bytes(), &insp); err != nil {
		t.Fatal(err)
	}
	if insp.Status != RequestStatusOutcomeUnknown || insp.ErrorCode != ErrCodeOutcomeUnknown {
		t.Fatalf("expected outcome_unknown after crash, got %+v", insp)
	}
	if insp.Evidence["headAdvanced"] != true || insp.Evidence["workingTreeClean"] != true {
		t.Fatalf("expected evidence headAdvanced=true & workingTreeClean=true, got %+v", insp.Evidence)
	}

	// 2. Retrying POST /api/hoxi/worktree/commit with the same clientRequestId must be blocked
	// with 409 outcome_unknown and MUST NOT create another commit even if working tree has new edits.
	if err := os.WriteFile(filepath.Join(repoDir, "crashed_change.txt"), []byte("new dirty edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	retryBody, _ := json.Marshal(map[string]any{
		"sessionID":       info.ID,
		"message":         commitMsg,
		"push":            false,
		"clientRequestId": "req-crash-commit-1",
	})
	retryRec := httptest.NewRecorder()
	mux2.ServeHTTP(retryRec, httptest.NewRequest(http.MethodPost, "/api/hoxi/worktree/commit", bytes.NewReader(retryBody)))
	assertCodedConflict(t, retryRec, ErrCodeOutcomeUnknown)

	if gotCommits := gitCommitCount(t, repoDir); gotCommits != commitsBeforeRestart {
		t.Fatalf("retry of outcome_unknown request created a duplicate commit: got %d, want %d", gotCommits, commitsBeforeRestart)
	}
}

func TestTaskLedgerV1ToV2HotUpgradePreservesEntriesAndReceipts(t *testing.T) {
	repoDir := initTestGitRepo(t)
	stateRoot := t.TempDir()
	storeDir := filepath.Join(stateRoot, "sessions")
	ledgerPath := filepath.Join(stateRoot, taskLedgerFile)

	nowMs := time.Now().UnixMilli()
	expMs := nowMs + 10*time.Minute.Milliseconds()

	seedStore := NewStore(storeDir)
	sessionID := "ses_v1_upgrade_1"
	if err := seedStore.Put(SessionRecord{
		ID:           sessionID,
		Runtime:      string(RuntimeClaudeCode),
		ExternalID:   "ext_v1_resume",
		ProjectDir:   repoDir,
		WorktreePath: repoDir,
		SandboxMode:  "local",
		CreatedAt:    nowMs,
		UpdatedAt:    nowMs,
	}); err != nil {
		t.Fatal(err)
	}
	seedStore.Flush()

	// Write a genuine v1 task-ledger.json containing GracefulRestart entry + PromptReceipts + PermissionReplies.
	v1Doc := map[string]any{
		"version": 1,
		"entries": []map[string]any{{
			"sessionID":       sessionID,
			"runtime":         "claude-code",
			"externalID":      "ext_v1_resume",
			"userMsgID":       "msg_u_v1_1",
			"mode":            "build",
			"startedAt":       nowMs,
			"updatedAt":       nowMs,
			"gracefulRestart": true,
		}},
		"promptReceipts": []map[string]any{{
			"sessionID":       sessionID,
			"clientRequestId": "v1-prompt-req-1",
			"receipt": map[string]any{
				"ok":              true,
				"instructionId":   42,
				"messageId":       "msg_u_v1_1",
				"clientRequestId": "v1-prompt-req-1",
			},
			"expiresAtMs": expMs,
		}},
		"permissionReplies": []map[string]any{{
			"permissionID":    "per_rnr_v1_1",
			"sessionID":       sessionID,
			"clientRequestId": "v1-perm-req-1",
			"reply":           "once",
			"rawBody":         []byte("true\n"),
			"expiresAtMs":     expMs,
		}},
	}
	rawV1, err := json.MarshalIndent(v1Doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath, rawV1, 0o600); err != nil {
		t.Fatal(err)
	}

	wtMgr := worktree.NewManager(repoDir)
	svc := NewService(repoDir, wtMgr, statusbar.NewTracker(), storeDir)
	defer svc.CloseAll()

	// 1. Must NOT create any .corrupt-* backup file.
	corruptMatches, _ := filepath.Glob(ledgerPath + ".corrupt-*")
	if len(corruptMatches) != 0 {
		t.Fatalf("v1 task-ledger.json triggered corrupt backup on v2 upgrade: %v", corruptMatches)
	}

	// 2. Graceful-restart turn checkpoint must be queued in pendingResumes.
	if !svc.isSessionBusy(sessionID) {
		t.Fatal("expected v1 gracefulRestart turn entry to remain queued on v2 boot")
	}

	// 3. Migrated prompt receipt & permission reply must deduplicate via both lookup helpers and HTTP routes.
	gotPrompt, dupPrompt := svc.lookupPromptReceipt(sessionID, "v1-prompt-req-1")
	if !dupPrompt || gotPrompt.InstructionID != 42 || gotPrompt.MessageID != "msg_u_v1_1" {
		t.Fatalf("migrated v1 prompt receipt = (%+v, %v)", gotPrompt, dupPrompt)
	}
	gotPerm, dupPerm := svc.lookupPermissionReply("per_rnr_v1_1", "v1-perm-req-1", "once")
	if !dupPerm || string(gotPerm) != "true\n" {
		t.Fatalf("migrated v1 permission reply = (%q, %v)", string(gotPerm), dupPerm)
	}
	// Mismatched reply value on migrated v1 permission reply must still conflict!
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	permConflictRec := httptest.NewRecorder()
	mux.ServeHTTP(permConflictRec, httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/per_rnr_v1_1/reply", strings.NewReader(`{"reply":"reject","clientRequestId":"v1-perm-req-1"}`)))
	assertCodedConflict(t, permConflictRec, ErrCodeRequestConflict)

	// 4. Trigger a ledger write and verify on-disk file upgraded cleanly to version 2.
	svc.storePromptReceipt(sessionID, "v2-new-req", PromptReceipt{OK: true, InstructionID: 43, MessageID: "msg_u_v2_2", ClientRequestID: "v2-new-req"})
	rawUpgraded, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	var upgradedDoc taskLedgerDoc
	if err := json.Unmarshal(rawUpgraded, &upgradedDoc); err != nil {
		t.Fatal(err)
	}
	if upgradedDoc.Version != 2 || len(upgradedDoc.Requests) < 3 {
		t.Fatalf("expected persisted version=2 with >=3 migrated+new requests, got version=%d len=%d", upgradedDoc.Version, len(upgradedDoc.Requests))
	}

	// Cancel queued resume before shutdown.
	svc.AbortSession(sessionID)
	_ = context.Background()
}

func assertCodedConflict(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	var errBody map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("unmarshal 409 JSON: %v (raw=%s)", err, rec.Body.String())
	}
	if errBody["code"] != wantCode {
		t.Fatalf("409 code = %v, want %q (body=%s)", errBody["code"], wantCode, rec.Body.String())
	}
}

func runGitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
}

func gitCommitCount(t *testing.T, dir string) int {
	t.Helper()
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-list --count HEAD: %v", err)
	}
	var n int
	for _, ch := range strings.TrimSpace(string(out)) {
		n = n*10 + int(ch-'0')
	}
	return n
}
