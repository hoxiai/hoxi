package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIsTransitionalActionPreambleAndShouldRecoverOpenCodeTurn(t *testing.T) {
	t.Parallel()

	positiveTexts := []string{
		"The migrated pins are all empty. v1.2.4 needs a non-empty release pin to load the release catalog; otherwise it falls back to a missing `themes/` source dir. Let me verify the available release keys and set pins.",
		"There are external modifications to `.github/workflows/release.yml`. The opencode test failure is environmental (`OPENCODE_DB` inherited from the shell), not a code bug. Let me confirm with the environment unset.",
		"5480 is free. Now start the container to auto-migrate schema.",
		"All 214 sequences are ahead of max values. Now start the container and verify.",
		"检查完毕，v1.2.4 的 pin 为空。让我查看可用的 release key 并设置 pin。",
		"原因已经确认，让我先验证可用的 release key 并设置 pin。",
		"容器已正常启动，接下来我检查 /health 与域名访问。",
		"258 张表只说明表壳齐了。\n\n我先确认到底哪个站的库缺表。",
	}
	for _, sample := range positiveTexts {
		if !isTransitionalActionPreamble(sample) {
			t.Errorf("expected isTransitionalActionPreamble(%q) = true, got false", sample)
		}
	}

	negativeTexts := []string{
		"",
		"已完成所有模块检查与修复。",
		"线上已切到 v1.2.4，无遗留步骤。",
		"Both files are already committed at d62c2f7. Nothing to do.",
		"需要我做那步绕过网关、直接用 loomy 账号凭证调原生接口的对比验证吗？",
		"Should I proceed with the migration? Let me know?",
		"发布检查发现新阻塞：`app/themes/panel` 子模块指针出现变化，需要你定夺：",
		"## 两点待你决定\n1. 文档区仍只有中文\n2. 未提交",
		"Let me summarize.\n[[goal:complete]] verified all 258 tables",
	}
	for _, sample := range negativeTexts {
		if isTransitionalActionPreamble(sample) {
			t.Errorf("expected isTransitionalActionPreamble(%q) = false, got true", sample)
		}
	}

	ap := &activeProcess{sessionID: "ses_test", runtime: "opencode", done: make(chan struct{}), idleCh: make(chan struct{}, 1)}

	// 1. Mid-task stop with prior tool call and transitional preamble must trigger recovery.
	midTaskStop := []MessageTurn{
		{
			Info:  MessageInfo{ID: "msg_u1", Role: "user"},
			Parts: []MessagePart{{ID: "prt_u1", MessageID: "msg_u1", Type: "text", Text: "帮我进行这个迁移"}},
		},
		{
			Info: MessageInfo{ID: "msg_a1", Role: "assistant", ParentID: "msg_u1", Finish: "tool-calls"},
			Parts: []MessagePart{{
				ID: "prt_tool1", MessageID: "msg_a1", Type: "tool", Tool: "bash",
				State: &ToolState{Status: "completed", Input: map[string]interface{}{"command": "psql -c 'select 1'"}},
			}},
		},
		{
			Info: MessageInfo{ID: "msg_a2", Role: "assistant", ParentID: "msg_u1", Finish: "stop"},
			Parts: []MessagePart{
				{ID: "prt_r2", MessageID: "msg_a2", Type: "reasoning", Text: "Let me first test the hypothesis: set bluex pin for store 1 and see if www.myshopark.com renders."},
				{ID: "prt_t2", MessageID: "msg_a2", Type: "text", Text: "The migrated pins are all empty. v1.2.4 needs a non-empty release pin to load the release catalog; otherwise it falls back to a missing `themes/` source dir. Let me verify the available release keys and set pins."},
			},
		},
	}
	if !shouldRecoverOpenCodeTurn(midTaskStop, ap) {
		t.Fatal("expected shouldRecoverOpenCodeTurn to return true for mid-task transitional preamble")
	}

	// 2. Leaked <rfl:parameter> tool markup inside reasoning must trigger recovery even when visible text is present.
	leakedWithText := []MessageTurn{
		{
			Info:  MessageInfo{ID: "msg_u1", Role: "user"},
			Parts: []MessagePart{{ID: "prt_u1", MessageID: "msg_u1", Type: "text", Text: "run tests"}},
		},
		{
			Info: MessageInfo{ID: "msg_a1", Role: "assistant", ParentID: "msg_u1", Finish: "stop"},
			Parts: []MessagePart{
				{ID: "prt_r1", MessageID: "msg_a1", Type: "reasoning", Text: "No reasoning available.<|end|><|constrain|>assistant to=bash<|channel|>analysis <|constrain|>xml<|message|><rfl:parameter name=\"command\">\ngit status --short\n</rfl:parameter>"},
				{ID: "prt_t1", MessageID: "msg_a1", Type: "text", Text: "The test failure is environmental."},
			},
		},
	}
	if !shouldRecoverOpenCodeTurn(leakedWithText, ap) {
		t.Fatal("expected shouldRecoverOpenCodeTurn to return true when reasoning leaks <rfl:parameter> tool markup alongside visible text")
	}
	cleaned, leaked := stripLeakedReasoningToolMarkup(leakedWithText[1].Parts[0].Text)
	if !leaked || cleaned != "No reasoning available." {
		t.Fatalf("expected <rfl:parameter> markup to be stripped, got leaked=%v cleaned=%q", leaked, cleaned)
	}
	if gotTool := extractLeakedToolFromTurns(leakedWithText, ap); gotTool != "bash" {
		t.Fatalf("expected extractLeakedToolFromTurns to identify leaked tool 'bash', got %q", gotTool)
	}
	recPayload := buildOpenCodeReasoningRecoveryPayload(SessionRecord{ProviderID: "openai"}, "build", "gpt-5", "", "", 1, recoveryKindReasoning, "bash")
	recParts, _ := recPayload["parts"].([]map[string]interface{})
	if len(recParts) != 1 || !json.Valid([]byte(`"ok"`)) {
		t.Fatalf("unexpected recovery parts: %+v", recParts)
	}
	if txt, _ := recParts[0]["text"].(string); txt == openCodeReasoningRecoveryPrompt {
		t.Fatalf("expected recovery prompt to include specific leaked tool name 'bash', got %q", txt)
	}

	// 3. Normal final summary after tool calls must NOT trigger recovery.
	completedTurn := []MessageTurn{
		midTaskStop[0],
		midTaskStop[1],
		{
			Info: MessageInfo{ID: "msg_a_done", Role: "assistant", ParentID: "msg_u1", Finish: "stop"},
			Parts: []MessagePart{
				{ID: "prt_r_done", MessageID: "msg_a_done", Type: "reasoning", Text: "Everything verified."},
				{ID: "prt_t_done", MessageID: "msg_a_done", Type: "text", Text: "迁移与 258 张表行数核验已全部完成，myshopark.com 返回 200。"},
			},
		},
	}
	if shouldRecoverOpenCodeTurn(completedTurn, ap) {
		t.Fatal("normal completed summary must not trigger recovery")
	}

	// 4. Step that already emitted a tool call must NOT trigger recovery.
	withTool := []MessageTurn{
		midTaskStop[0],
		{
			Info: MessageInfo{ID: "msg_a_tool", Role: "assistant", ParentID: "msg_u1", Finish: "tool-calls"},
			Parts: []MessagePart{
				{ID: "prt_t_tool", MessageID: "msg_a_tool", Type: "text", Text: "Let me verify the available release keys and set pins."},
				{ID: "prt_tool", MessageID: "msg_a_tool", Type: "tool", Tool: "bash", State: &ToolState{Status: "completed"}},
			},
		},
	}
	if shouldRecoverOpenCodeTurn(withTool, ap) {
		t.Fatal("step with actual tool part must not trigger recovery")
	}

	// 5. Concurrent multi-tool step where one tool was rejected with feedback (e.g. approval timeout)
	// and a sibling tool was cascade-rejected by OpenCode with bare RejectedError must trigger recovery.
	cascadeTimeoutTurn := []MessageTurn{
		midTaskStop[0],
		{
			Info: MessageInfo{ID: "msg_a_cascade", Role: "assistant", ParentID: "msg_u1", Finish: "tool-calls"},
			Parts: []MessagePart{
				{
					ID: "prt_tool_timeout", MessageID: "msg_a_cascade", Type: "tool", Tool: "bash",
					State: &ToolState{
						Status: "error",
						Error:  "The user rejected permission to use this specific tool call with the following feedback: Approval timed out after 10 minute(s) with no response and was rejected. Try an approach that does not need this permission, or say so in the summary.",
					},
				},
				{
					ID: "prt_tool_sibling", MessageID: "msg_a_cascade", Type: "tool", Tool: "bash",
					State: &ToolState{
						Status: "error",
						Error:  "The user rejected permission to use this specific tool call.",
					},
				},
			},
		},
	}
	if gotKind := classifyOpenCodeTurnRecovery(cascadeTimeoutTurn, ap); gotKind != recoveryKindPermissionCascade {
		t.Fatalf("expected classifyOpenCodeTurnRecovery = %q for concurrent timeout cascade, got %q", recoveryKindPermissionCascade, gotKind)
	}
	cascadePayload := buildOpenCodeReasoningRecoveryPayload(SessionRecord{ProviderID: "hoxi-official"}, "build", "deepseek-flash", "", "", 1, recoveryKindPermissionCascade, "")
	cascadeParts, _ := cascadePayload["parts"].([]map[string]interface{})
	if len(cascadeParts) != 1 || cascadeParts[0]["text"] != openCodePermissionCascadeRecoveryPrompt {
		t.Fatalf("expected permission cascade recovery prompt, got %+v", cascadeParts)
	}

	// 6. Bare user rejection without any feedback rejection must NOT trigger recovery.
	bareRejectTurn := []MessageTurn{
		midTaskStop[0],
		{
			Info: MessageInfo{ID: "msg_a_bare", Role: "assistant", ParentID: "msg_u1", Finish: "tool-calls"},
			Parts: []MessagePart{
				{
					ID: "prt_tool_bare", MessageID: "msg_a_bare", Type: "tool", Tool: "bash",
					State: &ToolState{
						Status: "error",
						Error:  "The user rejected permission to use this specific tool call.",
					},
				},
			},
		},
	}
	if shouldRecoverOpenCodeTurn(bareRejectTurn, ap) {
		t.Fatal("bare user rejection without feedback must not trigger recovery")
	}
}

