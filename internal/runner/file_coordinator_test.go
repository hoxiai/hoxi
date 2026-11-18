package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/agyhook"
)

type coordTestNativeEngine struct {
	mu   sync.Mutex
	runs map[string]func(ctx context.Context, req NativeTurnRequest) error
}

func (e *coordTestNativeEngine) RunTurn(ctx context.Context, req NativeTurnRequest) error {
	e.mu.Lock()
	fn := e.runs[req.SessionID]
	e.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, req)
}

func (e *coordTestNativeEngine) Summarize(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

func TestFileCoordinatorSerializesConcurrentLocalEditsAndProtectsDifferentFiles(t *testing.T) {
	repo := initTestGitRepo(t)
	sharedFile := filepath.Join(repo, "shared.go")
	otherFile := filepath.Join(repo, "other.go")
	if err := os.WriteFile(sharedFile, []byte("package main\nvar A = 1\nvar B = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherFile, []byte("package main\nvar Other = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(repo, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	infoA, _, err := svc.CreateSession(CreateSessionRequest{
		Runtime: "native", Directory: repo, SandboxMode: "local", ProviderID: "openai", ModelID: "gpt-5",
	}, repo)
	if err != nil {
		t.Fatal(err)
	}
	infoB, _, err := svc.CreateSession(CreateSessionRequest{
		Runtime: "native", Directory: repo, SandboxMode: "local", ProviderID: "openai", ModelID: "gpt-5",
	}, repo)
	if err != nil {
		t.Fatal(err)
	}
	infoC, _, err := svc.CreateSession(CreateSessionRequest{
		Runtime: "native", Directory: repo, SandboxMode: "local", ProviderID: "openai", ModelID: "gpt-5",
	}, repo)
	if err != nil {
		t.Fatal(err)
	}

	step1Done := make(chan struct{})
	allowStep2 := make(chan struct{})
	var bWaitedNotice atomic.Bool
	var bFinishedAfterA atomic.Bool
	var aFinished atomic.Bool

	eng := &coordTestNativeEngine{
		runs: map[string]func(ctx context.Context, req NativeTurnRequest) error{
			infoA.ID: func(ctx context.Context, req NativeTurnRequest) error {
				coord1, err := req.CoordinateFileEdit(ctx, []string{sharedFile}, nil)
				if err != nil {
					return err
				}
				if err := os.WriteFile(sharedFile, []byte("package main\nvar A = 2\nvar B = 1\n"), 0o644); err != nil {
					coord1.Release()
					return err
				}
				req.RecordFileEdit([]string{sharedFile})
				coord1.Release()
				close(step1Done)

				<-allowStep2
				coord2, err := req.CoordinateFileEdit(ctx, []string{sharedFile}, nil)
				if err != nil {
					return err
				}
				if err := os.WriteFile(sharedFile, []byte("package main\nvar A = 3\nvar B = 1\n"), 0o644); err != nil {
					coord2.Release()
					return err
				}
				req.RecordFileEdit([]string{sharedFile})
				coord2.Release()
				aFinished.Store(true)
				return nil
			},
			infoB.ID: func(ctx context.Context, req NativeTurnRequest) error {
				coord, err := req.CoordinateFileEdit(ctx, []string{sharedFile}, func(waitedPath string) {
					if strings.HasSuffix(waitedPath, "shared.go") {
						bWaitedNotice.Store(true)
					}
				})
				if err != nil {
					return err
				}
				defer coord.Release()
				if aFinished.Load() {
					bFinishedAfterA.Store(true)
				}
				if !coord.Waited || !coord.ModifiedByPeer[sharedFile] {
					t.Errorf("expected session B to wait and see ModifiedByPeer=true, got waited=%v map=%v", coord.Waited, coord.ModifiedByPeer)
				}
				raw, err := os.ReadFile(sharedFile)
				if err != nil {
					return err
				}
				updated := strings.Replace(string(raw), "var B = 1", "var B = 9", 1)
				if err := os.WriteFile(sharedFile, []byte(updated), 0o644); err != nil {
					return err
				}
				req.RecordFileEdit([]string{sharedFile})
				return nil
			},
			infoC.ID: func(ctx context.Context, req NativeTurnRequest) error {
				coord, err := req.CoordinateFileEdit(ctx, []string{otherFile}, func(string) {
					t.Errorf("unrelated file other.go must never wait on shared.go")
				})
				if err != nil {
					return err
				}
				defer coord.Release()
				if coord.Waited {
					t.Errorf("expected zero wait on other.go")
				}
				if err := os.WriteFile(otherFile, []byte("package main\nvar Other = 99\n"), 0o644); err != nil {
					return err
				}
				req.RecordFileEdit([]string{otherFile})
				return nil
			},
		},
	}
	svc.SetNativeEngine(eng)

	var wg sync.WaitGroup
	errCh := make(chan error, 3)

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := svc.RunPromptAndWait(context.Background(), infoA.ID, PromptRequest{Text: "edit shared in 2 steps"})
		errCh <- err
	}()

	<-step1Done

	// Unrelated file in session C finishes immediately while session A is still mid-turn!
	if _, err := svc.RunPromptAndWait(context.Background(), infoC.ID, PromptRequest{Text: "edit other.go"}); err != nil {
		t.Fatalf("session C on unrelated file failed: %v", err)
	}

	// Start session B on shared.go while session A holds the turn lease.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := svc.RunPromptAndWait(context.Background(), infoB.ID, PromptRequest{Text: "edit shared"})
		errCh <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !bWaitedNotice.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !bWaitedNotice.Load() {
		t.Fatal("expected session B to emit wait notice while session A owns shared.go lease")
	}

	// Stall watchdog must NOT treat B as stalled while waiting on healthy active A.
	svc.mu.Lock()
	apB := svc.procs[infoB.ID]
	svc.mu.Unlock()
	if apB == nil || !svc.isWaitingOnPeerFileLease(infoB.ID) {
		t.Fatal("expected isWaitingOnPeerFileLease(infoB) to report true while blocked on healthy peer A")
	}

	close(allowStep2)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent local session turn failed: %v", err)
		}
	}

	if !bFinishedAfterA.Load() {
		t.Fatal("expected session B to acquire shared.go only after session A completed its turn")
	}
	finalBytes, err := os.ReadFile(sharedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(finalBytes) != "package main\nvar A = 3\nvar B = 9\n" {
		t.Fatalf("expected both sessions' edits preserved sequentially, got:\n%s", string(finalBytes))
	}
}

func TestFileCoordinatorCycleDetectionSubagentInheritanceAndCleanup(t *testing.T) {
	repo := initTestGitRepo(t)
	fileA := filepath.Join(repo, "a.go")
	fileB := filepath.Join(repo, "b.go")
	_ = os.WriteFile(fileA, []byte("package main\n"), 0o644)
	_ = os.WriteFile(fileB, []byte("package main\n"), 0o644)

	svc := NewService(repo, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	s1, _, err := svc.CreateSession(CreateSessionRequest{
		Runtime: "claude-code", Directory: repo, SandboxMode: "local", ModelID: "claude-sonnet-4-5",
	}, repo)
	if err != nil {
		t.Fatal(err)
	}
	s2, _, err := svc.CreateSession(CreateSessionRequest{
		Runtime: "claude-code", Directory: repo, SandboxMode: "local", ModelID: "claude-sonnet-4-5",
	}, repo)
	if err != nil {
		t.Fatal(err)
	}

	// Register active processes for s1 and s2 so turn-scoped leases are retained.
	ap1 := &activeProcess{sessionID: s1.ID, runtime: "claude-code", done: make(chan struct{}), abortCh: make(chan struct{})}
	ap2 := &activeProcess{sessionID: s2.ID, runtime: "claude-code", done: make(chan struct{}), abortCh: make(chan struct{})}
	svc.mu.Lock()
	svc.procs[s1.ID] = ap1
	svc.procs[s2.ID] = ap2
	svc.mu.Unlock()
	svc.beginSessionFileTurn(s1.ID)
	svc.beginSessionFileTurn(s2.ID)

	// 1. s1 claims fileA, s2 claims fileB.
	if ok, reason := svc.acquireHookFileLease(context.Background(), s1.ID, fileA); !ok {
		t.Fatalf("s1 claim fileA failed: %s", reason)
	}
	if ok, reason := svc.acquireHookFileLease(context.Background(), s2.ID, fileB); !ok {
		t.Fatalf("s2 claim fileB failed: %s", reason)
	}

	// 2. Synchronous subagent child of s1 shares s1's lease owner and does NOT block on fileA!
	parentRec, _ := svc.store.Get(s1.ID)
	childInfo, ok := svc.childSessionFactory(parentRec, "claude-sonnet-4-5", "build")(ChildSessionSpec{
		ExternalID: "sub-1", Title: "Subagent",
	})
	if !ok {
		t.Fatal("failed to create subagent child session")
	}
	if ok, reason := svc.acquireHookFileLease(context.Background(), childInfo.ID, fileA); !ok {
		t.Fatalf("expected synchronous subagent child to inherit parent lease on fileA, got deny: %s", reason)
	}

	// 3. AB-BA deadlock cycle: s1 starts waiting on fileB; s2 then requests fileA -> must fail immediately!
	s1Waiting := make(chan struct{})
	s1Done := make(chan bool, 1)
	go func() {
		rel, _, _, lErr := svc.fileCoord.AcquireAndLock(context.Background(), s1.ID, []string{fileB}, 5*time.Second, func(string) {
			close(s1Waiting)
		})
		if rel != nil {
			rel()
		}
		s1Done <- (lErr == nil)
	}()
	<-s1Waiting

	okCycle, reasonCycle := svc.acquireHookFileLease(context.Background(), s2.ID, fileA)
	if okCycle || !strings.Contains(reasonCycle, "concurrent file edit conflict") {
		t.Fatalf("expected AB-BA cycle to be rejected immediately with conflict reason, got ok=%v reason=%q", okCycle, reasonCycle)
	}

	// 4. Releasing s2 unblocks s1 cleanly; deleting s1 leaves zero residual coordinator entries.
	svc.releaseActiveProcess(s2.ID, ap2)
	close(ap2.done)
	if !<-s1Done {
		t.Fatal("expected s1 to unblock and acquire fileB once s2 finished")
	}

	// Verify Claude HTTP PreToolUse hook waits on s1's active lease on fileA and unblocks when s1 aborts.
	svc.SetHookBaseURL("http://127.0.0.1:5410")
	ap3 := &activeProcess{sessionID: s2.ID, runtime: "claude-code", done: make(chan struct{}), abortCh: make(chan struct{})}
	svc.mu.Lock()
	svc.procs[s2.ID] = ap3
	svc.mu.Unlock()
	_, env, revoke := svc.issueClaudeHook("build", s2.ID, "msg_hook_1")
	defer revoke()
	var token string
	for _, kv := range env {
		if strings.HasPrefix(kv, claudeHookTokenEnv+"=") {
			token = strings.TrimPrefix(kv, claudeHookTokenEnv+"=")
		}
	}

	hookDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		body, _ := json.Marshal(map[string]any{
			"tool_name":  "Edit",
			"tool_input": map[string]any{"file_path": fileA},
			"cwd":        repo,
		})
		req := httptest.NewRequest(http.MethodPost, claudeHookPreToolPath, bytes.NewReader(body))
		req.RemoteAddr = "127.0.0.1:55123"
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(agyhook.HeaderSession, s2.ID)
		w := httptest.NewRecorder()
		svc.handleClaudeHookPreTool(w, req)
		hookDone <- w
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !svc.isWaitingOnPeerFileLease(s2.ID) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !svc.isWaitingOnPeerFileLease(s2.ID) {
		t.Fatal("expected Claude PreToolUse hook for s2 to wait on s1's active lease")
	}

	svc.AbortSession(s1.ID)
	recW := <-hookDone
	if recW.Code != http.StatusOK || strings.Contains(recW.Body.String(), `"deny"`) {
		t.Fatalf("expected Claude PreToolUse hook to proceed after s1 aborted, got %d %s", recW.Code, recW.Body.String())
	}

	svc.releaseActiveProcess(s1.ID, ap1)
	close(ap1.done)
	svc.releaseActiveProcess(s2.ID, ap3)
	close(ap3.done)
	if err := svc.deleteConversation(context.Background(), s1.ID); err != nil {
		t.Fatalf("deleteConversation s1: %v", err)
	}
	if err := svc.deleteConversation(context.Background(), s2.ID); err != nil {
		t.Fatalf("deleteConversation s2: %v", err)
	}

	svc.fileCoord.mu.Lock()
	leakedOwners := len(svc.fileCoord.ownerTurns)
	leakedLeases := len(svc.fileCoord.leases)
	leakedWaits := len(svc.fileCoord.waitingOn)
	leakedSeqs := len(svc.fileCoord.turnStartSeq)
	svc.fileCoord.mu.Unlock()
	if leakedOwners != 0 || leakedLeases != 0 || leakedWaits != 0 || leakedSeqs != 0 {
		t.Fatalf("expected zero leaked coordinator maps after turn release + session delete, got owners=%d leases=%d waits=%d seqs=%d",
			leakedOwners, leakedLeases, leakedWaits, leakedSeqs)
	}
}

func TestFileCoordinatorDistinguishesAbortedUnmodifiedLeaseFromDirectCLIDiskEdit(t *testing.T) {
	dir := t.TempDir()
	unchangedPath := filepath.Join(dir, "unchanged.go")
	cliEditedPath := filepath.Join(dir, "cli_edited.go")
	_ = os.WriteFile(unchangedPath, []byte("package p\nvar X = 1\n"), 0o644)
	_ = os.WriteFile(cliEditedPath, []byte("package p\nvar Y = 1\n"), 0o644)

	active := map[string]bool{"ses_old": true, "ses_a": true, "ses_b": true}
	var activeMu sync.Mutex
	coord := newFileEditCoordinator(func(id string) bool {
		activeMu.Lock()
		defer activeMu.Unlock()
		return active[id]
	})

	// Historical edit before ses_a / ses_b turns started must NOT trigger false ModifiedByPeer on aborted lease.
	coord.RecordModified("ses_old", []string{unchangedPath})
	coord.ReleaseTurn("ses_old")
	activeMu.Lock()
	active["ses_old"] = false
	activeMu.Unlock()

	coord.BeginTurn("ses_a")
	coord.BeginTurn("ses_b")

	// ses_a claims both files via PreToolUse lease, edits ONLY cliEditedPath directly on disk (without RecordModified), then aborts.
	relA, _, _, err := coord.AcquireAndLock(context.Background(), "ses_a", []string{unchangedPath, cliEditedPath}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	relA()
	if err := os.WriteFile(cliEditedPath, []byte("package p\nvar Y = 2 // written by CLI subprocess\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bWaiting := make(chan struct{})
	type bResult struct {
		waited  bool
		modPeer map[string]bool
		err     error
	}
	resCh := make(chan bResult, 1)
	go func() {
		relB, waited, modPeer, bErr := coord.AcquireAndLock(context.Background(), "ses_b", []string{unchangedPath, cliEditedPath}, 2*time.Second, func(string) {
			select {
			case <-bWaiting:
			default:
				close(bWaiting)
			}
		})
		if relB != nil {
			relB()
		}
		resCh <- bResult{waited: waited, modPeer: modPeer, err: bErr}
	}()

	<-bWaiting
	coord.ReleaseTurn("ses_a")
	got := <-resCh
	if got.err != nil || !got.waited {
		t.Fatalf("expected ses_b to wait and succeed, got waited=%v err=%v", got.waited, got.err)
	}
	if got.modPeer[unchangedPath] {
		t.Fatalf("unchanged.go was NOT modified during ses_b's turn; got false-positive ModifiedByPeer=%v", got.modPeer)
	}
	if !got.modPeer[cliEditedPath] {
		t.Fatalf("cli_edited.go WAS modified on disk by ses_a subprocess while ses_b waited; expected ModifiedByPeer=true, got %v", got.modPeer)
	}
}
