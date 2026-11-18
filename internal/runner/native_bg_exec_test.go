package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNativeBackgroundManagerSessionIsolationApprovalAndDeleteReap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group liveness probe test")
	}
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ws, nil, nil, filepath.Join(root, "sessions.json"))
	defer svc.CloseAll()

	recA := SessionRecord{
		ID:           "ses_bg_runner_a",
		Runtime:      string(RuntimeNative),
		ProviderID:   "openai",
		ModelID:      "gpt-5",
		ProjectDir:   ws,
		WorktreePath: ws,
	}
	recB := SessionRecord{
		ID:           "ses_bg_runner_b",
		Runtime:      string(RuntimeNative),
		ProviderID:   "openai",
		ModelID:      "gpt-5",
		ProjectDir:   ws,
		WorktreePath: ws,
	}
	if err := svc.store.Put(recA); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.Put(recB); err != nil {
		t.Fatal(err)
	}

	mgrA := svc.nativeBackgroundManager(recA)
	mgrB := svc.nativeBackgroundManager(recB)

	st, err := mgrA.Start(context.Background(), ws, "printf 'BOOT_A\\n'; sleep 30")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !st.Running || st.PID <= 1 || st.ProcID != "bg_1" {
		t.Fatalf("unexpected started status: %+v", st)
	}
	if !strings.Contains(st.Output, "BOOT_A") {
		t.Fatalf("expected startup output BOOT_A, got: %q", st.Output)
	}

	// Session B must NOT be able to read or stop Session A's background process.
	if _, err := mgrB.Output(context.Background(), st.ProcID, 0); err == nil {
		t.Fatal("expected cross-session Output to fail")
	}
	if _, err := mgrB.Stop(context.Background(), st.ProcID); err == nil {
		t.Fatal("expected cross-session Stop to fail")
	}

	// Approval policy check in Smart mode:
	// 1. Stopping Session A's own bg_1 is auto-approved with source=own_process.
	// 2. Stopping an unknown/foreign procId asks (fails when context cancelled).
	// 3. Destructive run_in_background command (reading .env) is NEVER auto-approved.
	hookA := svc.nativePermissionHook(recA, "msg_asst_a")
	stopOwnJSON, _ := json.Marshal(map[string]any{"action": "stop", "procId": st.ProcID})
	if err := hookA(context.Background(), NativePermissionRequest{
		SessionID: recA.ID,
		CallID:    "call_stop_own",
		ToolName:  "bash",
		Input:     stopOwnJSON,
	}); err != nil {
		t.Fatalf("expected stopping own background proc in smart mode to succeed without prompt, got: %v", err)
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	stopForeignJSON, _ := json.Marshal(map[string]any{"action": "stop", "procId": "bg_999"})
	if err := hookA(cancelCtx, NativePermissionRequest{
		SessionID: recA.ID,
		CallID:    "call_stop_foreign",
		ToolName:  "bash",
		Input:     stopForeignJSON,
	}); err == nil {
		t.Fatal("expected stopping unowned procId to require explicit approval")
	}

	destructiveBgJSON, _ := json.Marshal(map[string]any{"command": "cat .env", "run_in_background": true})
	if err := hookA(cancelCtx, NativePermissionRequest{
		SessionID: recA.ID,
		CallID:    "call_bg_secret",
		ToolName:  "bash",
		Input:     destructiveBgJSON,
	}); err == nil {
		t.Fatal("expected sensitive command with run_in_background=true to require approval")
	}

	// Also create a session-scoped .tsbuildinfo cache entry and verify DeleteConversation cleans BOTH
	// the live background process tree AND the session's .tsbuildinfo file.
	buildInfo := nativeTSCBuildInfoPath(recA.ID, filepath.Join(ws, "tsconfig.json"))
	if err := os.WriteFile(buildInfo, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := svc.DeleteConversation(context.Background(), recA.ID); err != nil {
		t.Fatalf("DeleteConversation failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(st.PID) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if processAlive(st.PID) {
		t.Fatalf("expected background process PID %d to be terminated on DeleteConversation", st.PID)
	}
	if _, err := os.Stat(buildInfo); !os.IsNotExist(err) {
		t.Fatalf("expected session .tsbuildinfo %s to be removed on DeleteConversation, err=%v", buildInfo, err)
	}

	// Verify detached background subshell (`trap '' INT TERM; ... &`) whose parent /bin/sh already exited
	// and closed stdout is still tracked as live and cleanly killed via process-group SIGKILL escalation on Stop().
	pidFile := filepath.Join(ws, "detached.pid")
	stDetached, err := mgrB.Start(context.Background(), ws, "sh -c 'trap \"\" INT TERM; sleep 30 >/dev/null 2>&1' & child=$!; echo $child > "+pidFile)
	if err != nil {
		t.Fatalf("detached Start failed: %v", err)
	}
	var detachedPID int
	for range 20 {
		if raw, rErr := os.ReadFile(pidFile); rErr == nil && strings.TrimSpace(string(raw)) != "" {
			_, _ = fmtSscanfInt(strings.TrimSpace(string(raw)), &detachedPID)
			if detachedPID > 1 {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if detachedPID <= 1 {
		t.Fatalf("failed to read detached child PID from %s", pidFile)
	}
	if _, err := mgrB.Stop(context.Background(), stDetached.ProcID); err != nil {
		t.Fatalf("Stop detached failed: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(detachedPID) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if processAlive(detachedPID) {
		killPIDs([]int{detachedPID})
		t.Fatalf("expected detached SIGINT-ignoring child PID %d to be killed by Stop()", detachedPID)
	}

	// Regression 1: repeated bash action="output" / "list" polling in the same turn must NOT trip doom_loop.
	hookB := svc.nativePermissionHook(recB, "msg_asst_b")
	for i := range 5 {
		pollCall := json.RawMessage(`{"action":"output","procId":"` + stDetached.ProcID + `","waitMs":10}`)
		if err := hookB(context.Background(), NativePermissionRequest{
			SessionID: recB.ID, CallID: "c_poll_loop", ToolName: "bash", Input: pollCall,
		}); err != nil {
			t.Fatalf("poll #%d unexpectedly blocked by permission/doom_loop: %v", i+1, err)
		}
		listCall := json.RawMessage(`{"action":"list"}`)
		if err := hookB(context.Background(), NativePermissionRequest{
			SessionID: recB.ID, CallID: "c_list_loop", ToolName: "bash", Input: listCall,
		}); err != nil {
			t.Fatalf("list #%d unexpectedly blocked by permission/doom_loop: %v", i+1, err)
		}
	}

	// Regression 2: command="ls" (even with session always-grant) must NOT shadow action="stop" gate,
	// and non-string action values must be rejected immediately.
	svc.gate.addOpenCodeAlwaysGrants(recB.ID, "bash", []string{"ls"}, "desktop")
	ctxShadow, cancelShadow := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancelShadow()
	shadowStop := json.RawMessage(`{"action":"stop","procId":"bg_unowned_99","command":"ls"}`)
	if err := hookB(ctxShadow, NativePermissionRequest{
		SessionID: recB.ID, CallID: "c_shadow_stop", ToolName: "bash", Input: shadowStop,
	}); err == nil {
		t.Fatalf("expected action=stop on unowned procId with command=ls to still require explicit approval, got nil")
	}
	badTypeAct := json.RawMessage(`{"action":["stop"],"command":"ls"}`)
	if err := hookB(context.Background(), NativePermissionRequest{
		SessionID: recB.ID, CallID: "c_bad_act_type", ToolName: "bash", Input: badTypeAct,
	}); err == nil || !strings.Contains(err.Error(), "invalid bash action type") {
		t.Fatalf("expected malformed non-string bash action to be rejected, got: %v", err)
	}

	// Regression 3: non-zero shell exit after spawning a detached child must reap the child immediately.
	failPidFile := filepath.Join(ws, "fail_detached.pid")
	stFailDetached, err := mgrB.Start(context.Background(), ws, "sh -c 'trap \"\" INT TERM; sleep 30 >/dev/null 2>&1' & child=$!; echo $child > "+failPidFile+"; exit 1")
	if err != nil {
		t.Fatalf("Start failed-detached returned unexpected error: %v", err)
	}
	if stFailDetached.Running || stFailDetached.ExitCode != 1 {
		t.Fatalf("expected non-zero startup to settle exited with code 1, got running=%v code=%d", stFailDetached.Running, stFailDetached.ExitCode)
	}
	var failDetachedPID int
	if raw, rErr := os.ReadFile(failPidFile); rErr == nil && strings.TrimSpace(string(raw)) != "" {
		_, _ = fmtSscanfInt(strings.TrimSpace(string(raw)), &failDetachedPID)
	}
	if failDetachedPID <= 1 {
		t.Fatalf("failed to read detached child PID from %s", failPidFile)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(failDetachedPID) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if processAlive(failDetachedPID) {
		killPIDs([]int{failDetachedPID})
		t.Fatalf("expected detached child PID %d from failed startup to be reaped immediately", failDetachedPID)
	}
}

func fmtSscanfInt(s string, out *int) (int, error) {
	v := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		v = v*10 + int(r-'0')
	}
	*out = v
	return 1, nil
}

func TestNativeRustDiagnosticPrioritizesEditedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell stub test")
	}
	root := t.TempDir()
	ws := filepath.Join(root, "crate")
	srcDir := filepath.Join(ws, "src")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "Cargo.toml"), []byte("[package]\nname = \"demo\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rsFile := filepath.Join(srcDir, "lib.rs")
	if err := os.WriteFile(rsFile, []byte("pub fn broken() -> i32 { \"wrong\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cargoStub := filepath.Join(binDir, "cargo")
	stubScript := "#!/bin/sh\necho 'src/other.rs:4:1: warning: unused import'\necho 'src/lib.rs:1:26: error[E0308]: mismatched types'\nexit 101\n"
	if err := os.WriteFile(cargoStub, []byte(stubScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	svc := NewService(ws, nil, nil, filepath.Join(root, "sessions.json"))
	defer svc.CloseAll()
	rec := SessionRecord{
		ID:           "ses_rust_diag",
		Runtime:      string(RuntimeNative),
		WorktreePath: ws,
		ProjectDir:   ws,
	}
	diag := svc.nativeDiagnostics(rec)(context.Background(), ws, rsFile)
	if !strings.Contains(diag, "[cargo check]") || !strings.Contains(diag, "error[E0308]") {
		t.Fatalf("expected [cargo check] diagnostic with E0308, got: %q", diag)
	}
	lines := strings.Split(diag, "\n")
	if len(lines) < 2 || !strings.Contains(lines[1], "src/lib.rs") {
		t.Fatalf("expected edited file src/lib.rs diagnostic prioritized first, got:\n%s", diag)
	}
}
