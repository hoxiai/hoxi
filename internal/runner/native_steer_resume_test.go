package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/worktree"
)

type scriptedNativeEngine struct {
	runFn func(ctx context.Context, req NativeTurnRequest) error
}

func (e *scriptedNativeEngine) RunTurn(ctx context.Context, req NativeTurnRequest) error {
	return e.runFn(ctx, req)
}

func (e *scriptedNativeEngine) Summarize(_ context.Context, _, _, transcript string) (string, error) {
	return "Summary: " + transcript, nil
}

func TestNativeSteerDuringApprovalAndDisconnectIdempotency(t *testing.T) {
	repoDir := initTestGitRepo(t)
	stateRoot := t.TempDir()
	storeDir := filepath.Join(stateRoot, "sessions")
	t.Setenv("HOME", stateRoot)

	wtMgr := worktree.NewManager(repoDir)
	svc := NewService(repoDir, wtMgr, statusbar.NewTracker(), storeDir)
	defer svc.CloseAll()

	var gatedExecCount atomic.Int32
	var steeredSeen atomic.Bool
	approvalAsked := make(chan struct{}, 1)

	svc.SetNativeEngine(&scriptedNativeEngine{
		runFn: func(ctx context.Context, req NativeTurnRequest) error {
			callID := "call_gated_1"
			partID := "tool_1"
			inRaw := json.RawMessage(`{"command":"echo gated_step"}`)
			req.Emit(NativeEvent{
				Kind: NativeEventToolRunning, Step: 1, PartID: partID, CallID: callID,
				ToolName: "bash", Input: map[string]any{"command": "echo gated_step"},
			})
			select {
			case approvalAsked <- struct{}{}:
			default:
			}
			permErr := req.PermissionHook(ctx, NativePermissionRequest{
				SessionID: req.SessionID,
				CallID:    callID,
				ToolName:  "bash",
				Input:     inRaw,
				ForceAsk:  true,
			})
			if permErr == nil {
				gatedExecCount.Add(1)
				req.Emit(NativeEvent{Kind: NativeEventToolCompleted, Step: 1, PartID: partID, CallID: callID, ToolName: "bash", Text: "executed"})
			} else {
				req.Emit(NativeEvent{Kind: NativeEventToolError, Step: 1, PartID: partID, CallID: callID, ToolName: "bash", ErrorText: permErr.Error()})
			}

			items := req.SteerDrainItems()
			for _, it := range items {
				if strings.Contains(it.Prompt, "instead inspect git status safely") {
					steeredSeen.Store(true)
				}
			}
			req.Emit(NativeEvent{Kind: NativeEventTextStart, Step: 2, PartID: "txt_2"})
			reply := "Completed steered safe inspection."
			req.Emit(NativeEvent{Kind: NativeEventTextDelta, Step: 2, PartID: "txt_2", Text: reply})
			req.Emit(NativeEvent{Kind: NativeEventTextEnd, Step: 2, PartID: "txt_2", Text: reply})
			req.Emit(NativeEvent{Kind: NativeEventTurnFinish, Step: 2, Finish: "stop"})
			return nil
		},
	})

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	info := createSandboxSession(t, svc, string(RuntimeNative), repoDir, "local")

	initBody := `{"text":"run gated step","clientRequestId":"mob-init-1"}`
	r1 := httptest.NewRecorder()
	mux.ServeHTTP(r1, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+info.ID+"/prompt_async", strings.NewReader(initBody)))
	if r1.Code != http.StatusOK {
		t.Fatalf("initial prompt = %d %s", r1.Code, r1.Body.String())
	}

	select {
	case <-approvalAsked:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for permission gate")
	}
	var permID string
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		svc.gate.mu.Lock()
		for id := range svc.gate.pending {
			permID = id
		}
		svc.gate.mu.Unlock()
		if permID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if permID == "" {
		t.Fatal("expected pending approval entry in gate")
	}

	r1Dup := httptest.NewRecorder()
	mux.ServeHTTP(r1Dup, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+info.ID+"/prompt_async", strings.NewReader(initBody)))
	if r1Dup.Code != http.StatusOK || r1Dup.Header().Get("X-Hoxi-Deduplicated") != "true" {
		t.Fatalf("expected deduplicated initial retry, got %d hdr=%q body=%s", r1Dup.Code, r1Dup.Header().Get("X-Hoxi-Deduplicated"), r1Dup.Body.String())
	}

	steerBody := `{"text":"skip gated step; instead inspect git status safely","clientRequestId":"mob-steer-1"}`
	rSteer := httptest.NewRecorder()
	mux.ServeHTTP(rSteer, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+info.ID+"/prompt_async", strings.NewReader(steerBody)))
	if rSteer.Code != http.StatusOK {
		t.Fatalf("steer prompt = %d %s", rSteer.Code, rSteer.Body.String())
	}

	rSteerDup := httptest.NewRecorder()
	mux.ServeHTTP(rSteerDup, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+info.ID+"/prompt_async", strings.NewReader(steerBody)))
	if rSteerDup.Code != http.StatusOK || rSteerDup.Header().Get("X-Hoxi-Deduplicated") != "true" {
		t.Fatalf("expected deduplicated steer retry, got %d hdr=%q", rSteerDup.Code, rSteerDup.Header().Get("X-Hoxi-Deduplicated"))
	}

	for deadline := time.Now().Add(4 * time.Second); svc.isSessionBusy(info.ID) && time.Now().Before(deadline); {
		time.Sleep(15 * time.Millisecond)
	}
	if svc.isSessionBusy(info.ID) {
		t.Fatal("expected session to become idle after steered turn finished")
	}
	if got := gatedExecCount.Load(); got != 0 {
		t.Fatalf("gated tool executed %d times despite Steer superseding approval!", got)
	}
	if !steeredSeen.Load() {
		t.Fatal("expected Step 2 to receive steered prompt at safe boundary")
	}

	latePerm := httptest.NewRecorder()
	mux.ServeHTTP(latePerm, httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/"+permID+"/reply", strings.NewReader(`{"reply":"once","clientRequestId":"mob-late-perm"}`)))
	if latePerm.Code == http.StatusOK {
		t.Fatalf("expected superseded approval reply to be rejected (404/409), got 200")
	}

	rec, ok := svc.Store().Get(info.ID)
	if !ok || len(rec.Turns) != 4 {
		t.Fatalf("expected 4 chronological turns [u1, a1, u2, a2], got %d", len(rec.Turns))
	}
	if rec.Turns[0].Info.Role != "user" || rec.Turns[1].Info.Role != "assistant" ||
		rec.Turns[2].Info.Role != "user" || rec.Turns[3].Info.Role != "assistant" {
		t.Fatalf("unexpected turn role order: %s, %s, %s, %s",
			rec.Turns[0].Info.Role, rec.Turns[1].Info.Role, rec.Turns[2].Info.Role, rec.Turns[3].Info.Role)
	}
}

