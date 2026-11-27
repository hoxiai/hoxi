package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCanonicalHistoryAffinityAndOrphanToolRepair(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(root, "sessions.json"))

	turns := []MessageTurn{
		{
			Info: MessageInfo{ID: "msg_u1", Role: "user"},
			Parts: []MessagePart{
				{ID: "p_u1", Type: "text", Text: "Refactor main.go"},
			},
		},
		{
			Info: MessageInfo{
				ID:         "msg_a1",
				Role:       "assistant",
				ProviderID: "anthropic",
				ModelID:    "claude-sonnet-4-5",
			},
			Parts: []MessagePart{
				{
					ID:   "p_think_1",
					Type: "reasoning",
					Text: "Checking imports first",
					Metadata: map[string]interface{}{
						"providerID": "anthropic",
						"modelID":    "claude-sonnet-4-5",
						"signature":  "sig_claude_123",
					},
				},
				{ID: "p_txt_1", Type: "text", Text: "Inspecting workspace..."},
				// 1) Completed read-only tool -> paired normally
				{
					ID:     "p_t_ok",
					Type:   "tool",
					Tool:   "read",
					CallID: "call_ok",
					State: &ToolState{
						Status:   "completed",
						Input:    map[string]interface{}{"filePath": "main.go"},
						Output:   "package main",
						Metadata: map[string]interface{}{"thoughtSignature": "gem_sig_ignore_diff_model"},
					},
				},
				// 2) Unstarted pending tool from interrupted batch -> stripped cleanly so no 400!
				{
					ID:     "p_t_pending",
					Type:   "tool",
					Tool:   "grep",
					CallID: "call_pending",
					State: &ToolState{
						Status: "pending",
						Input:  map[string]interface{}{"pattern": "TODO"},
					},
				},
				// 3) Running read-only tool from crash -> stripped cleanly
				{
					ID:     "p_t_ro_run",
					Type:   "tool",
					Tool:   "glob",
					CallID: "call_ro_run",
					State: &ToolState{
						Status: "running",
						Input:  map[string]interface{}{"pattern": "*.go"},
					},
				},
				// 4) Running mutating tool from crash -> paired with synthetic [outcome_unknown] error result!
				{
					ID:     "p_t_mut_run",
					Type:   "tool",
					Tool:   "bash",
					CallID: "call_mut_run",
					State: &ToolState{
						Status: "running",
						Input:  map[string]interface{}{"command": "go generate ./..."},
					},
				},
			},
		},
	}

	// Case A: Same provider + model -> replays private thinking + signature, repairs orphan tools!
	sameMsgs := svc.projectCanonicalNativeHistory(svc.rootDir, turns, "anthropic", "claude-sonnet-4-5")
	if len(sameMsgs) != 3 {
		t.Fatalf("expected 3 projected messages (user, assistant, user tool_results), got %d", len(sameMsgs))
	}
	asst := sameMsgs[1].Content
	if len(asst) != 4 {
		t.Fatalf("expected thinking + text + 2 valid tool_use blocks (pending & running read-only stripped), got %d: %+v", len(asst), asst)
	}
	if asst[0].Type != "thinking" || asst[0].Signature != "sig_claude_123" {
		t.Fatalf("expected thinking block with signature on same-model replay, got: %+v", asst[0])
	}
	results := sameMsgs[2].Content
	if len(results) != 2 {
		t.Fatalf("expected 1-to-1 paired tool_results (len=2), got %d: %+v", len(results), results)
	}
	if results[0].ToolUseID != "call_ok" || results[0].IsError {
		t.Fatalf("unexpected completed tool_result: %+v", results[0])
	}
	if results[1].ToolUseID != "call_mut_run" || !results[1].IsError || !strings.Contains(results[1].Text, "[outcome_unknown]") {
		t.Fatalf("expected [outcome_unknown] synthesized tool_result for running mutating bash, got: %+v", results[1])
	}

	// Case B: Switched model mid-session (e.g. anthropic -> openai/gpt-5) -> strips private thinking & signatures!
	switchedMsgs := svc.projectCanonicalNativeHistory(svc.rootDir, turns, "openai", "gpt-5")
	for _, b := range switchedMsgs[1].Content {
		if b.Type == "thinking" {
			t.Fatalf("expected private thinking block to be stripped on cross-model switch, got: %+v", b)
		}
		if b.Signature != "" {
			t.Fatalf("expected provider-specific signature to be stripped on cross-model switch, got: %+v", b)
		}
	}
}

type interruptedNativeEngine struct {
	mutateBeforeDrop bool
}

func (e *interruptedNativeEngine) RunTurn(_ context.Context, req NativeTurnRequest) error {
	if req.Emit != nil {
		req.Emit(NativeEvent{
			Kind:      NativeEventThinkingEnd,
			PartID:    "prt_think_1",
			Text:      "Planning step",
			Signature: "sig_abc",
		})
		if e.mutateBeforeDrop {
			req.Emit(NativeEvent{
				Kind:     NativeEventToolRunning,
				PartID:   "prt_bash_1",
				CallID:   "call_bash_1",
				ToolName: "bash",
				Input:    map[string]any{"command": "touch mutated.txt"},
			})
		} else {
			req.Emit(NativeEvent{
				Kind:     NativeEventToolPending,
				PartID:   "prt_read_1",
				CallID:   "call_read_1",
				ToolName: "read",
				Input:    map[string]any{"filePath": "README.md"},
			})
		}
	}
	return errors.New("unexpected EOF: model stream ended before terminal frame")
}

