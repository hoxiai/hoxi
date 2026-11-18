package runner

import (
	"path/filepath"
	"testing"
)

func TestNativeSubagentBridgePropagatesHeterogeneousChildModelAndRole(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions"))
	parent := SessionRecord{
		ID:           "ses_native_parent",
		Runtime:      string(RuntimeNative),
		ProviderID:   "anthropic",
		ModelID:      "claude-opus-4-6",
		Mode:         "build",
		ProjectDir:   "/repo",
		WorktreePath: "/repo",
		Title:        "Parent Opus Turn",
	}
	if err := svc.store.Put(parent); err != nil {
		t.Fatal(err)
	}
	userMsg := MessageInfo{ID: "msg_u_1", SessionID: parent.ID, Role: "user"}
	svc.store.UpsertTurnInfo(parent.ID, userMsg)

	tr := NewTurnTranslator(parent.ID, string(RuntimeNative), "msg_a_1", userMsg.ID, parent.ProviderID, parent.ModelID, "build", "")
	tr.CreateChild = svc.childSessionFactory(parent, parent.ModelID, "plan")
	bridge := newNativeSubagentBridge(svc, parent.ID, parent.ModelID, tr)
	svc.broadcastRouted(parent.ID, tr.InitialEvents(userMsg, nil))

	nowMs := int64(1700000000000)
	taskPart := MessagePart{
		ID:        "tool_1",
		SessionID: parent.ID,
		MessageID: "msg_a_1",
		Type:      "tool",
		Tool:      "task",
		CallID:    "call_sub_1",
		State: &ToolState{
			Status: "running",
			Title:  "Locate auth middleware",
		},
	}
	bridge.enrichParentTaskPart(&taskPart, nowMs)
	svc.broadcastRouted(parent.ID, []SSEEvent{partUpdated(taskPart)})

	startEvts := bridge.handleChildEvent(NativeEvent{
		Kind:         NativeEventSubtaskStart,
		ParentCallID: "call_sub_1",
		Title:        "[explore] Locate auth middleware",
		Text:         "Find token verification entrypoints",
		Metadata: map[string]any{
			"subagent_type": "explore",
			"providerID":    "google",
			"modelID":       "gemini-2.5-flash",
			"model":         "google/gemini-2.5-flash",
			"routing":       "agent_config",
			"readOnly":      true,
			"mode":          "plan",
		},
	}, nowMs+10)
	svc.broadcastRouted(parent.ID, startEvts)

	endEvts := bridge.handleChildEvent(NativeEvent{
		Kind:         NativeEventSubtaskEnd,
		ParentCallID: "call_sub_1",
	}, nowMs+50)
	svc.broadcastRouted(parent.ID, endEvts)

	ids := tr.ChildSessionIDs()
	if len(ids) != 1 {
		t.Fatalf("expected 1 child session, got %v", ids)
	}
	childRec, ok := svc.store.Get(ids[0])
	if !ok {
		t.Fatalf("child session %s not found in store", ids[0])
	}
	if childRec.ProviderID != "google" || childRec.ModelID != "gemini-2.5-flash" || childRec.Mode != "plan" {
		t.Fatalf("expected child session google/gemini-2.5-flash (mode=plan), got provider=%q model=%q mode=%q",
			childRec.ProviderID, childRec.ModelID, childRec.Mode)
	}
	if len(childRec.Turns) < 2 || childRec.Turns[1].Info.ProviderID != "google" || childRec.Turns[1].Info.ModelID != "gemini-2.5-flash" || childRec.Turns[1].Info.Agent != "explore" {
		t.Fatalf("expected child assistant turn metadata google/gemini-2.5-flash agent=explore, got %+v", childRec.Turns)
	}

	bridge.mu.Lock()
	storedParentPart := bridge.taskParts["call_sub_1"]
	bridge.mu.Unlock()
	if storedParentPart.State == nil ||
		storedParentPart.State.Metadata["providerID"] != "google" ||
		storedParentPart.State.Metadata["modelID"] != "gemini-2.5-flash" ||
		storedParentPart.State.Metadata["model"] != "google/gemini-2.5-flash" ||
		storedParentPart.State.Metadata["subagent_type"] != "explore" ||
		storedParentPart.State.Metadata["sessionId"] != ids[0] {
		t.Fatalf("expected parent task part metadata to carry child model & sessionId, got %+v", storedParentPart.State)
	}
}
