package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
)

const agySubParent = "ses_agy_subparent"

func newAgySubagentHookService(t *testing.T) (*Service, string) {
	t.Helper()
	t.Setenv("GEMINI_HOME", filepath.Join(t.TempDir(), ".gemini"))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(dir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeSmart})
	svc.gate.mu.Lock()
	svc.gate.subagentWait = 50 * time.Millisecond
	svc.gate.mu.Unlock()
	put := func(rec SessionRecord) {
		rec.Runtime, rec.ProjectDir, rec.WorktreePath = "antigravity", dir, dir
		if err := svc.store.Put(rec); err != nil {
			t.Fatal(err)
		}
	}
	put(SessionRecord{ID: agySubParent, ExternalID: "conv-parent"})
	put(SessionRecord{ID: "ses_agy_subchild", ParentID: agySubParent, Subagent: true, ExternalID: "conv-child"})
	put(SessionRecord{ID: "ses_agy_otherparent", ExternalID: "conv-other-parent"})
	put(SessionRecord{ID: "ses_agy_otherchild", ParentID: "ses_agy_otherparent", Subagent: true, ExternalID: "conv-other-child"})
	// Linked by ParentID but not a subagent: must not widen the session.
	put(SessionRecord{ID: "ses_agy_notsub", ParentID: agySubParent, ExternalID: "conv-not-subagent"})
	return svc, dir
}

func agySubagentHookRead(svc *Service, r *http.Request, convID string, step int, path string) (string, string) {
	var p hookPayload
	p.ConversationID = convID
	p.StepIdx = step
	p.ToolCall.Name = "view_file"
	p.ToolCall.Args = map[string]interface{}{"AbsolutePath": path}
	cred := hookCredential{runtime: string(RuntimeAntigravity), sessionID: agySubParent, messageID: "msg_a_sub"}
	return svc.decideHookCall(r, cred, p)
}

func TestAgyHookAllowsOwnSubagentConversation(t *testing.T) {
	svc, dir := newAgySubagentHookService(t)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	for _, conv := range []string{"conv-parent", "conv-child", ""} {
		if d, reason := agySubagentHookRead(svc, req, conv, 1, filepath.Join(dir, "main.go")); d != hookDecisionApprove {
			t.Fatalf("conversation %q: got %q (%q), want auto_approve", conv, d, reason)
		}
	}
}

func TestAgyHookDeniesConversationsOutsideTheSession(t *testing.T) {
	svc, dir := newAgySubagentHookService(t)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	for _, conv := range []string{"conv-unknown", "conv-other-parent", "conv-other-child", "conv-not-subagent"} {
		d, reason := agySubagentHookRead(svc, req, conv, 1, filepath.Join(dir, "main.go"))
		if d != hookDecisionDeny || !strings.Contains(reason, "does not belong to this Hoxi session") {
			t.Fatalf("conversation %q: got %q (%q), want deny", conv, d, reason)
		}
	}
}

func TestAgyHookWaitsForLateSubagentChildRecord(t *testing.T) {
	svc, dir := newAgySubagentHookService(t)
	svc.gate.mu.Lock()
	svc.gate.subagentWait = 5 * time.Second
	svc.gate.mu.Unlock()
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = svc.store.Put(SessionRecord{ID: "ses_agy_latechild", Runtime: "antigravity", ProjectDir: dir, WorktreePath: dir,
			ParentID: agySubParent, Subagent: true, ExternalID: "conv-late"})
	}()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if d, reason := agySubagentHookRead(svc, req, "conv-late", 1, filepath.Join(dir, "main.go")); d != hookDecisionApprove {
		t.Fatalf("late child: got %q (%q), want auto_approve", d, reason)
	}
}

func TestAgyHookSubagentWaitStopsWhenHookIsInterrupted(t *testing.T) {
	svc, dir := newAgySubagentHookService(t)
	svc.gate.mu.Lock()
	svc.gate.subagentWait = time.Minute
	svc.gate.mu.Unlock()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if d, _ := agySubagentHookRead(svc, req.WithContext(ctx), "conv-never", 1, filepath.Join(dir, "main.go")); d != hookDecisionDeny {
		t.Fatalf("interrupted wait: got %q, want deny", d)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("interrupted wait took %v", elapsed)
	}
}

func TestAgyHookSubagentCallIDsDoNotCollideWithParent(t *testing.T) {
	t.Parallel()
	parent := agyHookOrigin{convID: "conv-parent"}
	child := agyHookOrigin{convID: "conv-child", childID: "ses_agy_subchild"}
	if parent.callID("msg_a_1", 4) != "call_msg_a_1_4" {
		t.Fatalf("parent call ID changed: %q", parent.callID("msg_a_1", 4))
	}
	if child.callID("msg_a_1", 4) == parent.callID("msg_a_1", 4) {
		t.Fatal("subagent call ID collides with the parent's at the same step")
	}
	if parent.guardKey(agySubParent) != agySubParent || child.guardKey(agySubParent) != "ses_agy_subchild" {
		t.Fatal("unexpected doom-loop guard keys")
	}
}

func TestAgyHookParallelSubagentsDoNotTripEachOthersDoomLoop(t *testing.T) {
	svc, dir := newAgySubagentHookService(t)
	if err := svc.store.Put(SessionRecord{ID: "ses_agy_subchild2", Runtime: "antigravity", ProjectDir: dir, WorktreePath: dir,
		ParentID: agySubParent, Subagent: true, ExternalID: "conv-child-2"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	path := filepath.Join(dir, "main.go")
	// Each conversation reads the same file once: three identical calls in a
	// row for the turn, but none of them repeats within its own stream.
	for i, conv := range []string{"conv-parent", "conv-child", "conv-child-2"} {
		if d, reason := agySubagentHookRead(svc, req, conv, i+1, path); d != hookDecisionApprove {
			t.Fatalf("conversation %q: got %q (%q), want auto_approve", conv, d, reason)
		}
	}
}
