package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestNativeErrorToolMediaSurvivesHistoryReplay(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	defer svc.CloseAll()
	rec := SessionRecord{
		ID: "ses_mcp_error_media", Runtime: string(RuntimeNative), ProviderID: "selected", ModelID: "selected-model",
		ProjectDir: root, WorktreePath: root,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	svc.SetNativeEngine(&scriptedNativeEngine{runFn: func(_ context.Context, req NativeTurnRequest) error {
		req.Emit(NativeEvent{
			Kind: NativeEventToolError, Step: 1, PartID: "prt_error", CallID: "call_mcp", ToolName: "mcp_call_tool",
			ErrorText: "Screenshot captured but action failed", Input: map[string]any{"server": "browser", "tool": "capture"},
			Metadata: map[string]any{"mcpResult": json.RawMessage(`{"isError":true,"structuredContent":{"reason":"failed"}}`)},
			Media:    []NativeContentBlock{{Type: "media", MimeType: "image/png", Data: "iVBORw0KGgo=", Filename: "error.png"}},
		})
		return nil
	}})
	ch, unsub := svc.Subscribe(rec.ID)
	defer unsub()
	user := MessageInfo{ID: "msg_u", SessionID: rec.ID, Role: "user"}
	if err := svc.startNativeTurn(rec, "build", rec.ModelID, "", "", "inspect failure", user, nil, "msg_a"); err != nil {
		t.Fatal(err)
	}
	waitForRunnerSessionIdle(t, ch)
	stored, ok := svc.store.Get(rec.ID)
	if !ok {
		t.Fatal("missing session")
	}
	foundMedia, foundError, foundMetadata := false, false, false
	for _, turn := range stored.Turns {
		for _, part := range turn.Parts {
			if part.State != nil && part.State.Metadata["mcpResult"] != nil {
				foundMetadata = true
			}
		}
	}
	for _, msg := range svc.projectCanonicalNativeHistory(stored.Turns, rec.ProviderID, rec.ModelID) {
		for _, block := range msg.Content {
			if block.Type == "media" && block.Data == "iVBORw0KGgo=" {
				foundMedia = true
			}
			if block.Type == "tool_result" && block.ToolUseID == "call_mcp" && block.IsError {
				foundError = true
			}
		}
	}
	if !foundMedia || !foundError || !foundMetadata {
		t.Fatalf("tool replay lost result: media=%t error=%t metadata=%t", foundMedia, foundError, foundMetadata)
	}
}

func TestNativeChildToolMediaAndMetadataSurviveReplay(t *testing.T) {
	for _, kind := range []string{NativeEventToolCompleted, NativeEventToolError} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))
			defer svc.CloseAll()
			parent := SessionRecord{ID: "ses_parent", Runtime: string(RuntimeNative), ProviderID: "selected", ModelID: "selected-model", WorktreePath: root, ProjectDir: root}
			if err := svc.store.Put(parent); err != nil {
				t.Fatal(err)
			}
			tr := NewTurnTranslator(parent.ID, string(RuntimeNative), "msg_a", "msg_u", parent.ProviderID, parent.ModelID, "build", "")
			tr.CreateChild = svc.childSessionFactory(parent, parent.ModelID, "plan")
			bridge := newNativeSubagentBridge(svc, parent.ID, parent.ModelID, tr)
			send := func(evt NativeEvent) {
				evt.ParentCallID = "task-1"
				svc.broadcastRouted(parent.ID, bridge.handleChildEvent(evt, 100))
			}
			send(NativeEvent{Kind: NativeEventSubtaskStart, Title: "Read report", Text: "inspect"})
			send(NativeEvent{Kind: NativeEventToolRunning, Step: 1, PartID: "prt_child", CallID: "call_child", ToolName: "mcp_call_tool"})
			send(NativeEvent{
				Kind: kind, Step: 1, PartID: "prt_child", CallID: "call_child", ToolName: "mcp_call_tool", Text: "Report evidence",
				Metadata: map[string]any{"mcpResult": json.RawMessage(`{"structuredContent":{"answer":42}}`)},
				Media:    []NativeContentBlock{{Type: "media", MimeType: "image/png", Data: "iVBORw0KGgo=", Filename: "child.png"}},
			})
			send(NativeEvent{Kind: NativeEventSubtaskEnd})
			ids := tr.ChildSessionIDs()
			if len(ids) != 1 {
				t.Fatalf("expected child session: %v", ids)
			}
			rec, ok := svc.store.Get(ids[0])
			if !ok {
				t.Fatal("missing child session")
			}
			foundMetadata, foundMedia, foundResult := false, false, false
			for _, turn := range rec.Turns {
				for _, part := range turn.Parts {
					if part.State != nil && part.State.Metadata["mcpResult"] != nil {
						foundMetadata = true
					}
				}
			}
			for _, msg := range svc.projectCanonicalNativeHistory(rec.Turns, parent.ProviderID, parent.ModelID) {
				for _, block := range msg.Content {
					foundMedia = foundMedia || (block.Type == "media" && block.Data == "iVBORw0KGgo=")
					foundResult = foundResult || (block.Type == "tool_result" && block.IsError == (kind == NativeEventToolError))
				}
			}
			if !foundMedia || !foundMetadata || !foundResult {
				t.Fatalf("child replay lost evidence: media=%t metadata=%t result=%t", foundMedia, foundMetadata, foundResult)
			}
		})
	}
}