func TestNativeLedgerResumeAfterToolWriteDoesNotReExecuteWrite(t *testing.T) {
	repoDir := initTestGitRepo(t)
	stateRoot := t.TempDir()
	storeDir := filepath.Join(stateRoot, "sessions")
	t.Setenv("HOME", stateRoot)

	targetFile := filepath.Join(repoDir, "migrated_output.txt")
	var writeCount atomic.Int32
	step2Blocking := make(chan struct{})

	wtMgr1 := worktree.NewManager(repoDir)
	svc1 := NewService(repoDir, wtMgr1, statusbar.NewTracker(), storeDir)
	svc1.SetNativeEngine(&scriptedNativeEngine{
		runFn: func(ctx context.Context, req NativeTurnRequest) error {
			writeCount.Add(1)
			if err := os.WriteFile(targetFile, []byte("written-once\n"), 0o644); err != nil {
				return err
			}
			req.Emit(NativeEvent{
				Kind: NativeEventToolCompleted, Step: 1, PartID: "prt_w1", CallID: "call_w1",
				ToolName: "write", Title: "migrated_output.txt",
				Input: map[string]any{"filePath": "migrated_output.txt"},
				Text:  "wrote 13 bytes to migrated_output.txt",
			})
			close(step2Blocking)
			<-ctx.Done()
			return ctx.Err()
		},
	})

	info := createSandboxSession(t, svc1, string(RuntimeNative), repoDir, "local")
	if status, err := svc1.startPrompt(context.Background(), info.ID, repoDir, PromptRequest{
		Text:            "create migrated_output.txt and verify it",
		ClientRequestID: "req-resume-write-1",
	}); err != nil || status != http.StatusOK {
		t.Fatalf("startPrompt = (%d, %v)", status, err)
	}

	select {
	case <-step2Blocking:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Step 1 write completion")
	}

	svc1.PrepareForRestart(context.Background())

	var sawCompletedWriteInHistory atomic.Bool
	wtMgr2 := worktree.NewManager(repoDir)
	svc2 := NewService(repoDir, wtMgr2, statusbar.NewTracker(), storeDir)
	defer svc2.CloseAll()

	svc2.SetNativeEngine(&scriptedNativeEngine{
		runFn: func(_ context.Context, req NativeTurnRequest) error {
			for _, m := range req.History {
				for _, b := range m.Content {
					if b.Type == "tool_result" && b.ToolUseID == "call_w1" && strings.Contains(b.Text, "wrote 13 bytes") {
						sawCompletedWriteInHistory.Store(true)
					}
				}
			}
			req.Emit(NativeEvent{Kind: NativeEventTextStart, Step: 1, PartID: "txt_resumed"})
			req.Emit(NativeEvent{Kind: NativeEventTextDelta, Step: 1, PartID: "txt_resumed", Text: "Verified existing migrated_output.txt after restart."})
			req.Emit(NativeEvent{Kind: NativeEventTextEnd, Step: 1, PartID: "txt_resumed", Text: "Verified existing migrated_output.txt after restart."})
			req.Emit(NativeEvent{Kind: NativeEventTurnFinish, Step: 1, Finish: "stop"})
			return nil
		},
	})

	svc2.ResumeInterruptedTurns(context.Background())
	for deadline := time.Now().Add(4 * time.Second); svc2.isSessionBusy(info.ID) && time.Now().Before(deadline); {
		time.Sleep(15 * time.Millisecond)
	}
	if svc2.isSessionBusy(info.ID) {
		t.Fatal("expected resumed native session to complete and become idle")
	}
	if got := writeCount.Load(); got != 1 {
		t.Fatalf("expected mutating write tool to run exactly once across restart, got %d", got)
	}
	if !sawCompletedWriteInHistory.Load() {
		t.Fatal("expected resumed NativeTurnRequest.History to contain Step 1 completed write tool_result")
	}
}

