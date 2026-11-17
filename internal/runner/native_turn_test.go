package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
	if !ok || len(after.Turns) != 2 || !strings.Contains(after.Turns[1].Parts[0].Text, "Compacted Context Summary") {
		t.Fatalf("expected compacted 2-turn checkpoint in store, got: %+v", after.Turns)
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
		if clean := diagFn(context.Background(), workspace, goFile); clean != "" {
			t.Fatalf("expected clean diagnostic after fix, got:\n%s", clean)
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