func TestStartOpenCodeTurnAutoRecoversFromMidTaskTransitionalStop(t *testing.T) {
	t.Parallel()

	var (
		mu             sync.Mutex
		postedMsgIDs   []string
		postedMsgTexts []string
		turns          []MessageTurn
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				_, _ = w.Write([]byte(": connected\n\n"))
				f.Flush()
			}
			<-r.Context().Done()
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_oc_midtask/message":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			msgID, _ := body["messageID"].(string)
			var msgText string
			if rawParts, ok := body["parts"].([]interface{}); ok && len(rawParts) > 0 {
				if p0, ok := rawParts[0].(map[string]interface{}); ok {
					msgText, _ = p0["text"].(string)
				}
			}
			mu.Lock()
			postedMsgIDs = append(postedMsgIDs, msgID)
			postedMsgTexts = append(postedMsgTexts, msgText)
			callNum := len(postedMsgIDs)
			if callNum == 1 {
				turns = []MessageTurn{
					{
						Info:  MessageInfo{ID: msgID, SessionID: "ses_oc_midtask", Role: "user", Time: MessageTime{Created: 100}},
						Parts: []MessagePart{{ID: "prt_u1", SessionID: "ses_oc_midtask", MessageID: msgID, Type: "text", Text: "帮我进行这个迁移"}},
					},
					{
						Info: MessageInfo{
							ID: "msg_a1", SessionID: "ses_oc_midtask", Role: "assistant", ParentID: msgID,
							Finish: "tool-calls", Time: MessageTime{Created: 101, Completed: 102},
						},
						Parts: []MessagePart{{
							ID: "prt_tool1", SessionID: "ses_oc_midtask", MessageID: "msg_a1", Type: "tool", Tool: "bash",
							State: &ToolState{Status: "completed", Input: map[string]interface{}{"command": "psql -c 'select 1'"}},
						}},
					},
					{
						Info: MessageInfo{
							ID: "msg_a2", SessionID: "ses_oc_midtask", Role: "assistant", ParentID: msgID,
							Finish: "stop", Time: MessageTime{Created: 103, Completed: 104},
						},
						Parts: []MessagePart{
							{
								ID: "prt_r2", SessionID: "ses_oc_midtask", MessageID: "msg_a2", Type: "reasoning",
								Text: "Let me first test the hypothesis: set bluex pin for store 1 and see if myshopark.com renders.",
							},
							{
								ID: "prt_t2", SessionID: "ses_oc_midtask", MessageID: "msg_a2", Type: "text",
								Text: "The migrated pins are all empty. v1.2.4 needs a non-empty release pin to load the release catalog; otherwise it falls back to a missing `themes/` source dir. Let me verify the available release keys and set pins.",
							},
						},
					},
				}
			} else {
				turns = append(turns,
					MessageTurn{
						Info:  MessageInfo{ID: msgID, SessionID: "ses_oc_midtask", Role: "user", Time: MessageTime{Created: 105}},
						Parts: []MessagePart{{ID: "prt_u_rec", SessionID: "ses_oc_midtask", MessageID: msgID, Type: "text", Text: msgText}},
					},
					MessageTurn{
						Info: MessageInfo{
							ID: "msg_a3", SessionID: "ses_oc_midtask", Role: "assistant", ParentID: msgID,
							Finish: "stop", Time: MessageTime{Created: 106, Completed: 107},
						},
						Parts: []MessagePart{
							{ID: "prt_t3", SessionID: "ses_oc_midtask", MessageID: "msg_a3", Type: "text", Text: "已设置所有 store 的 theme_release_pin，myshopark.com 渲染 200，迁移全部完成。"},
						},
					},
				)
			}
			mu.Unlock()
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_oc_midtask/message":
			mu.Lock()
			snapshot := append([]MessageTurn(nil), turns...)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(snapshot)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	svc.ConfigureOpenCode(srv.URL, "secret")
	rec := SessionRecord{
		ID:           "ses_hoxi_midtask",
		ExternalID:   "ses_oc_midtask",
		Runtime:      "opencode",
		ProjectDir:   root,
		WorktreePath: root,
		SandboxMode:  "local",
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	user := MessageInfo{ID: "msg_u_initial", SessionID: rec.ID, Role: "user", Time: MessageTime{Created: 100}}
	parts := []MessagePart{{ID: "prt_u1", SessionID: rec.ID, MessageID: user.ID, Type: "text", Text: "帮我进行这个迁移"}}
	if err := svc.startOpenCodeTurn(rec, "build", "deepseek-flash", "", "", user, parts, parts); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		svc.mu.Lock()
		busy := svc.procs[rec.ID] != nil
		svc.mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	mu.Lock()
	gotPosts := append([]string(nil), postedMsgIDs...)
	gotTexts := append([]string(nil), postedMsgTexts...)
	mu.Unlock()
	if len(gotPosts) != 2 || gotPosts[0] != "msg_u_initial" || !isInternalRecoveryMessageID(gotPosts[1]) {
		t.Fatalf("expected initial post + 1 internal recovery post, got: %v", gotPosts)
	}
	if len(gotTexts) != 2 || gotTexts[1] != openCodePreambleRecoveryPrompt {
		t.Fatalf("expected preamble recovery prompt %q, got %v", openCodePreambleRecoveryPrompt, gotTexts)
	}

	stored, ok := svc.store.Get(rec.ID)
	if !ok {
		t.Fatal("expected session record in store")
	}
	if len(stored.Turns) != 4 {
		t.Fatalf("expected 4 visible turns (user, tool step, transitional step, final step), got %d", len(stored.Turns))
	}
	if stored.Turns[3].Info.ID != "msg_a3" || stored.Turns[3].Info.ParentID != "msg_u_initial" {
		t.Fatalf("expected recovered turn msg_a3 with ParentID=msg_u_initial, got %+v", stored.Turns[3].Info)
	}
}

func TestStartOpenCodeTurnRotatesPoisonedSessionAndCarriesHandoffContext(t *testing.T) {
	t.Parallel()

	priorTurns := []MessageTurn{
		{
			Info:  MessageInfo{ID: "msg_u_prior", SessionID: "ses_hoxi_poisoned", Role: "user", Time: MessageTime{Created: 100}},
			Parts: []MessagePart{{ID: "prt_u_prior", SessionID: "ses_hoxi_poisoned", MessageID: "msg_u_prior", Type: "text", Text: "建议增加新客服消息通知"}},
		},
		{
			Info: MessageInfo{
				ID: "msg_a_prior", SessionID: "ses_hoxi_poisoned", Role: "assistant", ParentID: "msg_u_prior",
				Finish: "stop", Time: MessageTime{Created: 101, Completed: 102},
			},
			Parts: []MessagePart{{ID: "prt_a_prior", SessionID: "ses_hoxi_poisoned", MessageID: "msg_a_prior", Type: "text", Text: "已规划好 APNs 与桌面端通知方案。"}},
		},
	}

	var (
		mu             sync.Mutex
		createdCount   int
		poisonedPosts  int
		freshPosts     int
		freshSystem    string
		freshPostedMsg string
		freshTurns     []MessageTurn
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				_, _ = w.Write([]byte(": connected\n\n"))
				f.Flush()
			}
			<-r.Context().Done()
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_oc_poisoned/message":
			mu.Lock()
			poisonedPosts++
			mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_d43c8cf9"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_oc_poisoned/message":
			_ = json.NewEncoder(w).Encode(priorTurns)
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			mu.Lock()
			createdCount++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"ses_oc_fresh"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_oc_fresh/message":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			msgID, _ := body["messageID"].(string)
			sysText, _ := body["system"].(string)
			mu.Lock()
			freshPosts++
			freshPostedMsg = msgID
			freshSystem = sysText
			freshTurns = []MessageTurn{
				{
					Info:  MessageInfo{ID: msgID, SessionID: "ses_oc_fresh", Role: "user", Time: MessageTime{Created: 200}},
					Parts: []MessagePart{{ID: "prt_u2", SessionID: "ses_oc_fresh", MessageID: msgID, Type: "text", Text: "请直接按这个方案实现"}},
				},
				{
					Info: MessageInfo{
						ID: "msg_a_fresh", SessionID: "ses_oc_fresh", Role: "assistant", ParentID: msgID,
						Finish: "stop", Time: MessageTime{Created: 201, Completed: 202},
					},
					Parts: []MessagePart{{ID: "prt_a_fresh", SessionID: "ses_oc_fresh", MessageID: "msg_a_fresh", Type: "text", Text: "已完成新客服消息通知实现。"}},
				},
			}
			mu.Unlock()
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_oc_fresh/message":
			mu.Lock()
			snapshot := append([]MessageTurn(nil), freshTurns...)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(snapshot)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	svc.ConfigureOpenCode(srv.URL, "secret")
	rec := SessionRecord{
		ID:           "ses_hoxi_poisoned",
		Title:        "建议增加新客服消息通知",
		ExternalID:   "ses_oc_poisoned",
		OpenCodeIDs:  []string{"ses_oc_poisoned"},
		Runtime:      "opencode",
		ProjectDir:   root,
		WorktreePath: root,
		SandboxMode:  "local",
		Turns:        priorTurns,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	ch, unsub := svc.Subscribe(rec.ID)
	defer unsub()

	user := MessageInfo{ID: "msg_u_followup", SessionID: rec.ID, Role: "user", Time: MessageTime{Created: 200}}
	parts := []MessagePart{{ID: "prt_u_followup", SessionID: rec.ID, MessageID: user.ID, Type: "text", Text: "请直接按这个方案实现"}}
	if err := svc.startOpenCodeTurn(rec, "build", "gpt-5", "openai", "", user, parts, parts); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		svc.mu.Lock()
		busy := svc.procs[rec.ID] != nil
		svc.mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	for len(ch) > 0 {
		evt := <-ch
		if evt.Type == "session.error" {
			t.Fatalf("unexpected session.error after successful session rotation: %+v", evt)
		}
	}

	mu.Lock()
	gotPoisoned := poisonedPosts
	gotCreated := createdCount
	gotFresh := freshPosts
	gotMsg := freshPostedMsg
	gotSys := freshSystem
	mu.Unlock()

	if gotPoisoned != 1 || gotCreated != 1 || gotFresh != 1 || gotMsg != "msg_u_followup" {
		t.Fatalf("unexpected rotation counts: poisoned=%d created=%d fresh=%d msg=%q", gotPoisoned, gotCreated, gotFresh, gotMsg)
	}
	if !strings.Contains(gotSys, "已规划好 APNs 与桌面端通知方案") {
		t.Fatalf("expected rotated session system prompt to carry prior handoff context, got: %q", gotSys)
	}

	stored, ok := svc.store.Get(rec.ID)
	if !ok {
		t.Fatal("expected session record in store")
	}
	if stored.ExternalID != "ses_oc_fresh" || !slices.Contains(stored.OpenCodeIDs, "ses_oc_fresh") {
		t.Fatalf("expected ExternalID=ses_oc_fresh in OpenCodeIDs, got ExternalID=%q IDs=%v", stored.ExternalID, stored.OpenCodeIDs)
	}
	if stored.ContextPending {
		t.Fatal("expected ContextPending to be cleared after rotated turn succeeded")
	}
	if len(stored.Turns) != 4 || stored.Turns[3].Info.ID != "msg_a_fresh" {
		t.Fatalf("expected 4 turns ending with msg_a_fresh, got %d turns", len(stored.Turns))
	}
}

func TestStartOpenCodeTurnPersistsFailedTurnWithoutRotatingNonServerFault(t *testing.T) {
	t.Parallel()

	var (
		mu           sync.Mutex
		createdCount int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				_, _ = w.Write([]byte(": connected\n\n"))
				f.Flush()
			}
			<-r.Context().Done()
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			mu.Lock()
			createdCount++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"ses_should_not_create"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_oc_modelerr/message":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"name":"UnknownError","data":{"message":"Model not found: openai/gpt-missing."}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_oc_modelerr/message":
			_ = json.NewEncoder(w).Encode([]MessageTurn{})
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	svc.ConfigureOpenCode(srv.URL, "secret")
	rec := SessionRecord{
		ID:           "ses_hoxi_modelerr",
		ExternalID:   "ses_oc_modelerr",
		OpenCodeIDs:  []string{"ses_oc_modelerr"},
		Runtime:      "opencode",
		ProjectDir:   root,
		WorktreePath: root,
		SandboxMode:  "local",
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	ch, unsub := svc.Subscribe(rec.ID)
	defer unsub()

	user := MessageInfo{ID: "msg_u_fail", SessionID: rec.ID, Role: "user", Time: MessageTime{Created: 300}}
	parts := []MessagePart{{ID: "prt_u_fail", SessionID: rec.ID, MessageID: user.ID, Type: "text", Text: "请继续处理"}}
	if err := svc.startOpenCodeTurn(rec, "build", "gpt-missing", "openai", "", user, parts, parts); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		svc.mu.Lock()
		busy := svc.procs[rec.ID] != nil
		svc.mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	mu.Lock()
	gotCreated := createdCount
	mu.Unlock()
	if gotCreated != 0 {
		t.Fatalf("model-not-found must never rotate OpenCode session, got %d /session creations", gotCreated)
	}

	var sawSessionError bool
	for len(ch) > 0 {
		evt := <-ch
		if evt.Type == "session.error" {
			sawSessionError = true
		}
	}
	if !sawSessionError {
		t.Fatal("expected session.error event to be broadcast")
	}

	stored, ok := svc.store.Get(rec.ID)
	if !ok {
		t.Fatal("expected session record in store")
	}
	if stored.ExternalID != "ses_oc_modelerr" {
		t.Fatalf("expected ExternalID to stay ses_oc_modelerr, got %q", stored.ExternalID)
	}
	if len(stored.Turns) != 2 {
		t.Fatalf("expected user turn + assistant error turn persisted in store, got %d turns: %+v", len(stored.Turns), stored.Turns)
	}
	if stored.Turns[0].Info.ID != "msg_u_fail" || len(stored.Turns[0].Parts) != 1 || stored.Turns[0].Parts[0].Text != "请继续处理" {
		t.Fatalf("unexpected persisted user turn: %+v", stored.Turns[0])
	}
	if stored.Turns[1].Info.Role != "assistant" || stored.Turns[1].Info.Error == nil || stored.Turns[1].Info.Error.Name != "ProviderModelNotFoundError" {
		t.Fatalf("expected persisted assistant turn with ProviderModelNotFoundError, got %+v", stored.Turns[1].Info)
	}
}
