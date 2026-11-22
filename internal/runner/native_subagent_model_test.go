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

func TestNativeSubagentBridgeThrottlesBurstHeartbeatsButPropagatesStateChanges(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions"))
	parent := SessionRecord{
		ID:           "ses_native_throttle",
		Runtime:      string(RuntimeNative),
		ProviderID:   "openai",
		ModelID:      "gpt-5",
		Mode:         "build",
		ProjectDir:   "/repo",
		WorktreePath: "/repo",
		Title:        "Parent Throttle Turn",
	}
	if err := svc.store.Put(parent); err != nil {
		t.Fatal(err)
	}
	userMsg := MessageInfo{ID: "msg_u_1", SessionID: parent.ID, Role: "user"}
	svc.store.UpsertTurnInfo(parent.ID, userMsg)

	tr := NewTurnTranslator(parent.ID, string(RuntimeNative), "msg_a_1", userMsg.ID, parent.ProviderID, parent.ModelID, "build", "")
	tr.CreateChild = svc.childSessionFactory(parent, parent.ModelID, "build")
	bridge := newNativeSubagentBridge(svc, parent.ID, parent.ModelID, tr)

	nowMs := int64(1700000000000)
	taskPart := MessagePart{
		ID:        "tool_1",
		SessionID: parent.ID,
		MessageID: "msg_a_1",
		Type:      "tool",
		Tool:      "task",
		CallID:    "call_sub_throttle",
		State:     &ToolState{Status: "running", Title: "Build feature"},
	}
	bridge.enrichParentTaskPart(&taskPart, nowMs)

	countParentPartUpdates := func(evts []SSEEvent) int {
		n := 0
		for _, e := range evts {
			if e.Type != "message.part.updated" {
				continue
			}
			if p, ok := e.Properties["part"].(MessagePart); ok && p.SessionID == parent.ID && p.ID == "tool_1" {
				n++
			}
		}
		return n
	}

	// SubtaskStart must immediately emit parent part update with sessionId.
	startEvts := bridge.handleChildEvent(NativeEvent{
		Kind:         NativeEventSubtaskStart,
		ParentCallID: "call_sub_throttle",
		Title:        "Build feature",
		Metadata:     map[string]any{"subagent_type": "general", "mode": "build"},
	}, nowMs+10)
	if got := countParentPartUpdates(startEvts); got != 1 {
		t.Fatalf("expected 1 parent update on SubtaskStart, got %d", got)
	}

	// Burst of 5 read-only child tool events inside 200ms should not duplicate parent task snapshots.
	burstParentUpdates := 0
	for i, dt := range []int64{40, 70, 100, 130, 160} {
		evts := bridge.handleChildEvent(NativeEvent{
			Kind:         NativeEventToolRunning,
			ParentCallID: "call_sub_throttle",
			CallID:       "r_" + string(rune('a'+i)),
			ToolName:     "read",
		}, nowMs+dt)
		burstParentUpdates += countParentPartUpdates(evts)
	}
	if burstParentUpdates != 0 {
		t.Fatalf("expected burst read heartbeats within 5s to be throttled, got %d parent updates", burstParentUpdates)
	}

	// First mutating child tool must flip mutated=true and emit immediately even within 5s.
	mutEvts := bridge.handleChildEvent(NativeEvent{
		Kind:         NativeEventToolRunning,
		ParentCallID: "call_sub_throttle",
		CallID:       "w_1",
		ToolName:     "write",
	}, nowMs+200)
	if got := countParentPartUpdates(mutEvts); got != 1 {
		t.Fatalf("expected immediate parent update when child first mutates workspace, got %d", got)
	}

	// Subsequent tool event 100ms later stays throttled.
	soonEvts := bridge.handleChildEvent(NativeEvent{
		Kind:         NativeEventToolCompleted,
		ParentCallID: "call_sub_throttle",
		CallID:       "w_1",
		ToolName:     "write",
	}, nowMs+300)
	if got := countParentPartUpdates(soonEvts); got != 0 {
		t.Fatalf("expected rapid post-mutation event within 5s to stay throttled, got %d", got)
	}

	// Activity >= 5s after last emit refreshes lastActivityAt on parent task part.
	laterEvts := bridge.handleChildEvent(NativeEvent{
		Kind:         NativeEventToolRunning,
		ParentCallID: "call_sub_throttle",
		CallID:       "r_late",
		ToolName:     "read",
	}, nowMs+5500)
	if got := countParentPartUpdates(laterEvts); got != 1 {
		t.Fatalf("expected heartbeat parent update once >=5s elapsed, got %d", got)
	}

	// SubtaskEnd always emits immediately.
	endEvts := bridge.handleChildEvent(NativeEvent{
		Kind:         NativeEventSubtaskEnd,
		ParentCallID: "call_sub_throttle",
		Metadata:     map[string]any{"mutated": true},
	}, nowMs+5600)
	if got := countParentPartUpdates(endEvts); got != 1 {
		t.Fatalf("expected immediate parent update on SubtaskEnd, got %d", got)
	}
}
