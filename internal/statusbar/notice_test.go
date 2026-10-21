package statusbar

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func collectNotices(t *testing.T, tracker *Tracker) chan ApprovalNotice {
	t.Helper()
	notices := make(chan ApprovalNotice, 8)
	tracker.SetApprovalListener(func(n ApprovalNotice) { notices <- n })
	return notices
}

func nextNotice(t *testing.T, notices chan ApprovalNotice) ApprovalNotice {
	t.Helper()
	select {
	case n := <-notices:
		return n
	case <-time.After(2 * time.Second):
		t.Fatal("expected an approval notice")
		return ApprovalNotice{}
	}
}

func TestApprovalNoticesFireOnceWithoutCommandText(t *testing.T) {
	tracker := NewTracker()
	notices := collectNotices(t, tracker)
	line := `data: {"type":"permission.asked","properties":{"id":"perm_1","sessionID":"ses_1","permission":"bash","patterns":["rm -rf build"],"metadata":{"command":"rm -rf build"}}}`
	tracker.InspectSSELine(line)
	// The same event seen by a second SSE subscriber must not announce again.
	tracker.InspectSSELine(line)

	n := nextNotice(t, notices)
	if n.Kind != "approval" || n.RequestID != "perm_1" || n.SessionID != "ses_1" || n.Permission != "bash" || n.Pending != 1 {
		t.Fatalf("notice = %+v", n)
	}
	raw, _ := json.Marshal(n)
	if strings.Contains(string(raw), "rm -rf") {
		t.Fatalf("notice leaked command text: %s", raw)
	}
	select {
	case dup := <-notices:
		t.Fatalf("duplicate notice: %+v", dup)
	case <-time.After(100 * time.Millisecond):
	}

	tracker.InspectSSELine(`data: {"type":"question.asked","properties":{"id":"q_1","sessionID":"ses_1"}}`)
	if q := nextNotice(t, notices); q.Kind != "question" || q.Pending != 2 {
		t.Fatalf("question notice = %+v", q)
	}
}

func TestRunnerApprovalsAnnounceAndReannounceAfterReply(t *testing.T) {
	tracker := NewTracker()
	notices := collectNotices(t, tracker)
	tracker.SetPendingApproval("per_rnr_1", "ses_2", true)
	if n := nextNotice(t, notices); n.RequestID != "per_rnr_1" || n.Kind != "approval" {
		t.Fatalf("notice = %+v", n)
	}
	tracker.SetPendingApproval("per_rnr_1", "ses_2", false)
	tracker.SetPendingApproval("per_rnr_2", "ses_2", true)
	if n := nextNotice(t, notices); n.RequestID != "per_rnr_2" || n.Pending != 1 {
		t.Fatalf("notice = %+v", n)
	}
}
