package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStripLeakedReasoningToolMarkupAndRunawayDetection(t *testing.T) {
	t.Parallel()

	loopTail := strings.Repeat("</invoke>\n</parameter>\n", 12)
	dsmlSample := "先检查 hero_video.liquid 文件内容。\n\n<invoke name=\"bash\">\n<parameter name=\"command\">git status</parameter>\n" + loopTail
	cleaned, leaked := stripLeakedReasoningToolMarkup(dsmlSample)
	if !leaked {
		t.Fatal("expected leaked tool markup to be detected")
	}
	if cleaned != "先检查 hero_video.liquid 文件内容。" {
		t.Fatalf("unexpected cleaned reasoning text: %q", cleaned)
	}
	if !hasRunawayReasoningToolLoop(dsmlSample) {
		t.Fatal("expected runaway closing-tag loop to be detected")
	}

	dsmlFenceSample := "准备调用工具检查样式。\n---\n<｜DSML｜ calls>\n<｜DSML｜invoke name=\"read\">\n</｜DSML｜invoke>\n</｜DSML｜ calls>"
	cleanedFence, leakedFence := stripLeakedReasoningToolMarkup(dsmlFenceSample)
	if !leakedFence || cleanedFence != "准备调用工具检查样式。" {
		t.Fatalf("unexpected DSML fence cleanup: leaked=%v cleaned=%q", leakedFence, cleanedFence)
	}

	normalReasoning := "We should inspect `<div>` and `<span>` tags in the template before editing."
	if got, changed := stripLeakedReasoningToolMarkup(normalReasoning); changed || got != normalReasoning {
		t.Fatalf("normal reasoning should not be modified: changed=%v got=%q", changed, got)
	}
	if hasRunawayReasoningToolLoop(normalReasoning) {
		t.Fatal("normal reasoning should not trigger runaway loop detection")
	}
}

