package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const (
	agyParentConv = "e8ee1879-66f6-4bf6-9feb-bd47c729070f"
	agyBugConv    = "ca5c164f-82f2-40af-8cb9-73a64a5d7ae5"
	agyDocsConv   = "b71b0905-4721-4402-8fd2-726deb547e14"
)

func agyDispatchLines() []string {
	return []string{
		`{"event":"init","conversation_id":"` + agyParentConv + `","init":{"model":"gemini-4-argon-high"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"` + agyParentConv + `","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"Delegating."}}`,
		`{"event":"step_update","step_update":{"conversation_id":"` + agyParentConv + `","step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"invoke_subagent","tool_info":{"name":"invoke_subagent","parameters":{"Subagents":[{"Role":"Deep Audit A","TypeName":"research","Model":"gemini-4-argon-high","Prompt":"Audit backend"},{"Role":"Deep Audit B","TypeName":"research","Prompt":"Audit frontend"}]}}}}`,
		`{"event":"step_update","step_update":{"conversation_id":"` + agyParentConv + `","step_index":2,"state":"DONE","step_type":"tool","tool_name":"invoke_subagent","subagent_info":{"subagents":[{"role":"Deep Audit A","conversation_id":"` + agyBugConv + `"},{"role":"Deep Audit B","conversation_id":"` + agyDocsConv + `"}]}}}`,
	}
}

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
	tr.LookupThink, tr.LookupStep = nil, nil
	tr.CreateChild = svc.childSessionFactory(parent, "gemini-4-argon-high", "build")
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

func TestFlatAgyInvokeSubagentRegistersChildForHookApproval(t *testing.T) {
	t.Setenv("GEMINI_HOME", filepath.Join(t.TempDir(), ".gemini"))
	dir := t.TempDir()
	svc, workDir := newAgySubagentHookService(t)
	_ = dir
	parent := SessionRecord{
		ID:           "ses_agy_flat_parent",
		Runtime:      string(RuntimeAntigravity),
		ExternalID:   agyParentConv,
		ProjectDir:   workDir,
		WorktreePath: workDir,
		Title:        "Flat Invoke Audit",
	}
	if err := svc.store.Put(parent); err != nil {
		t.Fatal(err)
	}
	userMsg := MessageInfo{ID: "msg_u_flat", SessionID: parent.ID, Role: "user"}
	svc.store.UpsertTurnInfo(parent.ID, userMsg)

	tr := NewTurnTranslator(parent.ID, parent.Runtime, "msg_a_flat", userMsg.ID, "antigravity", "gemini-4-carbon-high", "build", agyParentConv)
	tr.CreateChild = svc.childSessionFactory(parent, "gemini-4-carbon-high", "build")
	svc.broadcastRouted(parent.ID, tr.InitialEvents(userMsg, nil))
	svc.broadcastRouted(parent.ID, tr.FeedLine(`{"event":"init","conversation_id":"`+agyParentConv+`","init":{"model":"gemini-4-carbon-high"}}`))
	svc.broadcastRouted(parent.ID, tr.FeedLine(`{"event":"step_update","step_update":{"conversation_id":"`+agyParentConv+`","step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"invoke_subagent","tool_info":{"name":"invoke_subagent","parameters":{"Role":"Code Auditor","TypeName":"research","Model":"inherit","Prompt":"Audit repo"}}}}`))
	svc.broadcastRouted(parent.ID, tr.FeedLine(`{"event":"step_update","step_update":{"conversation_id":"`+agyParentConv+`","step_index":2,"state":"DONE","step_type":"tool","tool_name":"invoke_subagent","tool_info":{"name":"invoke_subagent","output":"Created the following subagent:\n{\n  \"conversationId\": \"`+agyBugConv+`\"\n}\n"}}}`))

	ids := tr.ChildSessionIDs()
	if len(ids) != 1 {
		t.Fatalf("expected 1 child session, got %v", ids)
	}
	child, ok := svc.store.Get(ids[0])
	if !ok || child.ExternalID != agyBugConv || child.ModelID != "gemini-4-carbon-high" {
		t.Fatalf("unexpected child session record: %+v", child)
	}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	var p hookPayload
	p.ConversationID = agyBugConv
	p.StepIdx = 1
	p.ToolCall.Name = "read_file"
	p.ToolCall.Args = map[string]interface{}{"Path": filepath.Join(workDir, "main.go")}
	cred := hookCredential{runtime: string(RuntimeAntigravity), sessionID: parent.ID, messageID: "msg_a_flat"}
	if d, reason := svc.decideHookCall(req, cred, p); d != hookDecisionApprove {
		t.Fatalf("expected flat invoke_subagent child conversation read_file to be approved, got %q (%q)", d, reason)
	}
}
