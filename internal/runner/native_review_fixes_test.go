package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func replyTestPermission(t *testing.T, svc *Service, permID, reply, message string) {
	t.Helper()
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	body, _ := json.Marshal(map[string]string{"reply": reply, "message": message})
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/permission/"+permID+"/reply", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("permission reply %s failed: status=%d body=%s", permID, rec.Code, rec.Body.String())
	}
}

func TestNativeSearchApprovalDistinguishesQueryFromFilePattern(t *testing.T) {
	ws := t.TempDir()
	svc := NewService(ws, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	defer svc.CloseAll()
	rec := SessionRecord{ID: "ses_search_approval", Runtime: string(RuntimeNative), ProjectDir: ws, WorktreePath: ws}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	hook := svc.nativePermissionHook(rec, "msg_search")
	for _, tc := range []struct {
		name, tool, input string
		ask               bool
	}{
		{"route text", "grep", `{"path":".","pattern":"/api/claude|api/v1","include":"*.{ts,vue}"}`, false},
		{"relative import text", "grep", `{"pattern":"../components"}`, false},
		{"secret target", "grep", `{"pattern":"/api","path":".env"}`, true},
		{"secret filter", "grep", `{"pattern":"/api","include":"**/.env"}`, true},
		{"outside filter", "grep", `{"pattern":"/api","include":"../other/*.go"}`, true},
		{"file pattern", "glob", `{"pattern":"../components"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A request reaching the interactive gate must fail on cancellation.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := hook(ctx, NativePermissionRequest{SessionID: rec.ID, CallID: tc.name, ToolName: tc.tool, IsRead: true, Input: json.RawMessage(tc.input)})
			if (err != nil) != tc.ask || (err != nil && !strings.Contains(err.Error(), "approval hook was interrupted")) {
				t.Fatalf("ask = %v, want %v (error %v)", err != nil, tc.ask, err)
			}
		})
	}
}

func TestNativeExplicitAskTaskPromptsInSmartAutoAndTrustedAndDeniesInUnattended(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ws, nil, nil, filepath.Join(root, "sessions.json"))
	defer svc.CloseAll()

	rec := SessionRecord{
		ID:           "ses_explicit_task_ask",
		Runtime:      string(RuntimeNative),
		ProviderID:   "anthropic",
		ModelID:      "claude-sonnet-4-6",
		ProjectDir:   ws,
		WorktreePath: ws,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	events, unsub := svc.Subscribe(rec.ID)
	defer unsub()

	hook := svc.nativePermissionHook(rec, "msg_asst_ask_1")
	taskInput := json.RawMessage(`{"subagent_type":"explore","description":"gated explore","prompt":"inspect auth"}`)

	for _, mode := range []string{ApprovalModeSmart, ApprovalModeAuto, ApprovalModeTrusted} {
		setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: mode})

		errCh := make(chan error, 1)
		go func(m string) {
			errCh <- hook(context.Background(), NativePermissionRequest{
				SessionID: rec.ID,
				CallID:    "call_ask_" + m,
				ToolName:  "task",
				Input:     taskInput,
				IsRead:    false,
				ForceAsk:  true,
			})
		}(mode)

		asked := waitForEvent(t, events, "permission.asked")
		permID, _ := asked.Properties["id"].(string)
		if permID == "" {
			t.Fatalf("mode=%s: expected permission.asked id, got %+v", mode, asked.Properties)
		}
		replyTestPermission(t, svc, permID, "reject", "no delegation")
		if err := <-errCh; err == nil || !strings.Contains(err.Error(), "no delegation") {
			t.Fatalf("mode=%s: expected explicit ForceAsk rejection error, got %v", mode, err)
		}
	}

	// Verify once-approval succeeds when user approves the explicit ask card.
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeSmart})
	allowCh := make(chan error, 1)
	go func() {
		allowCh <- hook(context.Background(), NativePermissionRequest{
			SessionID: rec.ID,
			CallID:    "call_ask_once",
			ToolName:  "task",
			Input:     taskInput,
			IsRead:    false,
			ForceAsk:  true,
		})
	}()
	askedOnce := waitForEvent(t, events, "permission.asked")
	onceID, _ := askedOnce.Properties["id"].(string)
	replyTestPermission(t, svc, onceID, "once", "")
	if err := <-allowCh; err != nil {
		t.Fatalf("expected ForceAsk to succeed after user replied once, got %v", err)
	}

	// Unattended mode must deny and record deferred rather than silently bypassing explicit ForceAsk.
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeUnattended})
	unattErr := hook(context.Background(), NativePermissionRequest{
		SessionID: rec.ID,
		CallID:    "call_ask_unattended",
		ToolName:  "task",
		Input:     taskInput,
		IsRead:    false,
		ForceAsk:  true,
	})
	if unattErr == nil {
		t.Fatal("expected ForceAsk under ApprovalModeUnattended to fail closed")
	}
}

type subagentMutationInterruptEngine struct {
	subagentType  string
	readOnly      bool
	childMutates  bool
	childFinishes bool
}

func (e *subagentMutationInterruptEngine) RunTurn(_ context.Context, req NativeTurnRequest) error {
	if req.Emit == nil {
		return nil
	}
	req.Emit(NativeEvent{
		Kind:     NativeEventToolRunning,
		Step:     1,
		PartID:   "prt_task_mut",
		CallID:   "call_task_mut",
		ToolName: "task",
		Title:    "Delegated subtask",
		Input:    map[string]any{"subagent_type": e.subagentType, "description": "Delegated subtask", "prompt": "do work"},
	})
	req.Emit(NativeEvent{
		Kind:         NativeEventSubtaskStart,
		ParentCallID: "call_task_mut",
		Title:        "Delegated subtask",
		Text:         "do work",
		Metadata: map[string]any{
			"subagent_type": e.subagentType,
			"readOnly":      e.readOnly,
			"mode":          map[bool]string{true: "plan", false: "build"}[e.readOnly],
		},
	})
	if e.childMutates {
		req.Emit(NativeEvent{
			Kind:         NativeEventToolRunning,
			ParentCallID: "call_task_mut",
			PartID:       "child_prt_w",
			CallID:       "child_call_w",
			ToolName:     "write",
			Input:        map[string]any{"filePath": "out.txt", "content": "mutated"},
		})
		if e.childFinishes {
			req.Emit(NativeEvent{
				Kind:         NativeEventToolCompleted,
				ParentCallID: "call_task_mut",
				CallID:       "child_call_w",
				ToolName:     "write",
				Text:         "wrote 7 bytes to out.txt",
			})
			req.Emit(NativeEvent{
				Kind:         NativeEventSubtaskEnd,
				ParentCallID: "call_task_mut",
				Metadata:     map[string]any{"mutated": true, "readOnly": false},
			})
			req.Emit(NativeEvent{
				Kind:     NativeEventToolCompleted,
				Step:     1,
				PartID:   "prt_task_mut",
				CallID:   "call_task_mut",
				ToolName: "task",
				Title:    "Delegated subtask",
				Text:     "completed write",
				Metadata: map[string]any{"subagent": true, "subagent_type": e.subagentType, "readOnly": false, "mutated": true},
			})
		}
	} else {
		req.Emit(NativeEvent{
			Kind:         NativeEventToolRunning,
			ParentCallID: "call_task_mut",
			PartID:       "child_prt_r",
			CallID:       "child_call_r",
			ToolName:     "read",
			Input:        map[string]any{"filePath": "README.md"},
		})
		req.Emit(NativeEvent{
			Kind:         NativeEventToolCompleted,
			ParentCallID: "call_task_mut",
			CallID:       "child_call_r",
			ToolName:     "read",
			Text:         "readme content",
		})
		req.Emit(NativeEvent{
			Kind:         NativeEventSubtaskEnd,
			ParentCallID: "call_task_mut",
			Metadata:     map[string]any{"mutated": false, "readOnly": true},
		})
		req.Emit(NativeEvent{
			Kind:     NativeEventToolCompleted,
			Step:     1,
			PartID:   "prt_task_mut",
			CallID:   "call_task_mut",
			ToolName: "task",
			Title:    "Delegated subtask",
			Text:     "read ok",
			Metadata: map[string]any{"subagent": true, "subagent_type": e.subagentType, "readOnly": true, "mutated": false},
		})
	}
	return errors.New("unexpected EOF: model stream ended before terminal frame")
}

func (e *subagentMutationInterruptEngine) Summarize(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

func TestGeneralSubtaskMutationsMarkInterruptedParentTurnNonRetryable(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}

	runCase := func(id string, eng NativeEngine) MessageTurn {
		t.Helper()
		svc := NewService(ws, nil, nil, filepath.Join(root, id+".json"))
		defer svc.CloseAll()
		svc.SetNativeEngine(eng)
		rec := SessionRecord{
			ID:           id,
			Runtime:      string(RuntimeNative),
			ProviderID:   "openai",
			ModelID:      "gpt-5",
			ProjectDir:   ws,
			WorktreePath: ws,
		}
		if err := svc.store.Put(rec); err != nil {
			t.Fatal(err)
		}
		ch, unsub := svc.Subscribe(rec.ID)
		defer unsub()
		u := MessageInfo{ID: "msg_u_" + id, SessionID: rec.ID, Role: "user", Time: MessageTime{Created: time.Now().UnixMilli()}}
		p := []MessagePart{{ID: "prt_u_" + id, SessionID: rec.ID, MessageID: u.ID, Type: "text", Text: "run"}}
		if err := svc.startNativeTurn(rec, "build", "gpt-5", "", "", "run", u, p, "msg_a_"+id); err != nil {
			t.Fatal(err)
		}
		waitForRunnerSessionIdle(t, ch)
		got, ok := svc.store.Get(rec.ID)
		if !ok || len(got.Turns) < 2 {
			t.Fatalf("missing turns for %s: %+v", id, got.Turns)
		}
		return got.Turns[1]
	}

	// 1. General subagent finished writing before parent disconnected -> non-retryable outcome_unknown!
	doneMut := runCase("ses_gen_mut_done", &subagentMutationInterruptEngine{
		subagentType: "general", readOnly: false, childMutates: true, childFinishes: true,
	})
	if doneMut.Info.Error == nil || doneMut.Info.Error.Data == nil || doneMut.Info.Error.Data.IsRetryable || doneMut.Info.Error.Data.Kind != ToolOutcomeUnknown {
		t.Fatalf("expected completed mutating general subagent to disable blind parent retry, got %+v", doneMut.Info.Error)
	}

	// 2. General subagent was STILL running write when parent disconnected -> non-retryable outcome_unknown!
	inflightMut := runCase("ses_gen_mut_inflight", &subagentMutationInterruptEngine{
		subagentType: "general", readOnly: false, childMutates: true, childFinishes: false,
	})
	if inflightMut.Info.Error == nil || inflightMut.Info.Error.Data == nil || inflightMut.Info.Error.Data.IsRetryable || inflightMut.Info.Error.Data.Kind != ToolOutcomeUnknown {
		t.Fatalf("expected in-flight mutating general subagent to disable blind parent retry, got %+v", inflightMut.Info.Error)
	}

	// 3. Read-only explore subagent only ran read -> parent remains retryable!
	roTurn := runCase("ses_exp_ro", &subagentMutationInterruptEngine{
		subagentType: "explore", readOnly: true, childMutates: false, childFinishes: true,
	})
	if roTurn.Info.Error == nil || roTurn.Info.Error.Data == nil || !roTurn.Info.Error.Data.IsRetryable {
		t.Fatalf("expected read-only explore subagent interruption to remain retryable, got %+v", roTurn.Info.Error)
	}
}

type multiStepMediaCompactionEngine struct {
	emitCompaction bool
	finishReason   string
}

func (e *multiStepMediaCompactionEngine) RunTurn(_ context.Context, req NativeTurnRequest) error {
	if req.Emit == nil {
		return nil
	}
	if e.emitCompaction {
		req.Emit(NativeEvent{
			Kind: NativeEventContextCompacted,
			Step: 1,
			Text: "Checkpoint: prior 10 turns refactored config parser.",
			Metadata: map[string]any{
				"scope":        "before_current_user",
				"keepFromStep": 1,
			},
		})
	}
	// Step 1: read an image (with Media!)
	req.Emit(NativeEvent{Kind: NativeEventThinkingStart, Step: 1, PartID: "p_th_1"})
	req.Emit(NativeEvent{Kind: NativeEventThinkingEnd, Step: 1, PartID: "p_th_1", Text: "Inspecting diagram", Signature: "sig_s1"})
	req.Emit(NativeEvent{Kind: NativeEventToolRunning, Step: 1, PartID: "p_tl_1", CallID: "call_img_1", ToolName: "read", Input: map[string]any{"filePath": "arch.png"}})
	req.Emit(NativeEvent{
		Kind:     NativeEventToolCompleted,
		Step:     1,
		PartID:   "p_tl_1",
		CallID:   "call_img_1",
		ToolName: "read",
		Title:    "arch.png",
		Text:     "Read arch.png (image/png, 4 bytes)",
		Media: []NativeContentBlock{{
			Type:     "media",
			MimeType: "image/png",
			Data:     "aGVsbG8=",
			Filename: "arch.png",
		}},
	})
	// Step 2: grep based on Step 1
	req.Emit(NativeEvent{Kind: NativeEventThinkingStart, Step: 2, PartID: "p_th_2"})
	req.Emit(NativeEvent{Kind: NativeEventThinkingEnd, Step: 2, PartID: "p_th_2", Text: "Checking symbol", Signature: "sig_s2"})
	req.Emit(NativeEvent{Kind: NativeEventToolRunning, Step: 2, PartID: "p_tl_2", CallID: "call_grep_2", ToolName: "grep", Input: map[string]any{"pattern": "Router"}})
	req.Emit(NativeEvent{Kind: NativeEventToolCompleted, Step: 2, PartID: "p_tl_2", CallID: "call_grep_2", ToolName: "grep", Text: "router.go:10: type Router struct"})
	// Step 3: final synthesis
	req.Emit(NativeEvent{Kind: NativeEventTextStart, Step: 3, PartID: "p_tx_3"})
	req.Emit(NativeEvent{Kind: NativeEventTextEnd, Step: 3, PartID: "p_tx_3", Text: "Diagram matches Router."})
	if e.finishReason != "" {
		req.Emit(NativeEvent{Kind: NativeEventTurnFinish, Step: 3, Finish: e.finishReason})
	}
	return nil
}

func (e *multiStepMediaCompactionEngine) Summarize(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

func TestCanonicalHistoryPreservesStepOrderToolMediaAndCompactionCheckpoint(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ws, nil, nil, filepath.Join(root, "sessions.json"))
	defer svc.CloseAll()

	rec := SessionRecord{
		ID:           "ses_steps_media_cmp",
		Runtime:      string(RuntimeNative),
		ProviderID:   "anthropic",
		ModelID:      "claude-sonnet-4-6",
		ProjectDir:   ws,
		WorktreePath: ws,
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_old_u", SessionID: "ses_steps_media_cmp", Role: "user"},
				Parts: []MessagePart{{ID: "prt_old_u", Type: "text", Text: "VERY_OLD_TURN_BEFORE_COMPACTION"}},
			},
			{
				Info:  MessageInfo{ID: "msg_old_a", SessionID: "ses_steps_media_cmp", Role: "assistant", ParentID: "msg_old_u"},
				Parts: []MessagePart{{ID: "prt_old_a", Type: "text", Text: "VERY_OLD_ASSISTANT_REPLY"}},
			},
		},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	svc.SetNativeEngine(&multiStepMediaCompactionEngine{emitCompaction: true, finishReason: "length"})
	ch, unsub := svc.Subscribe(rec.ID)
	defer unsub()

	now := time.Now().UnixMilli()
	uInfo := MessageInfo{ID: "msg_u_cur", SessionID: rec.ID, Role: "user", Time: MessageTime{Created: now}}
	uParts := []MessagePart{{ID: "prt_u_cur", SessionID: rec.ID, MessageID: uInfo.ID, Type: "text", Text: "Compare arch.png with Router"}}
	// Append current user turn to store just like prompt_submit does before startNativeTurn
	updatedRec, _, err := svc.store.Update(rec.ID, func(sr *SessionRecord) {
		sr.Turns = append(sr.Turns,
			MessageTurn{Info: uInfo, Parts: uParts},
			MessageTurn{Info: MessageInfo{ID: "msg_a_cur", SessionID: rec.ID, Role: "assistant", ParentID: uInfo.ID, ProviderID: rec.ProviderID, ModelID: rec.ModelID, Time: MessageTime{Created: now}}},
		)
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.startNativeTurn(updatedRec, "build", rec.ModelID, "", "", uParts[0].Text, uInfo, uParts, "msg_a_cur", uParts...); err != nil {
		t.Fatal(err)
	}
	waitForRunnerSessionIdle(t, ch)

	stored, ok := svc.store.Get(rec.ID)
	if !ok {
		t.Fatal("missing stored session")
	}
	// Verify UI scrollback still retains all 4 turns!
	if len(stored.Turns) != 4 {
		t.Fatalf("expected all 4 UI turns preserved in store, got %d", len(stored.Turns))
	}
	asstTurn := stored.Turns[3]
	if asstTurn.Info.Finish != "length" {
		t.Fatalf("expected persisted assistant Finish=%q, got %q", "length", asstTurn.Info.Finish)
	}

	// Project history for Turn N+1!
	projected := svc.projectCanonicalNativeHistory(svc.rootDir, stored.Turns, "anthropic", "claude-sonnet-4-6")
	// Expected sequence:
	// [0] user: <context_checkpoint>...
	// [1] assistant: Understood. Continuing from the compacted checkpoint.
	// [2] user: Compare arch.png with Router
	// [3] assistant (Step 1): thinking + tool_use(call_img_1)
	// [4] user (Step 1 results): tool_result(call_img_1) + media(arch.png)
	// [5] assistant (Step 2): thinking + tool_use(call_grep_2)
	// [6] user (Step 2 results): tool_result(call_grep_2)
	// [7] assistant (Step 3): text("Diagram matches Router.")
	if len(projected) != 8 {
		t.Fatalf("expected 8 step-preserved messages after checkpoint, got %d: %+v", len(projected), projected)
	}
	for i, m := range projected {
		wantRole := "user"
		if i%2 == 1 {
			wantRole = "assistant"
		}
		if m.Role != wantRole {
			t.Fatalf("message[%d] broke role alternation: got %q want %q", i, m.Role, wantRole)
		}
		for _, b := range m.Content {
			if strings.Contains(b.Text, "VERY_OLD_TURN_BEFORE_COMPACTION") {
				t.Fatal("expected pre-checkpoint raw turn omitted from projected history")
			}
		}
	}
	if !strings.Contains(projected[0].Content[0].Text, "<context_checkpoint>") {
		t.Fatalf("expected checkpoint in projected[0], got %+v", projected[0])
	}
	step1Results := projected[4].Content
	if len(step1Results) != 2 || step1Results[0].Type != "tool_result" || step1Results[1].Type != "media" || step1Results[1].Data != "aGVsbG8=" {
		t.Fatalf("expected tool_result + persisted media attachment replayed in Step 1 results, got %+v", step1Results)
	}
	if projected[7].Content[0].Text != "Diagram matches Router." {
		t.Fatalf("expected final Step 3 synthesis in projected[7], got %+v", projected[7])
	}
}
