package statusbar

import (
	"context"
	"testing"
)

func TestStatusTrackerInspectSSELine(t *testing.T) {
	tracker := NewTracker()

	if snap := tracker.Snapshot(); !snap.Available || snap.Running != 0 || snap.Attention != 0 {
		t.Fatalf("unexpected initial snapshot: %+v", snap)
	}

	// 1. Session becomes busy -> Running = 1
	tracker.InspectSSELine(`data: {"type":"session.status","properties":{"sessionID":"ses_1","status":{"type":"busy"}}}`)
	if snap := tracker.Snapshot(); snap.Running != 1 {
		t.Fatalf("expected Running=1 after busy status, got %+v", snap)
	}

	// 2. Wrapped payload format (GlobalEvent envelope) for second session -> Running = 2
	tracker.InspectSSELine(`data: {"directory":"/tmp","payload":{"type":"session.status","properties":{"sessionID":"ses_2","status":{"type":"retry"}}}}`)
	if snap := tracker.Snapshot(); snap.Running != 2 {
		t.Fatalf("expected Running=2 after retry status, got %+v", snap)
	}

	// 3. Permission asked -> Attention = 1
	tracker.InspectSSELine(`data: {"type":"permission.asked","properties":{"id":"perm_1","sessionID":"ses_1"}}`)
	if snap := tracker.Snapshot(); snap.Attention != 1 {
		t.Fatalf("expected Attention=1 after permission.asked, got %+v", snap)
	}

	// 4. Question asked -> Attention = 2
	tracker.InspectSSELine(`data: {"type":"question.asked","properties":{"id":"q_1","sessionID":"ses_2"}}`)
	if snap := tracker.Snapshot(); snap.Attention != 2 {
		t.Fatalf("expected Attention=2 after question.asked, got %+v", snap)
	}

	// 5. Permission replied and question replied/rejected -> Attention = 0
	tracker.InspectSSELine(`data: {"type":"permission.replied","properties":{"requestID":"perm_1"}}`)
	tracker.InspectSSELine(`data: {"type":"question.replied","properties":{"requestID":"q_1"}}`)
	if snap := tracker.Snapshot(); snap.Attention != 0 {
		t.Fatalf("expected Attention=0 after replies, got %+v", snap)
	}

	// Ask and reject another question to verify question.rejected path
	tracker.InspectSSELine(`data: {"type":"question.asked","properties":{"id":"q_2","sessionID":"ses_2"}}`)
	tracker.InspectSSELine(`data: {"type":"question.rejected","properties":{"requestID":"q_2"}}`)
	if snap := tracker.Snapshot(); snap.Attention != 0 {
		t.Fatalf("expected Attention=0 after question.rejected, got %+v", snap)
	}

	// 6. Sessions become idle / error -> Running = 0
	tracker.InspectSSELine(`data: {"type":"session.idle","properties":{"sessionID":"ses_1"}}`)
	tracker.InspectSSELine(`data: {"type":"session.error","properties":{"sessionID":"ses_2"}}`)
	if snap := tracker.Snapshot(); snap.Running != 0 {
		t.Fatalf("expected Running=0 after idle and error events, got %+v", snap)
	}

	// Re-mark ses_1 busy then transition via session.status idle
	tracker.InspectSSELine(`data: {"type":"session.status","properties":{"sessionID":"ses_1","status":{"type":"busy"}}}`)
	tracker.InspectSSELine(`data: {"type":"session.status","properties":{"sessionID":"ses_1","status":{"type":"idle"}}}`)
	if snap := tracker.Snapshot(); snap.Running != 0 {
		t.Fatalf("expected Running=0 after session.status idle, got %+v", snap)
	}
}

func TestStatusTrackerFiltersAndAvailability(t *testing.T) {
	Start(context.Background())
	defer Stop()

	tracker := NewTracker()

	// Toggle availability false and true
	tracker.SetAvailable(false)
	if snap := tracker.Snapshot(); snap.Available {
		t.Fatalf("expected Available=false, got %+v", snap)
	}
	Update(tracker.Snapshot())

	tracker.SetAvailable(true)
	if snap := tracker.Snapshot(); !snap.Available {
		t.Fatalf("expected Available=true, got %+v", snap)
	}
	Update(tracker.Snapshot())

	// Non-data SSE lines, empty data lines, and non-JSON payloads should be ignored
	tracker.InspectSSELine("")
	tracker.InspectSSELine(": ping")
	tracker.InspectSSELine("event: message")
	tracker.InspectSSELine("data:   ")
	tracker.InspectSSELine("data: [1,2,3]")

	// High-frequency token deltas and unrelated events should be skipped by fast pre-filter
	tracker.InspectSSELine(`data: {"type":"message.part.delta","properties":{"delta":"hello"}}`)
	tracker.InspectSSELine(`data: {"type":"message.part.updated","properties":{"part":{"type":"text"}}}`)

	// Malformed JSON that passes pre-filter should be safely ignored
	tracker.InspectSSELine(`data: {"type":"session.status","properties":{broken`)

	if snap := tracker.Snapshot(); snap.Running != 0 || snap.Attention != 0 {
		t.Fatalf("expected no state change from filtered/malformed lines, got %+v", snap)
	}

	// Stop clears context; exported callbacks should safely no-op when context is nil
	Stop()
	HoxiShowMainWindow()
	HoxiQuitApplication()
}

