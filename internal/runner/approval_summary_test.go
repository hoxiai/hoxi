package runner

import (
	"strings"
	"testing"
)

func TestFinishTurnSummaryCountsOnly(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, "")
	var got []string
	svc.SetSummaryNotifier(func(_, body string) { got = append(got, body) })
	parent := "ses_agy_parent"
	child := "ses_agy_child"
	if err := svc.store.Put(SessionRecord{ID: parent, Runtime: "antigravity"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.Put(SessionRecord{ID: child, Runtime: "antigravity", ParentID: parent, Subagent: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.policy.set(ApprovalPolicy{Mode: ApprovalModeUnattended, ApprovalTimeoutSeconds: 600}); err != nil {
		t.Fatal(err)
	}

	svc.noteTurn(parent, "allow")
	svc.noteTurn(parent, "deny")
	svc.finishTurnSummary(parent, "session.idle")
	svc.finishTurnSummary(child, "session.idle")
	if len(got) != 1 {
		t.Fatalf("summaries = %v, want exactly one for the parent", got)
	}
	if strings.Contains(got[0], "git ") || strings.Contains(got[0], "/") {
		t.Fatalf("summary leaked detail: %q", got[0])
	}

	if _, err := svc.policy.set(ApprovalPolicy{Mode: ApprovalModeSmart, ApprovalTimeoutSeconds: 600}); err != nil {
		t.Fatal(err)
	}
	svc.finishTurnSummary(parent, "session.idle")
	if len(got) != 1 {
		t.Fatal("a turn with nothing to report must stay silent")
	}
	svc.noteTurn(parent, "timeout")
	svc.finishTurnSummary(parent, "session.error")
	if len(got) != 2 {
		t.Fatal("a timed-out turn must still summarize outside unattended mode")
	}
}
