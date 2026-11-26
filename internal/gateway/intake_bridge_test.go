package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hoxiai/hoxi/internal/intake"
	"github.com/hoxiai/hoxi/internal/runner"
)

func TestIntakePortalIsolationAndSanitizedTimeline(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOXI_HOME", filepath.Join(tmp, "hoxi-home"))
	proj := filepath.Join(tmp, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	distFS := fstest.MapFS{
		"index.html":        &fstest.MapFile{Data: []byte("<!doctype html><html><body>Hoxi QA</body></html>")},
		"assets/app-123.js": &fstest.MapFile{Data: []byte("console.log('bundle');")},
		"secret.txt":        &fstest.MapFile{Data: []byte("not-an-asset")},
	}
	gw := New(0, "test-pass", proj, distFS, nil)
	t.Cleanup(gw.CloseBrowser)

	// 1. Desktop creates a tester intake link for proj.
	createBody, _ := json.Marshal(intake.CreateLinkInput{
		Directory: proj,
		Label:     "兼职测试小张",
	})
	linkReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/hoxi/intake/links", bytes.NewReader(createBody))
	linkRec := httptest.NewRecorder()
	gw.ServeHTTP(linkRec, linkReq)
	if linkRec.Code != http.StatusOK {
		t.Fatalf("desktop link create failed: %d %s", linkRec.Code, linkRec.Body.String())
	}
	var pubLink intake.PublicLink
	if err := json.Unmarshal(linkRec.Body.Bytes(), &pubLink); err != nil {
		t.Fatal(err)
	}
	qaToken := strings.TrimPrefix(pubLink.PortalPath, "/qa/")

	// 2. Ingress (:5418 Cloudflare Tunnel) serves /qa/<token> and /assets/*,
	//    but refuses arbitrary static paths (/secret.txt) and malformed /qa/ paths.
	pageReq := httptest.NewRequest(http.MethodGet, "https://qa.example.com"+pubLink.PortalPath, nil)
	pageRec := httptest.NewRecorder()
	gw.ServeIngressRequest(pageRec, pageReq)
	if pageRec.Code != http.StatusOK || !strings.Contains(pageRec.Body.String(), "Hoxi QA") {
		t.Fatalf("expected /qa/<token> SPA shell 200, got %d %s", pageRec.Code, pageRec.Body.String())
	}

	assetReq := httptest.NewRequest(http.MethodGet, "https://qa.example.com/assets/app-123.js", nil)
	assetRec := httptest.NewRecorder()
	gw.ServeIngressRequest(assetRec, assetReq)
	if assetRec.Code != http.StatusOK || !strings.Contains(assetRec.Body.String(), "bundle") {
		t.Fatalf("expected /assets/* 200, got %d", assetRec.Code)
	}

	nonAssetReq := httptest.NewRequest(http.MethodGet, "https://qa.example.com/secret.txt", nil)
	nonAssetRec := httptest.NewRecorder()
	gw.ServeIngressRequest(nonAssetRec, nonAssetReq)
	if nonAssetRec.Code == http.StatusOK {
		t.Fatalf("non-asset path must not be exposed publicly on ingress")
	}

	// 3. Tester token CANNOT reach desktop intake management, /file/content, /sessions, or /pty over Ingress or LAN!
	forbiddenPaths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/hoxi/intake/links"},
		{http.MethodGet, "/api/hoxi/intake/tasks"},
		{http.MethodPost, "/api/hoxi/intake/tasks/approve"},
		{http.MethodGet, "/api/hoxi/file/content?path=" + proj + "/main.go"},
		{http.MethodGet, "/api/hoxi/sessions"},
		{http.MethodPost, "/api/hoxi/pty"},
	}
	for _, tc := range forbiddenPaths {
		req := httptest.NewRequest(tc.method, "https://qa.example.com"+tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+qaToken)
		req.Header.Set("X-Hoxi-Intake-Token", qaToken)
		rec := httptest.NewRecorder()
		gw.ServeIngressRequest(rec, req)
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Fatalf("tester token must be rejected on %s %s, got %d", tc.method, tc.path, rec.Code)
		}
	}

	// GoPanel tunnel callers also get 403 on intake routes.
	tunRec := httptest.NewRecorder()
	gw.serveTunnelRequest(tunRec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/hoxi/intake/links", nil))
	if tunRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for tunnel caller on /api/hoxi/intake/links, got %d", tunRec.Code)
	}

	// 4. Tester submits staged task via Ingress -> succeeds and stays staged.
	submitBody, _ := json.Marshal(intake.SubmitTaskInput{
		Title:       "按钮点击无响应",
		Description: "在登录弹窗点确认按钮没有反应",
	})
	subReq := httptest.NewRequest(http.MethodPost, "https://qa.example.com/api/hoxi/intake/portal/tasks", bytes.NewReader(submitBody))
	subReq.Header.Set("X-Hoxi-Intake-Token", qaToken)
	subRec := httptest.NewRecorder()
	gw.ServeIngressRequest(subRec, subReq)
	if subRec.Code != http.StatusOK {
		t.Fatalf("portal submit failed: %d %s", subRec.Code, subRec.Body.String())
	}

	// 5. Verify gatewayIntakeSessions.SanitizedTimeline strips reasoning, raw tool output/diffs, and code blocks.
	info, _, err := gw.runner.CreateConversation(t.Context(), runner.CreateSessionRequest{
		Title:       "QA Fix",
		Directory:   proj,
		SandboxMode: "local",
	}, proj)
	if err != nil {
		t.Fatal(err)
	}
	gw.runner.Store().Update(info.ID, func(rec *runner.SessionRecord) {
		rec.Turns = []runner.MessageTurn{{
			Info: runner.MessageInfo{ID: "msg_a1", SessionID: info.ID, Role: "assistant"},
			Parts: []runner.MessagePart{
				{
					ID:   "prt_reason",
					Type: "reasoning",
					Text: "Let me inspect secret token SECRET_INTERNAL_REASONING in auth.go",
				},
				{
					ID:   "prt_tool",
					Type: "tool",
					Tool: "edit_file",
					State: &runner.ToolState{
						Status:   "completed",
						Input:    map[string]interface{}{"Path": proj + "/src/LoginModal.tsx", "Replacement": "const TOP_SECRET = 1;"},
						Output:   "diff --git a/LoginModal.tsx ... const TOP_SECRET = 1;",
						Metadata: map[string]interface{}{"diff": "-const OLD_SECRET = 0;\n+const TOP_SECRET = 1;\n+const EXTRA = 2;"},
					},
				},
				{
					ID:   "prt_patch",
					Type: "tool",
					Tool: "apply_patch",
					State: &runner.ToolState{
						Status: "completed",
						Input:  map[string]interface{}{},
						Metadata: map[string]interface{}{
							"files": []interface{}{
								map[string]interface{}{"filePath": proj + "/src/A.tsx", "additions": 5, "deletions": 2},
								map[string]interface{}{"filePath": proj + "/src/B.tsx", "additions": 3, "deletions": 0},
							},
						},
					},
				},
				{
					ID:   "prt_failed_edit",
					Type: "tool",
					Tool: "edit_file",
					State: &runner.ToolState{
						Status:   "error",
						Input:    map[string]interface{}{"Path": proj + "/src/Broken.tsx"},
						Metadata: map[string]interface{}{"filediff": map[string]interface{}{"file": "Broken.tsx", "additions": 99, "deletions": 11}},
					},
				},
				{
					ID:   "prt_text",
					Type: "text",
					Text: "已修复按钮事件绑定：\n```tsx\nconst TOP_SECRET = 1;\n```\n请重新点击确认按钮复测。",
				},
			},
		}}
	})

	bridge := &gatewayIntakeSessions{runner: gw.runner}
	tl, ok := bridge.SanitizedTimeline(info.ID)
	if !ok {
		t.Fatal("expected timeline found")
	}
	encodedTL, _ := json.Marshal(tl)
	rawJSON := string(encodedTL)
	for _, forbidden := range []string{"SECRET_INTERNAL_REASONING", "TOP_SECRET", "OLD_SECRET", proj} {
		if strings.Contains(rawJSON, forbidden) {
			t.Fatalf("sanitized timeline leaked %q: %s", forbidden, rawJSON)
		}
	}
	if !strings.Contains(rawJSON, "修改 LoginModal.tsx") || !strings.Contains(rawJSON, "请重新点击确认按钮复测") {
		t.Fatalf("expected sanitized step and summary in timeline, got: %s", rawJSON)
	}
	if len(tl.Turns) != 1 || len(tl.Turns[0].Steps) != 4 {
		t.Fatalf("expected 4 sanitized steps, got: %+v", tl.Turns)
	}
	editStep := tl.Turns[0].Steps[0]
	if editStep.Additions != 2 || editStep.Deletions != 1 || len(editStep.Files) != 1 || editStep.Files[0].File != "LoginModal.tsx" {
		t.Fatalf("unexpected single edit step diff stats: %+v", editStep)
	}
	patchStep := tl.Turns[0].Steps[1]
	if patchStep.Label != "修改 A.tsx 等 2 个文件" || patchStep.Additions != 8 || patchStep.Deletions != 2 || len(patchStep.Files) != 2 {
		t.Fatalf("unexpected multi-file patch step stats: %+v", patchStep)
	}
	failedStep := tl.Turns[0].Steps[2]
	if failedStep.Additions != 0 || failedStep.Deletions != 0 || len(failedStep.Files) != 0 {
		t.Fatalf("failed edit must omit diff stats, got: %+v", failedStep)
	}

	// 6. EnsureSession must refuse non-Git directories rather than falling back to unisolated local mode.
	if _, ensureErr := bridge.EnsureSession(t.Context(), intake.RunSessionRequest{
		Directory: proj,
		Title:     "QA Unisolated Check",
		Prompt:    "fix",
		Mode:      "build",
	}); ensureErr == nil || !strings.Contains(ensureErr.Error(), "isolated worktree") {
		t.Fatalf("expected EnsureSession to reject non-Git directory without committed HEAD, got: %v", ensureErr)
	}
}