func TestStatusTrackerStalledSessionsRaiseAttention(t *testing.T) {
	tracker := NewTracker()

	// A stall reported after the turn ended must not leave Attention stuck.
	tracker.SetSessionStalled("ses_late", true)
	if snap := tracker.Snapshot(); snap.Attention != 0 {
		t.Fatalf("stall of an idle session raised Attention: %+v", snap)
	}

	tracker.SetSessionBusy("ses_1", true)
	tracker.SetSessionStalled("ses_1", true)
	if snap := tracker.Snapshot(); snap.Attention != 1 || snap.Running != 1 {
		t.Fatalf("expected Attention=1 Running=1 for a stalled busy session, got %+v", snap)
	}
	tracker.SetSessionStalled("ses_1", false)
	if snap := tracker.Snapshot(); snap.Attention != 0 {
		t.Fatalf("cleared stall kept Attention: %+v", snap)
	}

	tracker.SetSessionStalled("ses_1", true)
	tracker.SetSessionBusy("ses_1", false)
	if snap := tracker.Snapshot(); snap.Attention != 0 || snap.Running != 0 {
		t.Fatalf("idle session kept its stall: %+v", snap)
	}
}

func TestStatusTrackerKeepAwakeLifecycle(t *testing.T) {
	cfgPath := t.TempDir() + "/keep-awake.json"
	t.Setenv("HOXI_KEEP_AWAKE_CONFIG", cfgPath)

	tracker := NewTracker()
	starts := 0
	stops := 0
	tracker.startKeepAwakeHook = func() (func(), error) {
		starts++
		return func() { stops++ }, nil
	}

	if !tracker.KeepAwakeEnabled() || tracker.KeepAwakeActive() {
		t.Fatalf("expected initial KeepAwakeEnabled=true KeepAwakeActive=false")
	}

	// Busy session activates keep-awake
	tracker.SetSessionBusy("ses_awake", true)
	if !tracker.KeepAwakeActive() || starts != 1 || stops != 0 {
		t.Fatalf("expected keep-awake active after busy session: active=%v starts=%d stops=%d", tracker.KeepAwakeActive(), starts, stops)
	}

	// Disabling keep-awake while busy stops it and persists setting
	tracker.SetKeepAwakeEnabled(false)
	if tracker.KeepAwakeEnabled() || tracker.KeepAwakeActive() || stops != 1 {
		t.Fatalf("expected keep-awake stopped when disabled: enabled=%v active=%v stops=%d", tracker.KeepAwakeEnabled(), tracker.KeepAwakeActive(), stops)
	}

	// Re-enabling while still busy restarts keep-awake
	tracker.SetKeepAwakeEnabled(true)
	if !tracker.KeepAwakeEnabled() || !tracker.KeepAwakeActive() || starts != 2 {
		t.Fatalf("expected keep-awake restarted when re-enabled: enabled=%v active=%v starts=%d", tracker.KeepAwakeEnabled(), tracker.KeepAwakeActive(), starts)
	}

	// Session idle stops keep-awake
	tracker.SetSessionBusy("ses_awake", false)
	if tracker.KeepAwakeActive() || stops != 2 {
		t.Fatalf("expected keep-awake stopped when session idle: active=%v stops=%d", tracker.KeepAwakeActive(), stops)
	}

	// Verify persistence across NewTracker
	tracker.SetKeepAwakeEnabled(false)
	reloaded := NewTracker()
	if reloaded.KeepAwakeEnabled() {
		t.Fatalf("expected persisted KeepAwakeEnabled=false on reload")
	}
}

func TestStatusTrackerTargetsAndMenuDispatch(t *testing.T) {
	tracker := NewTracker()
	tracker.SetSessionResolver(func(sessionID string) (string, string, string) {
		switch sessionID {
		case "ses_child":
			return "ses_root", "Fix auth callback", "/Users/dev/hoxi/.hoxi/worktrees/ses_root"
		case "ses_busy":
			return "ses_busy", "Run integration suite", "/Users/dev/backend"
		default:
			return sessionID, "", ""
		}
	})

	tracker.SetPendingApproval("perm_1", "ses_child", true)
	tracker.SetSessionBusy("ses_busy", true)

	snap := tracker.Snapshot()
	if snap.PendingApprovals != 1 {
		t.Fatalf("PendingApprovals = %d, want 1", snap.PendingApprovals)
	}
	if len(snap.PendingTargets) != 1 || snap.PendingTargets[0].SessionID != "ses_root" || snap.PendingTargets[0].Directory != "/Users/dev/hoxi/.hoxi/worktrees/ses_root" {
		t.Fatalf("unexpected PendingTargets: %+v", snap.PendingTargets)
	}
	if got := FormatMenuTargetLabel(snap.PendingTargets[0]); got != "  ↳ Fix auth callback · hoxi" {
		t.Fatalf("FormatMenuTargetLabel = %q, want %q", got, "  ↳ Fix auth callback · hoxi")
	}
	if len(snap.RunningTargets) != 1 || snap.RunningTargets[0].SessionID != "ses_busy" || snap.RunningTargets[0].Directory != "/Users/dev/backend" {
		t.Fatalf("unexpected RunningTargets: %+v", snap.RunningTargets)
	}

	att, ok := tracker.FirstAttentionTarget()
	if !ok || att.SessionID != "ses_root" {
		t.Fatalf("FirstAttentionTarget = %+v (ok=%v), want ses_root", att, ok)
	}
	run, ok := tracker.FirstRunningTarget()
	if !ok || run.SessionID != "ses_busy" {
		t.Fatalf("FirstRunningTarget = %+v (ok=%v), want ses_busy", run, ok)
	}

	var received MenuAction
	SetActionHandler(func(action MenuAction) {
		received = action
	})
	defer SetActionHandler(nil)

	dispatchMenuAction("attention", "ses_root")
	if received.Kind != MenuActionAttention || received.SessionID != "ses_root" {
		t.Fatalf("unexpected dispatched menu action: %+v", received)
	}
}
