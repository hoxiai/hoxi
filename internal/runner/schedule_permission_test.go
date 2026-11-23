package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
)

func TestNativeSchedulePermissionGate(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto})

	normalRec := SessionRecord{
		ID:           "ses_interactive",
		Runtime:      string(RuntimeNative),
		ProjectDir:   dir,
		WorktreePath: dir,
		SandboxMode:  "local",
	}
	if err := svc.store.Put(normalRec); err != nil {
		t.Fatal(err)
	}

	hook := svc.nativePermissionHook(normalRec, "msg_1")

	// 1. Read-only list succeeds without prompting in auto mode.
	if err := hook(context.Background(), NativePermissionRequest{
		SessionID: normalRec.ID,
		CallID:    "call_list",
		ToolName:  "hoxi_schedule",
		Input:     json.RawMessage(`{"action":"list"}`),
	}); err != nil {
		t.Fatalf("expected hoxi_schedule list to be allowed in auto mode, got: %v", err)
	}

	// 2. Scheduled session (rec.ScheduleID != "") hard-denies mutations.
	schedRec := SessionRecord{
		ID:           "ses_from_schedule",
		ScheduleID:   "sch_abc",
		Runtime:      string(RuntimeNative),
		ProjectDir:   dir,
		WorktreePath: dir,
		SandboxMode:  "local",
	}
	schedHook := svc.nativePermissionHook(schedRec, "msg_2")
	if err := schedHook(context.Background(), NativePermissionRequest{
		SessionID: schedRec.ID,
		CallID:    "call_rec_create",
		ToolName:  "hoxi_schedule",
		Input:     json.RawMessage(`{"action":"create","name":"Nested","intervalMinutes":10,"prompt":"loop"}`),
	}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "scheduled runs cannot") {
		t.Fatalf("expected recursion deny inside scheduled session, got: %v", err)
	}

	// 3. Bound session currently running a scheduled turn also hard-denies mutations.
	svc.SetScheduledTurnChecker(func(sid string) bool { return sid == normalRec.ID })
	if err := hook(context.Background(), NativePermissionRequest{
		SessionID: normalRec.ID,
		CallID:    "call_active_sched",
		ToolName:  "hoxi_schedule",
		Input:     json.RawMessage(`{"action":"create","name":"Nested","intervalMinutes":10,"prompt":"loop"}`),
	}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "scheduled runs cannot") {
		t.Fatalf("expected deny while scheduled turn is active on bound session, got: %v", err)
	}
	svc.SetScheduledTurnChecker(nil)

	// 4. Unattended mode hard-denies hoxi_schedule mutations.
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeUnattended})
	if err := hook(context.Background(), NativePermissionRequest{
		SessionID: normalRec.ID,
		CallID:    "call_unattended",
		ToolName:  "hoxi_schedule",
		Input:     json.RawMessage(`{"action":"create","name":"Nightly","cron":"0 9 * * *","prompt":"check"}`),
	}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "unattended") {
		t.Fatalf("expected unattended mode deny for hoxi_schedule mutation, got: %v", err)
	}

	// 5. Desktop one-time approval succeeds; remote reply and "always" reply return 403.
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	done := make(chan error, 1)
	go func() {
		done <- hook(context.Background(), NativePermissionRequest{
			SessionID: normalRec.ID,
			CallID:    "call_create_ok",
			ToolName:  "hoxi_schedule",
			Input:     json.RawMessage(`{"action":"create","name":"Daily PR","cron":"0 9 * * 1-5","prompt":"check PRs"}`),
		})
	}()

	var pendingID string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		svc.gate.mu.Lock()
		for id, pa := range svc.gate.pending {
			if pa.sid == normalRec.ID && permissionName(pa.req) == "hoxi_schedule" {
				pendingID = id
				break
			}
		}
		svc.gate.mu.Unlock()
		if pendingID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pendingID == "" {
		t.Fatal("expected pending hoxi_schedule approval request")
	}

	replyReq := func(body, source string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/"+pendingID+"/reply?directory="+dir, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(ApprovalSourceHeader, source)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := replyReq(`{"reply":"always"}`, "desktop"); code != http.StatusForbidden {
		t.Fatalf("expected 403 for 'always' on hoxi_schedule, got %d", code)
	}
	if code := replyReq(`{"reply":"once"}`, "tunnel"); code != http.StatusForbidden {
		t.Fatalf("expected 403 for remote tunnel reply on hoxi_schedule, got %d", code)
	}
	if code := replyReq(`{"reply":"once"}`, "desktop"); code != http.StatusOK {
		t.Fatalf("expected 200 for desktop one-time approval on hoxi_schedule, got %d", code)
	}
	if err := <-done; err != nil {
		t.Fatalf("expected hoxi_schedule hook to succeed after desktop once approval, got: %v", err)
	}
}