func TestNativeRetryStripsPoisonedSignaturesAndResumesReadOnlyProgressInPlace(t *testing.T) {
	repoDir := initTestGitRepo(t)
	stateRoot := t.TempDir()
	storeDir := filepath.Join(stateRoot, "sessions")
	t.Setenv("HOME", stateRoot)

	wtMgr := worktree.NewManager(repoDir)
	svc := NewService(repoDir, wtMgr, statusbar.NewTracker(), storeDir)
	defer svc.CloseAll()

	var step1ReadRuns atomic.Int32
	var callSeq atomic.Int32
	var sawPoisonedSigOnRetry atomic.Bool
	var sawCompletedReadOnRetry atomic.Bool

	svc.SetNativeEngine(&scriptedNativeEngine{
		runFn: func(_ context.Context, req NativeTurnRequest) error {
			n := callSeq.Add(1)
			if n == 1 {
				step1ReadRuns.Add(1)
				req.Emit(NativeEvent{
					Kind:      NativeEventThinkingEnd,
					Step:      1,
					PartID:    "prt_think_bad",
					Text:      "Inspecting repository layout",
					Signature: "poisoned_proxy_sig",
				})
				req.Emit(NativeEvent{
					Kind:     NativeEventToolCompleted,
					Step:     1,
					PartID:   "prt_read_1",
					CallID:   "call_read_1",
					ToolName: "read",
					Title:    "README.md",
					Input:    map[string]any{"filePath": "README.md"},
					Text:     "# Hoxi Architecture",
				})
				return &MessageErrorError{
					msg: `HTTP 400: {"type":"error","error":{"type":"invalid_request_error","message":"messages.1.content.0: Invalid 'signature' in 'thinking' block"}}`,
				}
			}
			for _, m := range req.History {
				for _, b := range m.Content {
					if b.Type == "thinking" && b.Signature == "poisoned_proxy_sig" {
						sawPoisonedSigOnRetry.Store(true)
					}
					if b.Type == "tool_result" && b.ToolUseID == "call_read_1" {
						sawCompletedReadOnRetry.Store(true)
					}
				}
			}
			req.Emit(NativeEvent{Kind: NativeEventTextStart, Step: 2, PartID: "txt_done"})
			req.Emit(NativeEvent{Kind: NativeEventTextDelta, Step: 2, PartID: "txt_done", Text: "Analysis finished cleanly."})
			req.Emit(NativeEvent{Kind: NativeEventTextEnd, Step: 2, PartID: "txt_done", Text: "Analysis finished cleanly."})
			req.Emit(NativeEvent{Kind: NativeEventTurnFinish, Step: 2, Finish: "stop"})
			return nil
		},
	})

	info := createSandboxSession(t, svc, string(RuntimeNative), repoDir, "local")
	if st, err := svc.startPrompt(context.Background(), info.ID, repoDir, PromptRequest{
		Text:  "analyze project",
		Model: &PromptModel{ProviderID: "subapi", ModelID: "antigravity-claude-opus-5-5-high"},
	}); err != nil || st != http.StatusOK {
		t.Fatalf("initial startPrompt = (%d, %v)", st, err)
	}
	for deadline := time.Now().Add(3 * time.Second); svc.isSessionBusy(info.ID) && time.Now().Before(deadline); {
		time.Sleep(15 * time.Millisecond)
	}

	// Retry same model via ContinueTask: must clear poisoned signature, preserve Step 1 read, and NOT duplicate user turn.
	if st, err := svc.startPrompt(context.Background(), info.ID, repoDir, PromptRequest{
		Text:         "analyze project",
		ContinueTask: true,
		Model:        &PromptModel{ProviderID: "subapi", ModelID: "antigravity-claude-opus-5-5-high"},
	}); err != nil || st != http.StatusOK {
		t.Fatalf("retry startPrompt = (%d, %v)", st, err)
	}
	for deadline := time.Now().Add(3 * time.Second); svc.isSessionBusy(info.ID) && time.Now().Before(deadline); {
		time.Sleep(15 * time.Millisecond)
	}

	if sawPoisonedSigOnRetry.Load() {
		t.Fatal("expected poisoned thinking signature to be stripped from SessionRecord on retry")
	}
	if !sawCompletedReadOnRetry.Load() || step1ReadRuns.Load() != 1 {
		t.Fatalf("expected Step 1 read tool result to be reused without re-running (runs=%d, seen=%v)", step1ReadRuns.Load(), sawCompletedReadOnRetry.Load())
	}

	rec, ok := svc.store.Get(info.ID)
	if !ok {
		t.Fatal("missing session record")
	}
	userTurnCount := 0
	for _, turn := range rec.Turns {
		if turn.Info.Role == "user" {
			userTurnCount++
		}
		if turn.Info.Error != nil {
			t.Fatalf("expected error to be cleared on resumed partial assistant turn, got %+v", turn.Info.Error)
		}
	}
	if userTurnCount != 1 {
		t.Fatalf("expected exactly 1 user turn without retry duplication, got %d", userTurnCount)
	}

	// Refusal path: mutating turns (e.g. write/edit completed or outcome_unknown) must NOT be auto-rewritten by prepareFailedTurnRetryLocked.
	mutRec := SessionRecord{
		ID:      "ses_mut_refuse",
		Runtime: string(RuntimeNative),
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "u_mut", Role: "user"},
				Parts: []MessagePart{{ID: "p_u", Type: "text", Text: "update config"}},
			},
			{
				Info: MessageInfo{
					ID:   "a_mut",
					Role: "assistant",
					Error: &MessageError{
						Name: "StreamInterruptedError",
						Data: &MessageErrorData{Message: "EOF", Kind: ToolOutcomeUnknown},
					},
				},
				Parts: []MessagePart{{
					ID: "p_w", Type: "tool", Tool: "write", CallID: "c_w",
					State: &ToolState{Status: "completed", Input: map[string]any{"filePath": "a.txt"}},
				}},
			},
		},
	}
	resUser, clrAsst := prepareFailedTurnRetryLocked(&mutRec, true, true, true, "update config")
	if resUser != nil || clrAsst != nil || mutRec.Turns[1].Info.Error == nil {
		t.Fatalf("expected mutating/outcome_unknown turn to refuse in-place retry rewrite: resUser=%v clrAsst=%v err=%+v", resUser, clrAsst, mutRec.Turns[1].Info.Error)
	}
}

type MessageErrorError struct {
	msg string
}

func (e *MessageErrorError) Error() string { return e.msg }