type stubRelayEndpointExt struct {
	intakeBase string
	remoteBase string
}

func (stubRelayEndpointExt) HasSessionContext(string) bool { return false }
func (stubRelayEndpointExt) ForgetSession(string)          {}
func (stubRelayEndpointExt) RegisterRoutes(*http.ServeMux) {}
func (stubRelayEndpointExt) Close()                        {}
func (s stubRelayEndpointExt) IntakeRelayBaseURL() string  { return s.intakeBase }
func (s stubRelayEndpointExt) RemoteRelayBaseURL() string  { return s.remoteBase }

func TestRelayEndpointExtensionAndServeIntakeRelayRequest(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOXI_HOME", filepath.Join(tmp, "hoxi-home"))
	proj := filepath.Join(tmp, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	distFS := fstest.MapFS{
		"index.html":        &fstest.MapFile{Data: []byte("<!doctype html><html><body>Hoxi QA</body></html>")},
		"assets/app-123.js": &fstest.MapFile{Data: []byte("console.log('bundle');")},
	}
	gw := New(0, "test-pass", proj, distFS, nil)
	t.Cleanup(gw.CloseBrowser)
	gw.AttachExtension(stubRelayEndpointExt{
		intakeBase: "https://coller.node.example.test",
		remoteBase: "https://coller.node.example.test",
	})

	createBody, _ := json.Marshal(intake.CreateLinkInput{Directory: proj, Label: "QA"})
	linkRec := httptest.NewRecorder()
	gw.ServeHTTP(linkRec, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/hoxi/intake/links", bytes.NewReader(createBody)))
	if linkRec.Code != http.StatusOK {
		t.Fatalf("link create failed: %d %s", linkRec.Code, linkRec.Body.String())
	}
	var pub intake.PublicLink
	if err := json.Unmarshal(linkRec.Body.Bytes(), &pub); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub.RelayURL, "https://coller.node.example.test/qa/hqa_") {
		t.Fatalf("RelayURL = %q, want extension base prefix", pub.RelayURL)
	}
	if hashes := gw.ActiveIntakeTokenHashes(); len(hashes) != 1 || len(hashes[0]) != 64 {
		t.Fatalf("expected 1 SHA-256 intake hash, got %v", hashes)
	}

	okRec := httptest.NewRecorder()
	okReq := httptest.NewRequest(http.MethodGet, "https://coller.node.example.test"+pub.PortalPath, nil)
	okReq.Header.Set("Cookie", "sid=untrusted")
	gw.ServeIntakeRelayRequest(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("ServeIntakeRelayRequest page got %d, want 200", okRec.Code)
	}

	denyRec := httptest.NewRecorder()
	gw.ServeIntakeRelayRequest(denyRec, httptest.NewRequest(http.MethodGet, "https://coller.node.example.test/api/hoxi/sessions", nil))
	if denyRec.Code != http.StatusForbidden {
		t.Fatalf("ServeIntakeRelayRequest must reject non-intake paths with 403, got %d", denyRec.Code)
	}
}

func TestIntakeManagementRoutesRequireGatewayToken(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOXI_HOME", filepath.Join(tmp, "hoxi-home"))
	proj := filepath.Join(tmp, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	gw := New(0, "test-pass", proj, nil, nil)
	t.Cleanup(gw.CloseBrowser)
	const tok = "intake-boot-token-123"
	gw.SetGatewayToken(tok)

	// Unauthenticated loopback calls — including spoofed Wails Origin/Host
	// headers — must all return 403 when a boot gateway token is configured.
	for _, p := range []string{"/api/hoxi/intake/links", "/api/hoxi/intake/tasks", "/api/hoxi/intake/tasks/approve", "/api/hoxi/intake/tasks/status"} {
		method := http.MethodGet
		if strings.HasSuffix(p, "/approve") || strings.HasSuffix(p, "/status") {
			method = http.MethodPost
		}
		for _, tc := range []struct {
			label  string
			host   string
			origin string
		}{
			{"plain", "", ""},
			{"spoofed-wails-origin", "", "wails://wails"},
			{"spoofed-wails-host", "wails", ""},
		} {
			req := localRequest(method, p, nil)
			if tc.host != "" {
				req.Host = tc.host
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			gw.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s (%s) without gateway token = %d, want 403: %s", method, p, tc.label, rec.Code, rec.Body.String())
			}
		}
	}

	// Authenticated call with X-Hoxi-Token (including from Wails Origin) succeeds.
	okReq := localRequest(http.MethodGet, "/api/hoxi/intake/links?directory="+proj, nil)
	okReq.Header.Set("Origin", "wails://wails")
	okReq.Header.Set(gatewayTokenHeader, tok)
	okRec := httptest.NewRecorder()
	gw.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("GET /api/hoxi/intake/links with token = %d, want 200: %s", okRec.Code, okRec.Body.String())
	}
}

func TestSanitizedTimelineMarksDeveloperContinuationWithoutLeakingPrompt(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOXI_HOME", filepath.Join(tmp, "hoxi-home"))
	proj := filepath.Join(tmp, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	gw := New(0, "test-pass", proj, nil, nil)
	t.Cleanup(gw.CloseBrowser)

	info, _, err := gw.runner.CreateConversation(t.Context(), runner.CreateSessionRequest{
		Title:       "QA · 导出失败",
		Directory:   proj,
		SandboxMode: "local",
	}, proj)
	if err != nil {
		t.Fatal(err)
	}
	_, initPrompt := intake.BuildInitialFixTurn(intake.TaskItem{
		Title:       "导出失败",
		Description: "点击导出报错",
	})
	_, reopenPrompt := intake.BuildFollowupFixTurn(intake.TaskItem{
		Title:    "导出失败",
		Comments: []intake.TaskComment{{AuthorRole: "tester", Text: "导出空表仍报错"}},
	})

	gw.runner.Store().Update(info.ID, func(rec *runner.SessionRecord) {
		rec.Turns = []runner.MessageTurn{
			{
				Info:  runner.MessageInfo{ID: "msg_u1", SessionID: info.ID, Role: "user", Time: runner.MessageTime{Created: 1000}},
				Parts: []runner.MessagePart{{ID: "p_u1", Type: "text", Text: initPrompt}},
			},
			{
				Info:  runner.MessageInfo{ID: "msg_a1", SessionID: info.ID, Role: "assistant", Time: runner.MessageTime{Created: 1100, Completed: 1200}},
				Parts: []runner.MessagePart{{ID: "p_a1", Type: "text", Text: "第一轮已修复导出接口。"}},
			},
			{
				Info:  runner.MessageInfo{ID: "msg_u2", SessionID: info.ID, Role: "user", Time: runner.MessageTime{Created: 2000}},
				Parts: []runner.MessagePart{{ID: "p_u2", Type: "text", Text: reopenPrompt}},
			},
			{
				Info:  runner.MessageInfo{ID: "msg_a2", SessionID: info.ID, Role: "assistant", Time: runner.MessageTime{Created: 2100, Completed: 2200}},
				Parts: []runner.MessagePart{{ID: "p_a2", Type: "text", Text: "第二轮已处理空表场景。"}},
			},
			{
				Info:  runner.MessageInfo{ID: "msg_u3", SessionID: info.ID, Role: "user", Time: runner.MessageTime{Created: 3000}},
				Parts: []runner.MessagePart{{ID: "p_u3", Type: "text", Text: "顺便检查 /Users/alice/secret/export.go 里的 SECRET_ADMIN_INSTRUCTION 逻辑"}},
			},
			{
				Info: runner.MessageInfo{ID: "msg_a3", SessionID: info.ID, Role: "assistant", Time: runner.MessageTime{Created: 3100, Completed: 3200}},
				Parts: []runner.MessagePart{
					{
						ID:   "p_tool3",
						Type: "tool",
						Tool: "edit_file",
						State: &runner.ToolState{
							Status: "completed",
							Input:  map[string]interface{}{"Path": proj + "/src/Export.tsx"},
						},
					},
					{ID: "p_a3", Type: "text", Text: "已补充大文件分片导出校验，请重点复测。"},
				},
			},
		}
	})

	bridge := &gatewayIntakeSessions{runner: gw.runner}
	tl, ok := bridge.SanitizedTimeline(info.ID)
	if !ok {
		t.Fatal("expected timeline found")
	}
	raw, _ := json.Marshal(tl)
	if strings.Contains(string(raw), "SECRET_ADMIN_INSTRUCTION") || strings.Contains(string(raw), "Users/alice") {
		t.Fatalf("developer continuation prompt must never leak to tester timeline: %s", string(raw))
	}
	// Expect: assistant 1, assistant 2, developer_followup notice, assistant 3.
	if len(tl.Turns) != 4 {
		t.Fatalf("expected 4 timeline turns (3 assistant + 1 developer notice), got %d: %+v", len(tl.Turns), tl.Turns)
	}
	if len(tl.Turns[2].Steps) != 1 || tl.Turns[2].Steps[0].Kind != "notice" || tl.Turns[2].Steps[0].Category != "developer_followup" {
		t.Fatalf("expected turn 2 to be developer_followup notice, got %+v", tl.Turns[2])
	}
	if tl.Turns[3].ID != "msg_a3" || len(tl.Turns[3].Steps) != 2 {
		t.Fatalf("expected continued assistant turn after notice, got %+v", tl.Turns[3])
	}

	summary, completed, aborted, errText := bridge.inspectLatestTurn(info.ID)
	if !completed || aborted || errText != "" || !strings.Contains(summary, "大文件分片导出") {
		t.Fatalf("unexpected inspectLatestTurn result: summary=%q completed=%v aborted=%v err=%q", summary, completed, aborted, errText)
	}
}

type stubApproveRunBridge struct {
	*gatewayIntakeSessions
	sessionID string
}

func (s *stubApproveRunBridge) EnsureSession(_ context.Context, _ intake.RunSessionRequest) (string, error) {
	return s.sessionID, nil
}

func (s *stubApproveRunBridge) RunTurn(_ context.Context, _ string, _ intake.RunSessionRequest) (string, error) {
	return "第一轮修复完成", nil
}

func TestGatewayIntakeWatcherRelaysDesktopContinuationEvents(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOXI_HOME", filepath.Join(tmp, "hoxi-home"))
	proj := filepath.Join(tmp, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	gw := New(0, "test-pass", proj, nil, nil)
	t.Cleanup(gw.CloseBrowser)

	info, _, err := gw.runner.CreateConversation(t.Context(), runner.CreateSessionRequest{
		Title:       "QA · 导出失败",
		Directory:   proj,
		SandboxMode: "local",
	}, proj)
	if err != nil {
		t.Fatal(err)
	}

	busyNow := false
	bridge := &gatewayIntakeSessions{
		runner: gw.runner,
		isBusy: func(sid string) bool { return sid == info.ID && busyNow },
	}
	svc := intake.NewService(filepath.Join(tmp, "intake-watch.json"), nil, &stubApproveRunBridge{
		gatewayIntakeSessions: bridge,
		sessionID:             info.ID,
	}, nil)
	svc.SetSessionWatcher(func() func() {
		return bridge.startSessionWatcher(svc)
	})
	t.Cleanup(svc.Close)

	pub, err := svc.CreateLink(intake.CreateLinkInput{Directory: proj, Label: "QA"})
	if err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimPrefix(pub.PortalPath, "/qa/")
	link, err := svc.AuthorizePortalToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	task, err := svc.SubmitPortalTask(link, intake.SubmitTaskInput{
		Title:       "导出失败",
		Description: "点击导出报错",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveAndRun(t.Context(), intake.ApproveTaskInput{TaskID: task.ID}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		items := svc.ListDesktopTasks(proj)
		if len(items) == 1 && items[0].Status == intake.StatusSucceeded {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	subCh, unsub := svc.SubscribePortal(link.ID)
	defer unsub()
	for {
		select {
		case <-subCh:
		default:
			goto drained
		}
	}
drained:

	// A stale buffered busy event after a turn already ended must not flicker the task back to running.
	busyNow = false
	bridge.forwardRunnerEvent(svc, runner.SSEEvent{
		Type: "session.status",
		Properties: map[string]interface{}{
			"sessionID": info.ID,
			"status":    runner.SessionStatus{Type: "busy"},
		},
	})
	if items := svc.ListDesktopTasks(proj); len(items) != 1 || items[0].Status != intake.StatusSucceeded {
		t.Fatalf("stale busy event must not flip settled task to running, got %+v", items)
	}

	// Simulate admin continuing the linked session directly on desktop.
	busyNow = true
	bridge.forwardRunnerEvent(svc, runner.SSEEvent{
		Type: "session.status",
		Properties: map[string]interface{}{
			"sessionID": info.ID,
			"status":    runner.SessionStatus{Type: "busy"},
		},
	})
	if items := svc.ListDesktopTasks(proj); len(items) != 1 || items[0].Status != intake.StatusRunning {
		t.Fatalf("expected running after desktop busy event, got %+v", items)
	}

	// A stale idle event arriving while the session is still actively busy must not settle it early.
	bridge.forwardRunnerEvent(svc, runner.SSEEvent{
		Type:       "session.idle",
		Properties: map[string]interface{}{"sessionID": info.ID},
	})
	if items := svc.ListDesktopTasks(proj); len(items) != 1 || items[0].Status != intake.StatusRunning {
		t.Fatalf("stale idle while busy must keep task running, got %+v", items)
	}

	gw.runner.Store().Update(info.ID, func(rec *runner.SessionRecord) {
		rec.Turns = []runner.MessageTurn{
			{
				Info:  runner.MessageInfo{ID: "msg_u1", SessionID: info.ID, Role: "user", Time: runner.MessageTime{Created: 1000}},
				Parts: []runner.MessagePart{{ID: "p_u1", Type: "text", Text: intake.BuildInitialFixPrompt(intake.TaskItem{Title: "导出失败", Description: "点击导出报错"})}},
			},
			{
				Info:  runner.MessageInfo{ID: "msg_a1", SessionID: info.ID, Role: "assistant", Time: runner.MessageTime{Created: 1100, Completed: 1200}},
				Parts: []runner.MessagePart{{ID: "p_a1", Type: "text", Text: "第一轮修复完成"}},
			},
			{
				Info:  runner.MessageInfo{ID: "msg_u2", SessionID: info.ID, Role: "user", Time: runner.MessageTime{Created: 2000}},
				Parts: []runner.MessagePart{{ID: "p_u2", Type: "text", Text: "继续补上边界校验"}},
			},
			{
				Info:  runner.MessageInfo{ID: "msg_a2", SessionID: info.ID, Role: "assistant", Time: runner.MessageTime{Created: 2100, Completed: 2200}},
				Parts: []runner.MessagePart{{ID: "p_a2", Type: "text", Text: "第二轮已补充边界校验，请复测"}},
			},
		}
	})

	bridge.forwardRunnerEvent(svc, runner.SSEEvent{
		Type: "message.part.updated",
		Properties: map[string]interface{}{
			"part": runner.MessagePart{ID: "p_a2", SessionID: info.ID, Type: "text", Text: "第二轮已补充边界校验，请复测"},
		},
	})
	busyNow = false
	bridge.forwardRunnerEvent(svc, runner.SSEEvent{
		Type:       "session.idle",
		Properties: map[string]interface{}{"sessionID": info.ID},
	})

	items := svc.ListDesktopTasks(proj)
	if len(items) != 1 || items[0].Status != intake.StatusSucceeded || !strings.Contains(items[0].Summary, "边界校验") {
		t.Fatalf("expected task settled with continued summary, got %+v", items)
	}
	tl, err := svc.PortalTimeline(link, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Turns) != 3 || tl.Turns[1].Steps[0].Kind != "notice" || tl.Turns[2].ID != "msg_a2" {
		t.Fatalf("expected continued timeline with notice + msg_a2, got %+v", tl)
	}

	// Resync event reconciles linked session state safely.
	bridge.forwardRunnerEvent(svc, runner.SSEEvent{Type: "hoxi.stream.resync", Properties: map[string]interface{}{}})
	sawTimeline := false
	sawTask := false
	for {
		select {
		case evt := <-subCh:
			if evt.Type == "timeline.updated" && evt.TaskID == task.ID {
				sawTimeline = true
			}
			if evt.Type == "task.updated" && evt.TaskID == task.ID {
				sawTask = true
			}
		default:
			goto doneCheck
		}
	}
doneCheck:
	if !sawTimeline || !sawTask {
		t.Fatalf("expected portal SSE timeline.updated and task.updated events, got timeline=%v task=%v", sawTimeline, sawTask)
	}
}
