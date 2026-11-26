package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/runner/memstore"
	"github.com/hoxiai/hoxi/internal/sandbox"
)

type captureNativeEngine struct {
	gotReq  chan NativeTurnRequest
	summary string
}

func (c *captureNativeEngine) RunTurn(_ context.Context, req NativeTurnRequest) error {
	c.gotReq <- req
	if req.Emit != nil {
		req.Emit(NativeEvent{Kind: NativeEventTextStart, PartID: "part_1"})
		req.Emit(NativeEvent{Kind: NativeEventTextDelta, PartID: "part_1", Text: "ok"})
		req.Emit(NativeEvent{Kind: NativeEventTextEnd, PartID: "part_1", Text: "ok"})
	}
	return nil
}

func (c *captureNativeEngine) Summarize(_ context.Context, providerID, modelID, transcript string) (string, error) {
	if c.summary != "" {
		return c.summary, nil
	}
	return "Summary(" + providerID + "/" + modelID + "): " + transcript[:min(24, len(transcript))], nil
}

func TestStartNativeTurnWiresHooksAndMultimodal(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(workspace, nil, nil, filepath.Join(root, "sessions.json"))
	capEng := &captureNativeEngine{gotReq: make(chan NativeTurnRequest, 1)}
	svc.SetNativeEngine(capEng)

	rec := SessionRecord{
		ID:           "ses_nat_wire",
		Runtime:      string(RuntimeNative),
		ProviderID:   "openai",
		ModelID:      "gpt-5",
		ProjectDir:   workspace,
		WorktreePath: workspace,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	if !RuntimeCapabilities(string(RuntimeNative), false).Compact {
		t.Fatal("expected RuntimeNative to advertise Compact: true")
	}

	userMsg := MessageInfo{ID: "msg_u1", SessionID: rec.ID, Role: "user"}
	storedParts := []MessagePart{{ID: "prt_u1", SessionID: rec.ID, MessageID: userMsg.ID, Type: "text", Text: "hi"}}
	deliveryParts := []MessagePart{
		{ID: "prt_u1", SessionID: rec.ID, MessageID: userMsg.ID, Type: "text", Text: "hi"},
		{ID: "prt_f1", SessionID: rec.ID, MessageID: userMsg.ID, Type: "file", Mime: "image/png", Filename: "ui.png", URL: "data:image/png;base64,aGVsbG8="},
	}
	if err := svc.startNativeTurn(rec, "code", "gpt-5", "", "", "hi", userMsg, storedParts, "msg_a1", deliveryParts...); err != nil {
		t.Fatalf("startNativeTurn failed: %v", err)
	}

	select {
	case req := <-capEng.gotReq:
		if req.CommandRunner == nil || req.Diagnostics == nil || req.PermissionHook == nil || req.AskQuestion == nil {
			t.Fatal("expected CommandRunner, Diagnostics, PermissionHook, and AskQuestion to be wired")
		}
		if len(req.UserBlocks) != 2 || req.UserBlocks[1].Type != "media" || req.UserBlocks[1].MimeType != "image/png" || req.UserBlocks[1].Data != "aGVsbG8=" {
			t.Fatalf("expected multimodal UserBlocks with decoded base64 media, got: %+v", req.UserBlocks)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for NativeEngine.RunTurn")
	}
	svc.CloseAll()

	// Verify POST .../summarize compacts the native session transcript via NativeEngine.Summarize.
	reqBody, _ := json.Marshal(map[string]string{"providerID": "openai", "modelID": "gpt-5"})
	httpReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+rec.ID+"/summarize", bytes.NewReader(reqBody))
	recW := httptest.NewRecorder()
	svc.handleSessionSummarize(recW, httpReq, rec.ID)
	if recW.Code != http.StatusOK || strings.TrimSpace(recW.Body.String()) != "true" {
		t.Fatalf("expected native summarize 200 true, got %d %q", recW.Code, recW.Body.String())
	}
	after, ok := svc.store.Get(rec.ID)
	if !ok || len(after.Turns) != 3 {
		t.Fatalf("expected non-destructive 3-turn transcript after compact, got: %+v", after.Turns)
	}
	if after.Turns[0].Info.ID != "msg_u1" || after.Turns[1].Info.ID != "msg_a1" {
		t.Fatalf("expected prior turns preserved for scrollback/revert, got: %+v", after.Turns)
	}
	ckpt := after.Turns[2]
	if ckpt.Info.Role != "assistant" || len(ckpt.Parts) != 2 || ckpt.Parts[0].Type != "compaction" || !strings.Contains(ckpt.Parts[1].Text, "Compacted Context Summary") {
		t.Fatalf("expected appended assistant compaction checkpoint, got: %+v", ckpt)
	}
	if u := ckpt.Info.Tokens; u == nil || u.Context == 0 || u.Output != 0 || u.Summary != nil {
		t.Fatalf("unmetered checkpoint should estimate context only: %+v", u)
	}
	projected := svc.projectCanonicalNativeHistory(after.Turns, "openai", "gpt-5")
	if len(projected) != 2 || !strings.Contains(projected[0].Content[0].Text, "<context_checkpoint>") {
		t.Fatalf("expected canonical history to project from after_turn checkpoint, got: %+v", projected)
	}
}

func TestNativeCommandAndDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test")
	}

	root := t.TempDir()
	workspace := filepath.Join(root, "repo")
	sibling := filepath.Join(root, "sibling")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}

	svc := NewService(workspace, nil, nil, filepath.Join(root, "sessions.json"))
	sandboxedRec := SessionRecord{
		ID:             "ses_native_sb",
		Runtime:        string(RuntimeNative),
		ProjectDir:     workspace,
		WorktreePath:   workspace,
		CommandSandbox: sandbox.Available(),
	}

	// Verify Seatbelt wrapping contract for native commands.
	if sandbox.Available() {
		bin, args, err := svc.confineRuntimeCommand(sandboxedRec, "/bin/sh", []string{"-c", "echo ok"})
		if err != nil {
			t.Fatalf("confineRuntimeCommand failed: %v", err)
		}
		if bin != "/usr/bin/sandbox-exec" || !strings.Contains(strings.Join(args, " "), "(deny default)") {
			t.Fatalf("expected Seatbelt wrapper, got %q %v", bin, args)
		}
	}

	execRec := sandboxedRec
	if execRec.CommandSandbox {
		probeOut, probeCode, probeErr := svc.nativeCommandRunner(execRec, nil)(context.Background(), workspace, "true")
		if probeErr != nil && probeCode == 71 && strings.Contains(probeOut, "sandbox_apply: Operation not permitted") {
			execRec.CommandSandbox = false
		}
	}

	runCmd := svc.nativeCommandRunner(execRec, nil)
	if _, code, err := runCmd(context.Background(), sibling, "true"); err == nil || code == 0 {
		t.Fatalf("expected non-workspace directory to be rejected, got code=%d err=%v", code, err)
	}
	if out, code, err := runCmd(context.Background(), workspace, "echo native-ok"); err != nil || code != 0 || strings.TrimSpace(out) != "native-ok" {
		t.Fatalf("expected workspace command to succeed: code=%d err=%v out=%q", code, err, out)
	}

	diagFn := svc.nativeDiagnostics(execRec)
	if goBin := resolveNativeToolBinary(execRec, "go"); goBin != "" {
		foreignWork := filepath.Join(sibling, "go.work")
		if err := os.WriteFile(foreignWork, []byte("go 1.22\n\nuse .\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sibling, "go.mod"), []byte("module foreign\n\ngo 1.22\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOWORK", foreignWork)
		if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module diagcheck\n\ngo 1.22\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		pkgDir := filepath.Join(workspace, "calc")
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		goFile := filepath.Join(pkgDir, "calc.go")
		badCode := "package calc\n\nimport \"fmt\"\n\nfunc FormatCount() string {\n\treturn fmt.Sprintf(\"%d\", \"text\")\n}\n"
		if err := os.WriteFile(goFile, []byte(badCode), 0o644); err != nil {
			t.Fatal(err)
		}
		gotDiag := diagFn(context.Background(), workspace, goFile)
		if !strings.Contains(gotDiag, "[go vet ./calc]") || !strings.Contains(gotDiag, "calc.go") {
			t.Fatalf("expected go vet diagnostic, got:\n%s", gotDiag)
		}

		goodCode := "package calc\n\nimport \"fmt\"\n\nfunc FormatCount() string {\n\treturn fmt.Sprintf(\"%d\", 1)\n}\n"
		if err := os.WriteFile(goFile, []byte(goodCode), 0o644); err != nil {
			t.Fatal(err)
		}
		if clean := diagFn(context.Background(), workspace, goFile); clean != "Passed: go vet ./calc" {
			t.Fatalf("expected clean diagnostic after fix, got:\n%s", clean)
		}

		// The same package argument is ambiguous when run from a nested module.
		if err := os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module calc\n\ngo 1.22\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if clean := diagFn(context.Background(), workspace, goFile); clean != "Passed: go vet . (module calc/go.mod)" {
			t.Fatalf("expected nested module root in diagnostic, got %q", clean)
		}
	}

	rawTSC := "src/other.ts(9,1): error TS2304: Cannot find name x.\nsrc/Widget.tsx(4,7): error TS2322: Type string is not assignable to number.\n"
	formatted := formatDiagnosticLines(rawTSC, "src/Widget.tsx", 35, 4096)
	if strings.Index(formatted, "src/Widget.tsx(4,7)") > strings.Index(formatted, "src/other.ts(9,1)") {
		t.Fatalf("expected edited file diagnostic prioritized first:\n%s", formatted)
	}
}

type subagentNativeEngine struct {
	done chan struct{}
}

func (s *subagentNativeEngine) RunTurn(_ context.Context, req NativeTurnRequest) error {
	defer close(s.done)
	if req.Emit == nil {
		return nil
	}
	req.Emit(NativeEvent{
		Kind:     NativeEventToolRunning,
		PartID:   "prt_task_1",
		CallID:   "call_sub_1",
		ToolName: "task",
		Title:    "Audit auth layer",
		Input:    map[string]any{"description": "Audit auth layer", "prompt": "Check token verification"},
	})
	req.Emit(NativeEvent{
		Kind:         NativeEventSubtaskStart,
		ParentCallID: "call_sub_1",
		Title:        "Audit auth layer",
		Text:         "Check token verification",
	})
	req.Emit(NativeEvent{
		Kind:         NativeEventToolRunning,
		ParentCallID: "call_sub_1",
		PartID:       "child_prt_1",
		CallID:       "child_call_1",
		ToolName:     "read",
		Input:        map[string]any{"filePath": "auth.go"},
	})
	req.Emit(NativeEvent{
		Kind:         NativeEventToolCompleted,
		ParentCallID: "call_sub_1",
		CallID:       "child_call_1",
		ToolName:     "read",
		Text:         "package auth",
	})
	req.Emit(NativeEvent{
		Kind:         NativeEventTextEnd,
		ParentCallID: "call_sub_1",
		PartID:       "child_txt_1",
		Text:         "Auth layer verified.",
	})
	req.Emit(NativeEvent{
		Kind:         NativeEventSubtaskEnd,
		ParentCallID: "call_sub_1",
	})
	req.Emit(NativeEvent{
		Kind:     NativeEventToolCompleted,
		PartID:   "prt_task_1",
		CallID:   "call_sub_1",
		ToolName: "task",
		Title:    "Audit auth layer",
		Text:     "Auth layer verified.",
	})
	req.Emit(NativeEvent{Kind: NativeEventTextStart, PartID: "prt_parent_txt"})
	req.Emit(NativeEvent{Kind: NativeEventTextEnd, PartID: "prt_parent_txt", Text: "Completed audit."})
	return nil
}

func (s *subagentNativeEngine) Summarize(_ context.Context, _, _, transcript string) (string, error) {
	return transcript, nil
}

func TestNativeSubagentBridgeCreatesChildSessionAndMetadata(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(workspace, nil, nil, filepath.Join(root, "sessions.json"))
	eng := &subagentNativeEngine{done: make(chan struct{})}
	svc.SetNativeEngine(eng)

	rec := SessionRecord{
		ID:           "ses_parent_sub",
		Runtime:      string(RuntimeNative),
		ProviderID:   "openai",
		ModelID:      "gpt-5",
		ProjectDir:   workspace,
		WorktreePath: workspace,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	userMsg := MessageInfo{ID: "msg_u_sub", SessionID: rec.ID, Role: "user"}
	storedParts := []MessagePart{{ID: "prt_u_sub", SessionID: rec.ID, MessageID: userMsg.ID, Type: "text", Text: "audit"}}
	if err := svc.startNativeTurn(rec, "code", "gpt-5", "", "", "audit", userMsg, storedParts, "msg_a_sub", storedParts...); err != nil {
		t.Fatalf("startNativeTurn failed: %v", err)
	}
	select {
	case <-eng.done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for subagentNativeEngine")
	}
	svc.CloseAll()

	parentRec, ok := svc.store.Get(rec.ID)
	if !ok || len(parentRec.Turns) < 2 {
		t.Fatalf("expected parent turns persisted, got: %+v", parentRec.Turns)
	}
	var childSessionID string
	for _, p := range parentRec.Turns[1].Parts {
		if p.Type == "tool" && p.Tool == "task" && p.State != nil && p.State.Metadata != nil {
			if sid, _ := p.State.Metadata["sessionId"].(string); sid != "" {
				childSessionID = sid
			}
		}
	}
	if childSessionID == "" {
		t.Fatalf("expected parent task part metadata.sessionId to link child session, got parts: %+v", parentRec.Turns[1].Parts)
	}
	childRec, ok := svc.store.Get(childSessionID)
	if !ok || childRec.ParentID != rec.ID || len(childRec.Turns) < 2 {
		t.Fatalf("expected child session linked to parent %s, got: %+v", rec.ID, childRec)
	}
}

type retryAndCleanupNativeEngine struct {
	done      chan struct{}
	forgotten chan string
}

func (e *retryAndCleanupNativeEngine) RunTurn(_ context.Context, req NativeTurnRequest) error {
	defer close(e.done)
	if req.Emit != nil {
		req.Emit(NativeEvent{
			Kind:      NativeEventStepRetry,
			Step:      1,
			ErrorText: "HTTP 429: rate_limit_error",
			Metadata:  map[string]any{"attempt": 2, "next": int64(1790000012345)},
		})
		req.Emit(NativeEvent{Kind: NativeEventStepRetryDone, Step: 1})
		req.Emit(NativeEvent{Kind: NativeEventTextStart, PartID: "prt_1"})
		req.Emit(NativeEvent{Kind: NativeEventTextEnd, PartID: "prt_1", Text: "done"})
	}
	return nil
}

func (e *retryAndCleanupNativeEngine) Summarize(_ context.Context, _, _, _ string) (string, error) {
	return "ok", nil
}

func (e *retryAndCleanupNativeEngine) ForgetSession(sessionID string) {
	select {
	case e.forgotten <- sessionID:
	default:
	}
}

func TestNativeStepRetryBroadcastsStatusAndDeleteCleansSessionState(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(workspace, nil, nil, filepath.Join(root, "sessions.json"))
	eng := &retryAndCleanupNativeEngine{
		done:      make(chan struct{}),
		forgotten: make(chan string, 2),
	}
	svc.SetNativeEngine(eng)

	rec := SessionRecord{
		ID:           "ses_nat_retry_1",
		Runtime:      string(RuntimeNative),
		ProviderID:   "anthropic",
		ModelID:      "claude-sonnet-4-5",
		ProjectDir:   workspace,
		WorktreePath: workspace,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	ch, unsub := svc.Subscribe(rec.ID)
	defer unsub()

	userMsg := MessageInfo{ID: "msg_u_r1", SessionID: rec.ID, Role: "user"}
	storedParts := []MessagePart{{ID: "prt_u_r1", SessionID: rec.ID, MessageID: userMsg.ID, Type: "text", Text: "hi"}}
	if err := svc.startNativeTurn(rec, "build", "claude-sonnet-4-5", "", "", "hi", userMsg, storedParts, "msg_a_r1", storedParts...); err != nil {
		t.Fatalf("startNativeTurn failed: %v", err)
	}

	var sawRetryStatus, sawBusyAfterRetry, sawIdle bool
	deadline := time.After(3 * time.Second)
	for !sawIdle {
		select {
		case evt := <-ch:
			if evt.Type == "session.status" {
				st, ok := parseSessionStatus(evt.Properties["status"])
				if !ok {
					continue
				}
				switch st.Type {
				case "retry":
					if st.Attempt == 2 && st.Next == 1790000012345 && strings.Contains(st.Message, "429") {
						sawRetryStatus = true
					}
				case "busy":
					if sawRetryStatus {
						sawBusyAfterRetry = true
					}
				case "idle":
					sawIdle = true
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for idle status (sawRetry=%v sawBusyAfterRetry=%v)", sawRetryStatus, sawBusyAfterRetry)
		}
	}
	if !sawRetryStatus || !sawBusyAfterRetry {
		t.Fatalf("expected retry -> busy -> idle status sequence, got retry=%v busyAfterRetry=%v", sawRetryStatus, sawBusyAfterRetry)
	}

	if err := svc.DeleteConversation(context.Background(), rec.ID); err != nil {
		t.Fatalf("DeleteConversation failed: %v", err)
	}
	select {
	case gotID := <-eng.forgotten:
		if gotID != rec.ID {
			t.Fatalf("expected ForgetSession(%q), got %q", rec.ID, gotID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected DeleteConversation to invoke ForgetSession on nativeEngine")
	}
	svc.CloseAll()
}

type costAndTodoNativeEngine struct {
	gotReq chan NativeTurnRequest
}

func (e *costAndTodoNativeEngine) RunTurn(_ context.Context, req NativeTurnRequest) error {
	e.gotReq <- req
	if req.Emit != nil {
		req.Emit(NativeEvent{
			Kind:         NativeEventTurnUsage,
			ParentCallID: "call_sub_1",
			Usage:        &NativeTokenUsage{Input: 1000, Output: 500, CostUSD: 0.0125},
		})
		req.Emit(NativeEvent{
			Kind:  NativeEventTurnUsage,
			Usage: &NativeTokenUsage{Input: 4000, Output: 1000, CostUSD: 0.0375},
		})
		req.Emit(NativeEvent{Kind: NativeEventTextStart, PartID: "prt_txt_cost"})
		req.Emit(NativeEvent{Kind: NativeEventTextEnd, PartID: "prt_txt_cost", Text: "Completed with cost"})
		req.Emit(NativeEvent{Kind: NativeEventTurnFinish, Finish: "stop"})
	}
	return nil
}

func (e *costAndTodoNativeEngine) Summarize(_ context.Context, _, _, _ string) (string, error) {
	return "ok", nil
}

func TestNativeTurnPropagatesCostSeedsTodosAndPermissions(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(root, "sessions.json"))
	eng := &costAndTodoNativeEngine{gotReq: make(chan NativeTurnRequest, 1)}
	svc.SetNativeEngine(eng)

	rec := SessionRecord{
		ID:           "ses_nat_cost_todo",
		Runtime:      string(RuntimeNative),
		ProviderID:   "anthropic",
		ModelID:      "claude-sonnet-4-5",
		ProjectDir:   root,
		WorktreePath: root,
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_prev_u", SessionID: "ses_nat_cost_todo", Role: "user"},
				Parts: []MessagePart{{ID: "prt_prev_u", Type: "text", Text: "start plan"}},
			},
			{
				Info: MessageInfo{ID: "msg_prev_a", SessionID: "ses_nat_cost_todo", Role: "assistant"},
				Parts: []MessagePart{{
					ID:   "prt_prev_todo",
					Type: "tool",
					Tool: "todowrite",
					State: &ToolState{
						Status: "completed",
						Metadata: map[string]interface{}{
							"todos": []map[string]interface{}{
								{"id": "t1", "content": "Audit capability gaps", "status": "completed"},
								{"id": "t2", "content": "Wire cost & streaming", "status": "in_progress"},
							},
						},
					},
				}},
			},
		},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	// Verify hoxi_data list action is allowed read-only in Smart mode while http_request requires approval
	if _, err := svc.UpdateApprovalPolicy(ApprovalPolicy{Mode: ApprovalModeSmart}); err != nil {
		t.Fatal(err)
	}
	permHook := svc.nativePermissionHook(rec, "msg_a_perm")
	if err := permHook(context.Background(), NativePermissionRequest{
		SessionID: rec.ID,
		CallID:    "call_data_list",
		ToolName:  "hoxi_data",
		Input:     json.RawMessage(`{"action":"list"}`),
		IsRead:    true,
	}); err != nil {
		t.Fatalf("expected hoxi_data action=list to be allowed without approval prompt, got: %v", err)
	}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := permHook(canceledCtx, NativePermissionRequest{
		SessionID: rec.ID,
		CallID:    "call_data_http",
		ToolName:  "hoxi_data",
		Input:     json.RawMessage(`{"action":"http_request","profile":"prod","path":"/users"}`),
	}); err == nil {
		t.Fatal("expected hoxi_data action=http_request to require approval gate and fail when context is canceled")
	}

	userMsg := MessageInfo{ID: "msg_u2", SessionID: rec.ID, Role: "user", ContinueTask: true}
	storedParts := []MessagePart{{ID: "prt_u2", SessionID: rec.ID, MessageID: userMsg.ID, Type: "text", Text: "continue"}}
	if err := svc.startNativeTurn(rec, "code", "claude-sonnet-4-5", "", "", "continue", userMsg, storedParts, "msg_a2"); err != nil {
		t.Fatalf("startNativeTurn: %v", err)
	}

	select {
	case req := <-eng.gotReq:
		if len(req.InitialTodos) != 2 || req.InitialTodos[1].Content != "Wire cost & streaming" {
			t.Fatalf("expected InitialTodos seeded from persisted todowrite turn, got: %+v", req.InitialTodos)
		}
		if req.StreamCommandRunner == nil {
			t.Fatal("expected StreamCommandRunner to be wired")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for RunTurn")
	}
	svc.CloseAll()

	after, ok := svc.store.Get(rec.ID)
	if !ok || len(after.Turns) != 4 {
		t.Fatalf("expected 4 turns in store, got: %+v", after.Turns)
	}
	gotCost := after.Turns[3].Info.Cost
	if gotCost < 0.049999 || gotCost > 0.050001 {
		t.Fatalf("expected accumulated parent+subagent Cost=0.0500, got %.6f", gotCost)
	}
}

func TestNativeTurnsReplayStablePrefixAndDeliverHandoffMemory(t *testing.T) {
	ws := t.TempDir()
	p := hoxipaths.ProjectMemoryFile(ws)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("Always preserve integer rounding"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ws, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	defer svc.CloseAll()
	eng := &captureNativeEngine{gotReq: make(chan NativeTurnRequest, 1)}
	svc.SetNativeEngine(eng)
	session := createSandboxSession(t, svc, string(RuntimeNative), ws, "local")
	ch, unsub := svc.Subscribe(session.ID)
	defer unsub()
	var firstSystem string
	var previous []NativeMessage
	for i, prompt := range []string{"Inspect arithmetic helpers", "Check division", "Explain rounding", "Continue handoff", "Check receipt", "Read my note", "Check it again"} {
		const note = "Always use half-even rounding for this service"
		if i == 5 {
			if _, _, err := memstore.SaveMemoryDocument(ws, "session", session.ID, note); err != nil {
				t.Fatal(err)
			}
		}
		if i == 3 {
			_, ok, err := svc.store.UpdateMetadata(session.ID, func(r *SessionMetadata) { r.ContextPending = true })
			if err != nil || !ok {
				t.Fatalf("mark handoff pending: %v", err)
			}
		}
		code, err := svc.startPrompt(context.Background(), session.ID, ws, PromptRequest{Text: prompt, Model: &PromptModel{ProviderID: "offline", ModelID: "offline"}})
		if err != nil || code != http.StatusOK {
			t.Fatalf("start: %d %v", code, err)
		}
		select {
		case req := <-eng.gotReq:
			if i == 0 {
				firstSystem = req.System
			}
			if req.System != firstSystem || !strings.Contains(req.System, "Always preserve integer rounding") || strings.Contains(req.System, "<session_handoff_memory>") {
				t.Fatal("native system prefix changed or project guidance is missing")
			}
			if len(req.UserBlocks) == 0 || req.UserBlocks[len(req.UserBlocks)-1].Text != prompt {
				t.Fatalf("user prompt lost: %+v", req.UserBlocks)
			}
			if len(req.History) != 2*i {
				t.Fatalf("turn %d: lost history", i+1)
			}
			if i == 3 {
				if len(req.UserBlocks) != 2 || !strings.Contains(req.UserBlocks[0].Text, "<session_handoff_memory>") {
					t.Fatal("missing handoff")
				}
			} else if len(req.UserBlocks) != 1 {
				t.Fatalf("turn %d repeated an automatic checkpoint", i+1)
			}
			if i >= 5 && (len(req.History[0].Content) != 2 || !strings.Contains(req.History[0].Content[0].Text, note)) {
				t.Fatal("missing authored note on the first historical user message")
			}
			if ((i > 0 && i <= 3) || i == 6) && !reflect.DeepEqual(previous, req.History[:len(previous)]) {
				t.Fatalf("turn %d changed its predecessor's request prefix", i+1)
			}
			previous = append(append([]NativeMessage(nil), req.History...), NativeMessage{Role: "user", Content: req.UserBlocks})

		case <-time.After(4 * time.Second):
			t.Fatal("turn did not start")
		}
		waitForRunnerSessionIdle(t, ch)
	}
}