func TestLocalSessionDiffAndRevertWithNativeApplyPatchAndMultiEdit(t *testing.T) {
	repo := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "move_me.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "multi.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "legacy.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeSnap, err := worktree.CreateSnapshot(repo)
	if err != nil || beforeSnap == "" {
		t.Fatalf("CreateSnapshot before: snap=%q err=%v", beforeSnap, err)
	}

	// Simulate native apply_patch (update + move + add) + multiedit + legacy string metadata + unrelated user file.
	_ = os.Remove(filepath.Join(repo, "move_me.txt"))
	if err := os.WriteFile(filepath.Join(repo, "moved.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "added.txt"), []byte("created\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "multi.txt"), []byte("ALPHA\nBETA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "legacy.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(repo, "user_scratch.txt")
	if err := os.WriteFile(unrelated, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	userIdeEdited := filepath.Join(repo, "user_ide_edit.txt")
	if err := os.WriteFile(userIdeEdited, []byte("user ide change\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(repo, worktree.NewManager(repo), nil, filepath.Join(t.TempDir(), "sessions"))
	defer svc.CloseAll()

	rec := SessionRecord{
		ID:           "ses_nat_local_patch",
		Runtime:      string(RuntimeNative),
		ProjectDir:   repo,
		WorktreePath: repo,
		SandboxMode:  "local",
		Snapshots:    map[string]string{"msg_u1": beforeSnap},
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_u1", SessionID: "ses_nat_local_patch", Role: "user"},
				Parts: []MessagePart{{ID: "prt_u1", Type: "text", Text: "patch files"}},
			},
			{
				Info: MessageInfo{ID: "msg_a1", SessionID: "ses_nat_local_patch", Role: "assistant", ParentID: "msg_u1"},
				Parts: []MessagePart{
					{
						ID: "prt_ap", Type: "tool", Tool: "apply_patch", CallID: "call_ap",
						State: &ToolState{
							Status: "completed",
							Input: map[string]interface{}{
								"patch": "*** Begin Patch\n*** Add File: added.txt\n+created\n*** Update File: move_me.txt\n*** Move to: moved.txt\n@@\n-v1\n+v2\n*** End Patch",
							},
							Metadata: map[string]interface{}{
								"files": []interface{}{
									map[string]interface{}{"filePath": filepath.Join(repo, "added.txt"), "relativePath": "added.txt", "type": "add"},
									map[string]interface{}{"filePath": filepath.Join(repo, "move_me.txt"), "movePath": filepath.Join(repo, "moved.txt"), "relativePath": "moved.txt", "type": "move"},
								},
							},
						},
					},
					{
						ID: "prt_me", Type: "tool", Tool: "multiedit", CallID: "call_me",
						State: &ToolState{
							Status: "completed",
							Input:  map[string]interface{}{"filePath": "multi.txt"},
							Metadata: map[string]interface{}{
								"filepath": filepath.Join(repo, "multi.txt"),
								"filediff": map[string]interface{}{"file": "multi.txt", "before": "alpha\nbeta\n", "after": "ALPHA\nBETA\n"},
							},
						},
					},
					{
						ID: "prt_leg", Type: "tool", Tool: "apply_patch", CallID: "call_leg",
						State: &ToolState{
							Status:   "completed",
							Input:    map[string]interface{}{"diff": "--- a/legacy.txt\n+++ b/legacy.txt\n@@ -1 +1 @@\n-before\n+after\n"},
							Metadata: map[string]interface{}{"files": []interface{}{"M legacy.txt"}},
						},
					},
					{
						ID: "prt_err_patch", Type: "tool", Tool: "apply_patch", CallID: "call_err_patch",
						State: &ToolState{
							Status: "error",
							Error:  "hunk mismatch",
							Input: map[string]interface{}{
								"patch": "*** Begin Patch\n*** Update File: user_ide_edit.txt\n@@\n-old\n+bad\n*** End Patch",
							},
						},
					},
					{
						ID: "prt_running_me", Type: "tool", Tool: "multiedit", CallID: "call_running_me",
						State: &ToolState{
							Status: "running",
							Input:  map[string]interface{}{"filePath": "user_ide_edit.txt"},
						},
					},
				},
			},
		},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	// 1. Local session diff must include session-edited files and exclude unrelated/failed-tool files.
	diffReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/sessions/"+rec.ID+"/diff?directory="+repo, nil)
	diffRec := httptest.NewRecorder()
	svc.handleSessionDiff(diffRec, diffReq, rec.ID)
	if diffRec.Code != http.StatusOK {
		t.Fatalf("handleSessionDiff status=%d body=%s", diffRec.Code, diffRec.Body.String())
	}
	diffBody := diffRec.Body.String()
	for _, want := range []string{"added.txt", "moved.txt", "multi.txt", "legacy.txt"} {
		if !strings.Contains(diffBody, want) {
			t.Fatalf("expected %q in local session diff, got: %s", want, diffBody)
		}
	}
	for _, unwanted := range []string{"user_scratch.txt", "user_ide_edit.txt"} {
		if strings.Contains(diffBody, unwanted) {
			t.Fatalf("unattributed %s leaked into local session diff: %s", unwanted, diffBody)
		}
	}

	// 2. Local session revert must restore completed edits while preserving user_scratch.txt and user_ide_edit.txt.
	revReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/sessions/"+rec.ID+"/revert", strings.NewReader(`{"messageID":"msg_u1"}`))
	revRec := httptest.NewRecorder()
	svc.handleSessionRevert(revRec, revReq, rec.ID)
	if revRec.Code != http.StatusOK {
		t.Fatalf("handleSessionRevert status=%d body=%s", revRec.Code, revRec.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(repo, "move_me.txt")); err != nil || string(b) != "v1\n" {
		t.Fatalf("expected move_me.txt restored to 'v1\\n', got err=%v body=%q", err, string(b))
	}
	for _, gone := range []string{"added.txt", "moved.txt"} {
		if _, err := os.Stat(filepath.Join(repo, gone)); !os.IsNotExist(err) {
			t.Fatalf("expected %s removed on revert, stat err=%v", gone, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(repo, "multi.txt")); err != nil || string(b) != "alpha\nbeta\n" {
		t.Fatalf("expected multi.txt restored, got err=%v body=%q", err, string(b))
	}
	if b, err := os.ReadFile(filepath.Join(repo, "legacy.txt")); err != nil || string(b) != "before\n" {
		t.Fatalf("expected legacy.txt restored, got err=%v body=%q", err, string(b))
	}
	if b, err := os.ReadFile(unrelated); err != nil || string(b) != "keep me\n" {
		t.Fatalf("expected unrelated user_scratch.txt preserved, got err=%v body=%q", err, string(b))
	}
	if b, err := os.ReadFile(userIdeEdited); err != nil || string(b) != "user ide change\n" {
		t.Fatalf("expected failed/running tool target user_ide_edit.txt preserved, got err=%v body=%q", err, string(b))
	}
}

func TestNativeHistoryCapsEphemeralScreenshotsWhilePreservingOtherMedia(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	defer svc.CloseAll()

	turns := []MessageTurn{
		{
			Info: MessageInfo{ID: "msg_u1", SessionID: "ses_shots", Role: "user"},
			Parts: []MessagePart{
				{ID: "prt_u1", Type: "text", Text: "Watch Gemini"},
				{ID: "prt_uimg", Type: "file", URL: "data:image/png;base64,VVNFUl9JTUc=", Mime: "image/png", Filename: "user.png"},
			},
		},
		{
			Info: MessageInfo{ID: "msg_a1", SessionID: "ses_shots", Role: "assistant", ParentID: "msg_u1"},
			Parts: []MessagePart{
				{
					ID: "prt_mcp", Type: "tool", Tool: "mcp_call_tool", CallID: "c_mcp",
					State: &ToolState{
						Status: "completed", Output: "ok",
						Metadata: map[string]interface{}{
							"step": 1,
							"mediaAttachments": []map[string]interface{}{
								{"url": "data:image/png;base64,TUNQX0lNRw==", "mime": "image/png", "filename": "mcp.png"},
							},
						},
					},
				},
			},
		},
	}
	for i := 1; i <= 5; i++ {
		validData := []string{"U0hPVF8x", "U0hPVF8y", "U0hPVF8z", "U0hPVF80", "U0hPVF81"}[i-1]
		turns[1].Parts = append(turns[1].Parts, MessagePart{
			ID:     "prt_shot_" + string(rune('0'+i)),
			Type:   "tool",
			Tool:   "hoxi_computer",
			CallID: "c_shot_" + string(rune('0'+i)),
			State: &ToolState{
				Status: "completed",
				Input:  map[string]interface{}{"action": "screenshot"},
				Output: `{"windowID":"w1"}`,
				Metadata: map[string]interface{}{
					"step": i + 1,
					"mediaAttachments": []map[string]interface{}{
						{"url": "data:image/jpeg;base64," + validData, "mime": "image/jpeg", "filename": "hoxi_computer.jpg"},
					},
				},
			},
		})
	}

	projected := svc.projectCanonicalNativeHistory(turns, "openai", "gpt-5")
	var liveShots []string
	var omittedShots int
	var hasUserImg, hasMCPImg bool
	for _, m := range projected {
		for _, b := range m.Content {
			if b.Type == "media" {
				switch b.Filename {
				case "user.png":
					hasUserImg = true
				case "mcp.png":
					hasMCPImg = true
				case "hoxi_computer.jpg":
					liveShots = append(liveShots, b.Data)
				}
			}
			if b.Type == "text" && strings.Contains(b.Text, "[omitted earlier screenshot: hoxi_computer.jpg (image/jpeg)]") {
				omittedShots++
			}
		}
	}
	if !hasUserImg || !hasMCPImg {
		t.Fatalf("expected user and MCP media preserved, got user=%v mcp=%v", hasUserImg, hasMCPImg)
	}
	if omittedShots != 3 || len(liveShots) != 2 {
		t.Fatalf("expected 3 omitted and 2 live hoxi_computer screenshots, got omitted=%d live=%v", omittedShots, liveShots)
	}
}
