package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubagentChildSessionsArePersistedRoutedAndCleanedUp(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions"))
	parent := SessionRecord{ID: "ses_agy_parent", Runtime: string(RuntimeAntigravity), ProjectDir: "/repo", WorktreePath: "/repo/.hoxi/worktrees/ses_agy_parent", Title: "Audit"}
	if err := svc.store.Put(parent); err != nil {
		t.Fatal(err)
	}
	userMsg := MessageInfo{ID: "msg_user", SessionID: parent.ID, Role: "user"}
	svc.store.UpsertTurnInfo(parent.ID, userMsg)

	tr := NewTurnTranslator(parent.ID, parent.Runtime, "msg_turn", userMsg.ID, "antigravity", "gemini-4-argon-high", "build", "")
	tr.lookupThink, tr.lookupStep = nil, nil
	tr.createChild = svc.childSessionFactory(parent, "gemini-4-argon-high", "build")
	svc.broadcastRouted(parent.ID, tr.InitialEvents(userMsg, nil))
	for _, line := range agyDispatchLines() {
		svc.broadcastRouted(parent.ID, tr.FeedLine(line))
	}

	ids := tr.ChildSessionIDs()
	if len(ids) != 2 {
		t.Fatalf("want two child sessions, got %v", ids)
	}
	child, ok := svc.store.Get(ids[0])
	if !ok || !child.Subagent || child.ParentID != parent.ID || child.WorktreePath != parent.WorktreePath || child.ExternalID != agyBugConv {
		t.Fatalf("child record not persisted as a subagent of the parent: %+v", child)
	}
	if len(child.Turns) != 2 || child.Turns[0].Parts[0].Text != "Audit backend" {
		t.Fatalf("child events must be routed into the child transcript, got %+v", child.Turns)
	}
	if busy := svc.conversationStatuses(context.Background(), "", ""); string(busy[ids[0]]) != `{"type":"busy"}` {
		t.Fatalf("running subagent must report busy, got %v", busy)
	}
	parentRec, _ := svc.store.Get(parent.ID)
	for _, turn := range parentRec.Turns {
		for _, p := range turn.Parts {
			if p.SessionID != parent.ID {
				t.Fatalf("child part leaked into the parent transcript: %+v", p)
			}
		}
	}

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/hoxi/runner/sessions/"+ids[0]+"/prompt", "application/json", strings.NewReader(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("prompting a subagent session must be refused, got %d", resp.StatusCode)
	}

	svc.broadcastRouted(parent.ID, tr.FeedLine(`{"event":"result","result":{"conversation_id":"`+agyParentConv+`","status":"SUCCESS"}}`))
	if busy := svc.busySessionIDs(); len(busy) != 0 {
		t.Fatalf("finished subagents must not stay busy: %v", busy)
	}

	if err := svc.deleteConversation(context.Background(), parent.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, ok := svc.store.Meta(id); ok {
			t.Fatalf("deleting the parent must delete subagent session %s", id)
		}
	}
}