func TestSanitizeOpenCodeToolJSONCleansLeakedReasoningMarkup(t *testing.T) {
	t.Parallel()

	loop := strings.Repeat("</invoke>\n</parameter>\n", 8)
	turns := []MessageTurn{
		{
			Info: MessageInfo{ID: "msg_a1", Role: "assistant"},
			Parts: []MessagePart{
				{
					ID:        "prt_r1",
					MessageID: "msg_a1",
					Type:      "reasoning",
					Text:      "查看 schema 配置。\n\n<invoke name=\"bash\">\n<parameter name=\"command\">ls</parameter>\n" + loop,
				},
				{
					ID:        "prt_t1",
					MessageID: "msg_a1",
					Type:      "text",
					Text:      "Literal example: <invoke name=\"bash\"></invoke>",
				},
			},
		},
	}
	raw, err := json.Marshal(turns)
	if err != nil {
		t.Fatal(err)
	}
	sanitized := SanitizeOpenCodeToolJSON(raw)
	var decoded []MessageTurn
	if err := json.Unmarshal(sanitized, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded[0].Parts[0].Text; got != "查看 schema 配置。" {
		t.Fatalf("reasoning part was not sanitized: %q", got)
	}
	if got := decoded[0].Parts[1].Text; got != "Literal example: <invoke name=\"bash\"></invoke>" {
		t.Fatalf("text part should remain untouched: %q", got)
	}
}

func TestRelayOpenCodeEventAbortsRunawayDeltaAndFiltersRecoveryMessages(t *testing.T) {
	t.Parallel()

	var abortCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/abort") {
			abortCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	svc.ConfigureOpenCode(srv.URL, "secret")
	rec := SessionRecord{
		ID:           "ses_hoxi_guard",
		ExternalID:   "ses_oc_guard",
		Runtime:      "opencode",
		ProjectDir:   root,
		WorktreePath: root,
		SandboxMode:  "local",
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	ap := &activeProcess{sessionID: rec.ID, runtime: "opencode", done: make(chan struct{}), idleCh: make(chan struct{}, 1)}
	svc.mu.Lock()
	svc.procs[rec.ID] = ap
	svc.mu.Unlock()

	ch, unsub := svc.Subscribe(rec.ID)
	defer unsub()

	// 1. Internal recovery user messages and deltas must never be broadcast.
	recoverMsgEvt, _ := json.Marshal(SSEEvent{
		Type: "message.updated",
		Properties: map[string]interface{}{
			"info": MessageInfo{ID: "msg_u_recover_1700000000_1", SessionID: rec.ExternalID, Role: "user"},
		},
	})
	svc.relayOpenCodeEvent(rec, recoverMsgEvt)

	// 2. Runaway reasoning delta loop should trigger abort and drop subsequent deltas.
	runawayDelta, _ := json.Marshal(SSEEvent{
		Type: "message.part.delta",
		Properties: map[string]interface{}{
			"sessionID": rec.ExternalID,
			"messageID": "msg_a_loop",
			"partID":    "prt_r_loop",
			"field":     "text",
			"delta":     strings.Repeat("</invoke>\n</parameter>\n", 8),
		},
	})
	svc.relayOpenCodeEvent(rec, runawayDelta)

	// 3. Synthetic session.error emitted by OpenCode when our runaway guard aborts
	// the step must be suppressed rather than surfacing as a user-facing error.
	abortSessionErr, _ := json.Marshal(SSEEvent{
		Type: "session.error",
		Properties: map[string]interface{}{
			"sessionID": rec.ExternalID,
			"error":     map[string]interface{}{"name": "MessageAbortedError", "data": map[string]interface{}{"message": "Aborted"}},
		},
	})
	svc.relayOpenCodeEvent(rec, abortSessionErr)

	deadline := time.Now().Add(2 * time.Second)
	for abortCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if abortCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 abort call on runaway reasoning loop, got %d", abortCalls.Load())
	}

	select {
	case unexpected := <-ch:
		t.Fatalf("expected recovery message and runaway delta to be suppressed, got: %+v", unexpected)
	default:
	}
}

func TestStartOpenCodeTurnAutoRecoversFromReasoningOnlyTruncation(t *testing.T) {
	t.Parallel()

	var (
		mu           sync.Mutex
		postedMsgIDs []string
		turns        []MessageTurn
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
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_oc_recover/message":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			msgID, _ := body["messageID"].(string)
			mu.Lock()
			postedMsgIDs = append(postedMsgIDs, msgID)
			callNum := len(postedMsgIDs)
			if callNum == 1 {
				turns = []MessageTurn{
					{
						Info:  MessageInfo{ID: msgID, SessionID: "ses_oc_recover", Role: "user", Time: MessageTime{Created: 100}},
						Parts: []MessagePart{{ID: "prt_u1", SessionID: "ses_oc_recover", MessageID: msgID, Type: "text", Text: "继续执行"}},
					},
					{
						Info: MessageInfo{
							ID: "msg_a1", SessionID: "ses_oc_recover", Role: "assistant", ParentID: msgID,
							Finish: "length", Time: MessageTime{Created: 101, Completed: 102},
						},
						Parts: []MessagePart{{
							ID: "prt_r1", SessionID: "ses_oc_recover", MessageID: "msg_a1", Type: "reasoning",
							Text: "准备修改文件。\n\n<invoke name=\"bash\">\n<parameter name=\"command\">ls</parameter>\n" + strings.Repeat("</invoke>\n</parameter>\n", 8),
						}},
					},
				}
			} else {
				turns = append(turns,
					MessageTurn{
						Info:  MessageInfo{ID: msgID, SessionID: "ses_oc_recover", Role: "user", Time: MessageTime{Created: 103}},
						Parts: []MessagePart{{ID: "prt_u_rec", SessionID: "ses_oc_recover", MessageID: msgID, Type: "text", Text: openCodeReasoningRecoveryPrompt}},
					},
					MessageTurn{
						Info: MessageInfo{
							ID: "msg_a2", SessionID: "ses_oc_recover", Role: "assistant", ParentID: msgID,
							Finish: "stop", Time: MessageTime{Created: 104, Completed: 105},
						},
						Parts: []MessagePart{
							{ID: "prt_r2", SessionID: "ses_oc_recover", MessageID: "msg_a2", Type: "reasoning", Text: "继续完成修改。"},
							{ID: "prt_t2", SessionID: "ses_oc_recover", MessageID: "msg_a2", Type: "text", Text: "已完成所有模块检查与修复。"},
						},
					},
				)
			}
			mu.Unlock()
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_oc_recover/message":
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
		ID:           "ses_hoxi_recover",
		ExternalID:   "ses_oc_recover",
		Runtime:      "opencode",
		ProjectDir:   root,
		WorktreePath: root,
		SandboxMode:  "local",
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	user := MessageInfo{ID: "msg_u_initial", SessionID: rec.ID, Role: "user", Time: MessageTime{Created: 100}}
	parts := []MessagePart{{ID: "prt_u1", SessionID: rec.ID, MessageID: user.ID, Type: "text", Text: "继续执行"}}
	if err := svc.startOpenCodeTurn(rec, "build", "deepseek-v4", "", "", user, parts, parts); err != nil {
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
	mu.Unlock()
	if len(gotPosts) != 2 || gotPosts[0] != "msg_u_initial" || !isInternalRecoveryMessageID(gotPosts[1]) {
		t.Fatalf("expected initial post + 1 internal recovery post, got: %v", gotPosts)
	}

	stored, ok := svc.store.Get(rec.ID)
	if !ok {
		t.Fatal("expected session record in store")
	}
	for _, turn := range stored.Turns {
		if isInternalRecoveryMessageID(turn.Info.ID) {
			t.Fatalf("internal recovery turn leaked into session store: %+v", turn.Info)
		}
	}
	if len(stored.Turns) != 3 {
		t.Fatalf("expected 3 visible turns (user, first assistant, recovered assistant), got %d", len(stored.Turns))
	}
	if got := stored.Turns[1].Parts[0].Text; got != "准备修改文件。" {
		t.Fatalf("expected leaked tool markup in first assistant turn to be stripped, got %q", got)
	}
	if got := stored.Turns[2].Info.ParentID; got != "msg_u_initial" {
		t.Fatalf("expected recovered assistant turn ParentID to be rewritten to msg_u_initial, got %q", got)
	}
	if got := stored.Turns[2].Parts[1].Text; got != "已完成所有模块检查与修复。" {
		t.Fatalf("unexpected recovered assistant text: %q", got)
	}
}
