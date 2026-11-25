package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestRunnerPromotableForegroundCommandAdoptsIntoSessionBgProcsOnSteer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell timing test")
	}
	restore := SetRunnerSteerPromoteGraceForTest(35 * time.Millisecond)
	defer restore()

	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ws, nil, nil, filepath.Join(root, "sessions.json"))
	defer svc.CloseAll()

	rec := SessionRecord{
		ID:           "ses_runner_promote_1",
		Runtime:      string(RuntimeNative),
		ProviderID:   "anthropic",
		ModelID:      "claude-sonnet-4-5",
		ProjectDir:   ws,
		WorktreePath: ws,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	steerQ := &nativeSteerQueue{}
	ap := &activeProcess{
		sessionID:  rec.ID,
		runtime:    string(RuntimeNative),
		done:       make(chan struct{}),
		abortCh:    make(chan struct{}),
		steerQueue: steerQ,
	}
	svc.mu.Lock()
	svc.procs[rec.ID] = ap
	svc.mu.Unlock()
	defer func() {
		svc.mu.Lock()
		delete(svc.procs, rec.ID)
		svc.mu.Unlock()
	}()

	runFn := svc.nativePromotableStreamCommandRunner(rec, ap)
	bgMgr := svc.nativeBackgroundManager(rec)

	// 1. Fast command completes normally within the 35ms grace window even if steer is already queued.
	steerQ.Push("steer-fast")
	var captured strings.Builder
	outFast, codeFast, errFast := runFn(context.Background(), ws, "printf 'fast-runner-ok'", &captured, nil)
	if captured.String() != "fast-runner-ok" || errFast != nil || codeFast != 0 || outFast != "fast-runner-ok" {
		t.Fatalf("expected fast command to finish cleanly without promotion, got out=%q code=%d err=%v", outFast, codeFast, errFast)
	}
	_ = steerQ.DrainItems()

	// 2. Long command promotes cleanly to bg_1 when steerNativeTurn pushes a steer mid-flight.
	go func() {
		time.Sleep(25 * time.Millisecond)
		if !svc.steerNativeTurn(rec.ID, "please inspect tests first while build continues") {
			t.Errorf("steerNativeTurn returned false")
		}
	}()

	start := time.Now()
	_, _, promErr := runFn(context.Background(), ws, "printf 'runner-before\\n'; sleep 0.30; printf 'runner-after\\n'", nil, nil)
	elapsed := time.Since(start)
	var bgPromErr *NativeBackgroundPromotedError
	if !errors.As(promErr, &bgPromErr) || bgPromErr == nil {
		t.Fatalf("expected NativeBackgroundPromotedError, got %v", promErr)
	}
	if elapsed >= 260*time.Millisecond {
		t.Fatalf("expected foreground runner to promote promptly (~60ms), took %v", elapsed)
	}
	if bgPromErr.Proc.ProcID != "bg_1" || !bgPromErr.Proc.Running || bgPromErr.Proc.PID <= 1 {
		t.Fatalf("unexpected promoted background proc: %+v", bgPromErr.Proc)
	}
	if ap.activeProcess() != nil {
		t.Fatal("expected foreground ap.cmd pointer to be cleared once promoted to session bgProcs")
	}

	// Session permission hook must recognize promoted bg_1 as owned by this session.
	hook := svc.nativePermissionHook(rec, "msg_asst_1")
	pollInput, _ := json.Marshal(map[string]any{"action": "output", "procId": bgPromErr.Proc.ProcID})
	if err := hook(context.Background(), NativePermissionRequest{
		SessionID: rec.ID,
		CallID:    "call_poll_promoted",
		ToolName:  "bash",
		Input:     pollInput,
	}); err != nil {
		t.Fatalf("expected polling promoted procId to be permitted: %v", err)
	}

	// Poll until completion and verify both pre-promotion and post-promotion output are preserved.
	var afterOut strings.Builder
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, err := bgMgr.Output(context.Background(), bgPromErr.Proc.ProcID, 250*time.Millisecond)
		if err != nil {
			t.Fatalf("bgMgr.Output failed: %v", err)
		}
		afterOut.WriteString(st.Output)
		if !st.Running {
			if st.ExitCode != 0 {
				t.Fatalf("expected promoted process exit code 0, got %d", st.ExitCode)
			}
			break
		}
	}
	combined := bgPromErr.Proc.Output + "\n" + afterOut.String()
	if !strings.Contains(combined, "runner-before") || !strings.Contains(combined, "runner-after") {
		t.Fatalf("expected complete output across promotion boundary, got:\n%s", combined)
	}
}

func TestRunnerThinkingSignatureErrorRecognizesEncryptedContent(t *testing.T) {
	if !isThinkingSignatureErrorText("HTTP 400: invalid encrypted_content signature for reasoning item") {
		t.Fatal("expected encrypted_content error text to trigger signature reset on retry")
	}
}

func TestRunnerPromotableForegroundCommandPromotesOnTimeoutCauseAndKillsOnCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell timing test")
	}
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ws, nil, nil, filepath.Join(root, "sessions.json"))
	defer svc.CloseAll()

	rec := SessionRecord{
		ID:           "ses_runner_timeout_promote",
		Runtime:      string(RuntimeNative),
		ProviderID:   "openai",
		ModelID:      "gpt-5",
		ProjectDir:   ws,
		WorktreePath: ws,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	ap := &activeProcess{
		sessionID:  rec.ID,
		runtime:    string(RuntimeNative),
		done:       make(chan struct{}),
		abortCh:    make(chan struct{}),
		steerQueue: &nativeSteerQueue{},
	}
	runFn := svc.nativePromotableStreamCommandRunner(rec, ap)
	bgMgr := svc.nativeBackgroundManager(rec)

	// 1. Timeout caused by llm.ErrForegroundCommandTimeout promotes instead of killing.
	tCtx, tCancel := context.WithTimeoutCause(context.Background(), 60*time.Millisecond, llm.ErrForegroundCommandTimeout)
	defer tCancel()
	_, _, err := runFn(tCtx, ws, "printf 'rt-before\\n'; sleep 0.25; printf 'rt-after\\n'", nil, nil)
	var promErr *NativeBackgroundPromotedError
	if !errors.As(err, &promErr) || promErr == nil || promErr.Reason != "timeout" {
		t.Fatalf("expected timeout NativeBackgroundPromotedError, got %v", err)
	}
	var afterOut strings.Builder
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, oErr := bgMgr.Output(context.Background(), promErr.Proc.ProcID, 200*time.Millisecond)
		if oErr != nil {
			t.Fatalf("bgMgr.Output failed: %v", oErr)
		}
		afterOut.WriteString(st.Output)
		if !st.Running {
			break
		}
	}
	if full := promErr.Proc.Output + "\n" + afterOut.String(); !strings.Contains(full, "rt-before") || !strings.Contains(full, "rt-after") {
		t.Fatalf("expected full pre/post output across runner timeout promotion, got:\n%s", full)
	}

	// 2. Rejection path: explicit context.Canceled must kill, NOT promote.
	cCtx, cCancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cCancel()
	}()
	_, _, cancelErr := runFn(cCtx, ws, "sleep 5", nil, nil)
	var cancelProm *NativeBackgroundPromotedError
	if cancelErr == nil || errors.As(cancelErr, &cancelProm) {
		t.Fatalf("expected explicit cancel to kill command instead of promoting, got %v", cancelErr)
	}
}
