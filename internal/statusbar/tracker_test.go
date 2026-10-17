package statusbar

import "testing"

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

	// 5. Permission and question replied -> Attention = 0
	tracker.InspectSSELine(`data: {"type":"permission.replied","properties":{"requestID":"perm_1"}}`)
	tracker.InspectSSELine(`data: {"type":"question.rejected","properties":{"requestID":"q_1"}}`)
	if snap := tracker.Snapshot(); snap.Attention != 0 {
		t.Fatalf("expected Attention=0 after replies, got %+v", snap)
	}

	// 6. Sessions become idle / error -> Running = 0
	tracker.InspectSSELine(`data: {"type":"session.idle","properties":{"sessionID":"ses_1"}}`)
	tracker.InspectSSELine(`data: {"type":"session.status","properties":{"sessionID":"ses_2","status":{"type":"idle"}}}`)
	if snap := tracker.Snapshot(); snap.Running != 0 {
		t.Fatalf("expected Running=0 after idle events, got %+v", snap)
	}
}