func (e *interruptedNativeEngine) Summarize(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

func waitForRunnerSessionIdle(t *testing.T, ch <-chan SSEEvent) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type == "session.status" {
				if st, ok := evt.Properties["status"].(SessionStatus); ok && st.Type == "idle" {
					return
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for session idle")
		}
	}
}

func TestNativeTurnSettlementDistinguishesStreamInterruptAndOutcomeUnknown(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ws, nil, nil, filepath.Join(root, "sessions.json"))
	defer svc.CloseAll()

	// 1) Stream drops BEFORE any mutating tool ran -> Finish="interrupted", IsRetryable=true, pending read settled as "cancelled"
	recRO := SessionRecord{
		ID:           "ses_nat_ro_drop",
		Runtime:      string(RuntimeNative),
		ProviderID:   "anthropic",
		ModelID:      "claude-sonnet-4-5",
		ProjectDir:   ws,
		WorktreePath: ws,
	}
	if err := svc.store.Put(recRO); err != nil {
		t.Fatal(err)
	}
	svc.SetNativeEngine(&interruptedNativeEngine{mutateBeforeDrop: false})
	chRO, unsubRO := svc.Subscribe(recRO.ID)
	defer unsubRO()
	u1 := MessageInfo{ID: "msg_u_ro", SessionID: recRO.ID, Role: "user", Time: MessageTime{Created: time.Now().UnixMilli()}}
	p1 := []MessagePart{{ID: "prt_u_ro", SessionID: recRO.ID, MessageID: u1.ID, Type: "text", Text: "inspect"}}
	if err := svc.startNativeTurn(recRO, "code", "claude-sonnet-4-5", "", "", "inspect", u1, p1, "msg_a_ro"); err != nil {
		t.Fatal(err)
	}
	waitForRunnerSessionIdle(t, chRO)

	gotRO, ok := svc.store.Get(recRO.ID)
	if !ok || len(gotRO.Turns) != 2 {
		t.Fatalf("expected 2 turns, got: %+v", gotRO.Turns)
	}
	asstRO := gotRO.Turns[1]
	if asstRO.Info.Finish != TurnFinishInterrupted {
		t.Fatalf("expected Finish=%q on truncated stream, got %q", TurnFinishInterrupted, asstRO.Info.Finish)
	}
	if asstRO.Info.Error == nil || asstRO.Info.Error.Data == nil || !asstRO.Info.Error.Data.IsRetryable {
		t.Fatalf("expected read-only interrupted turn to be retryable, got: %+v", asstRO.Info.Error)
	}
	// Verify lossless signature was persisted on reasoning part!
	if sig := stringMetaField(asstRO.Parts[0].Metadata, "signature"); sig != "sig_abc" {
		t.Fatalf("expected persisted reasoning signature 'sig_abc', got %q", sig)
	}

	// 2) Stream drops WHILE mutating bash tool was running -> Finish="interrupted", Kind="outcome_unknown", IsRetryable=false!
	svc2 := NewService(ws, nil, nil, filepath.Join(root, "sessions2.json"))
	defer svc2.CloseAll()
	recMut := SessionRecord{
		ID:           "ses_nat_mut_drop",
		Runtime:      string(RuntimeNative),
		ProviderID:   "openai",
		ModelID:      "gpt-5",
		ProjectDir:   ws,
		WorktreePath: ws,
	}
	if err := svc2.store.Put(recMut); err != nil {
		t.Fatal(err)
	}
	svc2.SetNativeEngine(&interruptedNativeEngine{mutateBeforeDrop: true})
	chMut, unsubMut := svc2.Subscribe(recMut.ID)
	defer unsubMut()
	u2 := MessageInfo{ID: "msg_u_mut", SessionID: recMut.ID, Role: "user", Time: MessageTime{Created: time.Now().UnixMilli()}}
	p2 := []MessagePart{{ID: "prt_u_mut", SessionID: recMut.ID, MessageID: u2.ID, Type: "text", Text: "deploy"}}
	if err := svc2.startNativeTurn(recMut, "code", "gpt-5", "", "", "deploy", u2, p2, "msg_a_mut"); err != nil {
		t.Fatal(err)
	}
	waitForRunnerSessionIdle(t, chMut)

	gotMut, ok := svc2.store.Get(recMut.ID)
	if !ok || len(gotMut.Turns) != 2 {
		t.Fatalf("expected 2 turns, got: %+v", gotMut.Turns)
	}
	asstMut := gotMut.Turns[1]
	if asstMut.Info.Finish != TurnFinishInterrupted {
		t.Fatalf("expected Finish=%q, got %q", TurnFinishInterrupted, asstMut.Info.Finish)
	}
	if asstMut.Info.Error == nil || asstMut.Info.Error.Data == nil || asstMut.Info.Error.Data.IsRetryable {
		t.Fatalf("expected IsRetryable=false when mutating tool has unknown outcome, got: %+v", asstMut.Info.Error)
	}
	if asstMut.Info.Error.Data.Kind != ToolOutcomeUnknown {
		t.Fatalf("expected Error.Data.Kind=%q, got %q", ToolOutcomeUnknown, asstMut.Info.Error.Data.Kind)
	}
	var bashTool *MessagePart
	for i := range asstMut.Parts {
		if asstMut.Parts[i].Type == "tool" {
			bashTool = &asstMut.Parts[i]
		}
	}
	if bashTool == nil || stringMetaField(bashTool.State.Metadata, "outcome") != ToolOutcomeUnknown {
		t.Fatalf("expected settled bash tool Metadata[outcome]=%q, got: %+v", ToolOutcomeUnknown, bashTool)
	}
}
