package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/testutil/execstub"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestGracefulRestartStepDrainAndCLIAgyAutoResume(t *testing.T) {
	repoDir := initTestGitRepo(t)
	stateRoot := t.TempDir()
	storeDir := filepath.Join(stateRoot, "sessions")

	binRoot := t.TempDir()
	agyDir := filepath.Join(binRoot, "bin")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(stateRoot, "agy_args.log")
	stepDoneFile := filepath.Join(stateRoot, "step1_done.marker")
	t.Setenv("GEMINI_HOME", binRoot)
	t.Setenv("PATH", agyBinPathWithSystem(agyDir))
	t.Setenv("TEST_ARGS_FILE", argsFile)
	t.Setenv("TEST_STEP_DONE", stepDoneFile)

	// Run 1 emits an ACTIVE tool step, finishes it after 120ms, then pauses thinking.
	// Run 2 (--conversation conv-restart-1) finishes the turn cleanly.
	execstub.Write(t, agyDir, "agy", `#!/bin/sh
printf '%s\n' "$*" >> "$TEST_ARGS_FILE"
cat >/dev/null
case "$*" in
  *--conversation\ conv-restart-1*)
    printf '%s\n' '{"conversation_id":"conv-restart-1","event":"init","init":{"model":"claude-sonnet-4-6","permission_mode":"accept-edits"}}'
    printf '%s\n' '{"conversation_id":"conv-restart-1","event":"step_update","step_update":{"conversation_id":"conv-restart-1","state":"ACTIVE","step_index":1,"step_type":"agent_response","text_delta":"RESUMED_COMPLETE"}}'
    printf '%s\n' '{"conversation_id":"conv-restart-1","event":"result","result":{"conversation_id":"conv-restart-1","response":"RESUMED_COMPLETE","status":"DONE"}}'
    ;;
  *)
    trap 'exit 130' INT TERM
    printf '%s\n' '{"conversation_id":"conv-restart-1","event":"init","init":{"model":"claude-sonnet-4-6","permission_mode":"accept-edits"}}'
    printf '%s\n' '{"conversation_id":"conv-restart-1","event":"step_update","step_update":{"conversation_id":"conv-restart-1","state":"ACTIVE","step_index":1,"step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"echo step1"}}}}'
    sleep 0.12
    printf 'ok\n' > "$TEST_STEP_DONE"
    printf '%s\n' '{"conversation_id":"conv-restart-1","event":"step_update","step_update":{"conversation_id":"conv-restart-1","state":"DONE","step_index":1,"step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","output":"step1\n","parameters":{"CommandLine":"echo step1"}}}}'
    sleep 10
    ;;
esac
`)

	svc1 := NewService(repoDir, worktree.NewManager(repoDir), statusbar.NewTracker(), storeDir)
	mux1 := http.NewServeMux()
	svc1.RegisterRoutes(mux1)

	createBody, _ := json.Marshal(map[string]string{
		"runtime":     "antigravity",
		"title":       "Restart CLI Test",
		"modelID":     "claude-sonnet-4-6",
		"directory":   repoDir,
		"sandboxMode": "worktree",
	})
	recCreate := httptest.NewRecorder()
	mux1.ServeHTTP(recCreate, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions", bytes.NewReader(createBody)))
	if recCreate.Code != http.StatusOK {
		t.Fatalf("create session failed: %d %s", recCreate.Code, recCreate.Body.String())
	}
	var created SessionInfo
	if err := json.Unmarshal(recCreate.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	events1, unsub1 := svc1.Subscribe(created.ID)
	defer unsub1()

	recPrompt := httptest.NewRecorder()
	mux1.ServeHTTP(recPrompt, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+created.ID+"/prompt_async", strings.NewReader(`{"text":"implement feature across restart"}`)))
	if recPrompt.Code != http.StatusOK {
		t.Fatalf("prompt_async failed: %d %s", recPrompt.Code, recPrompt.Body.String())
	}

	// Wait until the first tool step enters "running" state, then trigger graceful restart.
	sawRunningTool := false
	deadline := time.After(4 * time.Second)
	for !sawRunningTool {
		select {
		case evt := <-events1:
			if evt.Type == "message.part.updated" {
				if p, ok := evt.Properties["part"].(MessagePart); ok && p.Type == "tool" && p.State != nil && p.State.Status == "running" {
					sawRunningTool = true
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for running tool part")
		}
	}

	prepCtx, prepCancel := context.WithTimeout(context.Background(), 5*time.Second)
	svc1.PrepareForRestart(prepCtx)
	prepCancel()

	// Step drain must have waited for the active 120ms tool step to complete cleanly.
	if _, err := os.Stat(stepDoneFile); err != nil {
		t.Fatalf("expected PrepareForRestart to drain running tool step before stopping CLI: %v", err)
	}
	ledgerPath := filepath.Join(stateRoot, taskLedgerFile)
	info, err := os.Stat(ledgerPath)
	if err != nil {
		t.Fatalf("expected task-ledger.json on disk: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 permissions on task-ledger.json, got %o", info.Mode().Perm())
	}

	// Boot new Service instance simulating restarted Hoxi binary.
	svc2 := NewService(repoDir, worktree.NewManager(repoDir), statusbar.NewTracker(), storeDir)
	defer svc2.CloseAll()
	events2, unsub2 := svc2.Subscribe(created.ID)
	defer unsub2()

	svc2.ResumeInterruptedTurns(context.Background())
	waitForEvent(t, events2, "session.idle")

	for waitDeadline := time.Now().Add(2 * time.Second); len(svc2.busySessionIDs()) > 0 && time.Now().Before(waitDeadline); {
		time.Sleep(10 * time.Millisecond)
	}

	argsRaw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(argsRaw), "--conversation conv-restart-1") {
		t.Fatalf("expected resumed CLI to pass --conversation conv-restart-1, got:\n%s", string(argsRaw))
	}

	stored, ok := svc2.store.Get(created.ID)
	if !ok {
		t.Fatal("session missing after resume")
	}
	userCount := 0
	sawResumedText := false
	sawCompletedStep1 := false
	for _, turn := range stored.Turns {
		if turn.Info.Role == "user" {
			userCount++
		}
		for _, p := range turn.Parts {
			if p.Type == "text" && strings.Contains(p.Text, "RESUMED_COMPLETE") {
				sawResumedText = true
			}
			if p.Type == "tool" && p.State != nil && p.State.Status == "completed" {
				sawCompletedStep1 = true
			}
		}
	}
	if userCount != 1 {
		t.Fatalf("expected exactly 1 user turn (no duplicate bubble), got %d", userCount)
	}
	if !sawCompletedStep1 || !sawResumedText {
		t.Fatalf("expected completed tool step (%v) and resumed final text (%v)", sawCompletedStep1, sawResumedText)
	}
	if entries := svc2.ledger.listEntries(); len(entries) != 0 {
		t.Fatalf("expected ledger entries cleared after resumed turn idle, got %+v", entries)
	}
}

func TestOpenCodeGracefulRestartAutoResumesWithByteStableSystemPrompt(t *testing.T) {
	repoDir := initTestGitRepo(t)
	stateRoot := t.TempDir()
	storeDir := filepath.Join(stateRoot, "sessions")

	var (
		mu             sync.Mutex
		systems        []string
		messageIDs     []string
		messagesInDB   []MessageTurn
		sseSubscribers []chan string
		firstEntered   = make(chan struct{})
		firstOnce      sync.Once
		msgCalls       atomic.Int32
	)

	broadcastSSE := func(evt map[string]interface{}) {
		raw, _ := json.Marshal(evt)
		line := "data: " + string(raw) + "\n\n"
		mu.Lock()
		subs := append([]chan string(nil), sseSubscribers...)
		mu.Unlock()
		for _, ch := range subs {
			select {
			case ch <- line:
			default:
			}
		}
	}

	ocServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/config/providers":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"providers":[{"id":"anthropic","source":"config","models":{"claude-sonnet-4-6":{}}}],"default":{"anthropic":"claude-sonnet-4-6"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"oc_ses_restart_1","title":"OpenCode Restart"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			ch := make(chan string, 32)
			mu.Lock()
			sseSubscribers = append(sseSubscribers, ch)
			mu.Unlock()
			if flusher != nil {
				flusher.Flush()
			}
			for {
				select {
				case <-r.Context().Done():
					return
				case msg := <-ch:
					_, _ = fmt.Fprint(w, msg)
					if flusher != nil {
						flusher.Flush()
					}
				}
			}
		case r.Method == http.MethodPost && r.URL.Path == "/session/oc_ses_restart_1/abort":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && (r.URL.Path == "/permission" || r.URL.Path == "/question"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"oc_ses_restart_1":{"type":"idle"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/oc_ses_restart_1/message":
			mu.Lock()
			out := append([]MessageTurn(nil), messagesInDB...)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(out)
		case r.Method == http.MethodPost && r.URL.Path == "/session/oc_ses_restart_1/message":
			var body struct {
				MessageID string                   `json:"messageID"`
				System    string                   `json:"system"`
				Parts     []map[string]interface{} `json:"parts"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			callNum := msgCalls.Add(1)
			mu.Lock()
			systems = append(systems, body.System)
			messageIDs = append(messageIDs, body.MessageID)
			mu.Unlock()

			if callNum == 1 {
				now := time.Now().UnixMilli()
				userTurn := MessageTurn{
					Info:  MessageInfo{ID: body.MessageID, SessionID: "oc_ses_restart_1", Role: "user", Time: MessageTime{Created: now}},
					Parts: []MessagePart{{ID: "prt_u_1", SessionID: "oc_ses_restart_1", MessageID: body.MessageID, Type: "text", Text: "build feature"}},
				}
				mu.Lock()
				messagesInDB = append(messagesInDB, userTurn)
				mu.Unlock()
				broadcastSSE(map[string]interface{}{"type": "message.updated", "properties": map[string]interface{}{"info": userTurn.Info}})
				broadcastSSE(map[string]interface{}{"type": "message.part.updated", "properties": map[string]interface{}{"part": userTurn.Parts[0]}})
				firstOnce.Do(func() { close(firstEntered) })
				<-r.Context().Done()
				w.WriteHeader(http.StatusRequestTimeout)
				return
			}

			now := time.Now().UnixMilli()
			asstInfo := MessageInfo{
				ID:        "msg_a_oc_resumed",
				SessionID: "oc_ses_restart_1",
				Role:      "assistant",
				ParentID:  body.MessageID,
				Time:      MessageTime{Created: now, Completed: now + 10},
				Finish:    "stop",
			}
			asstPart := MessagePart{
				ID:        "prt_a_oc_resumed",
				SessionID: "oc_ses_restart_1",
				MessageID: asstInfo.ID,
				Type:      "text",
				Text:      "OPENCODE_RESUMED_OK",
			}
			mu.Lock()
			messagesInDB = append(messagesInDB, MessageTurn{Info: asstInfo, Parts: []MessagePart{asstPart}})
			mu.Unlock()
			broadcastSSE(map[string]interface{}{"type": "message.updated", "properties": map[string]interface{}{"info": asstInfo}})
			broadcastSSE(map[string]interface{}{"type": "message.part.updated", "properties": map[string]interface{}{"part": asstPart}})
			broadcastSSE(map[string]interface{}{"type": "session.idle", "properties": map[string]interface{}{"sessionID": "oc_ses_restart_1"}})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ocServer.Close()

	svc1 := NewService(repoDir, worktree.NewManager(repoDir), statusbar.NewTracker(), storeDir)
	svc1.ConfigureOpenCode(ocServer.URL, "secret")
	info, _, err := svc1.CreateConversation(context.Background(), CreateSessionRequest{
		Title:       "OC Restart",
		Directory:   repoDir,
		Runtime:     "opencode",
		ProviderID:  "anthropic",
		ModelID:     "claude-sonnet-4-6",
		SandboxMode: "local",
	}, repoDir)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	_, _, err = svc1.startPromptWithReceipt(context.Background(), info.ID, repoDir, PromptRequest{
		Text:   "build feature",
		Agent:  "build",
		System: "Custom Project Rules",
	})
	if err != nil {
		t.Fatalf("startPromptWithReceipt: %v", err)
	}

	select {
	case <-firstEntered:
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for OpenCode turn to start")
	}

	svc1.PrepareForRestart(context.Background())

	svc2 := NewService(repoDir, worktree.NewManager(repoDir), statusbar.NewTracker(), storeDir)
	defer svc2.CloseAll()
	svc2.ConfigureOpenCode(ocServer.URL, "secret")
	events2, unsub2 := svc2.Subscribe(info.ID)
	defer unsub2()

	svc2.ResumeInterruptedTurns(context.Background())
	waitForEvent(t, events2, "session.status")
	for deadline := time.Now().Add(4 * time.Second); len(svc2.busySessionIDs()) > 0 && time.Now().Before(deadline); {
		time.Sleep(15 * time.Millisecond)
	}

	mu.Lock()
	gotSystems := append([]string(nil), systems...)
	gotMsgIDs := append([]string(nil), messageIDs...)
	mu.Unlock()

	if len(gotSystems) != 2 {
		t.Fatalf("expected 2 OpenCode message calls (initial + resumed), got %d", len(gotSystems))
	}
	if gotSystems[0] == "" || gotSystems[0] != gotSystems[1] {
		t.Fatalf("system prompt drifted across restart (breaks KV cache):\ninitial=%q\nresumed=%q", gotSystems[0], gotSystems[1])
	}
	if !strings.HasPrefix(gotMsgIDs[1], internalRecoveryMsgPrefix) {
		t.Fatalf("expected resumed message ID to use hidden internal prefix %q, got %q", internalRecoveryMsgPrefix, gotMsgIDs[1])
	}

	stored, ok := svc2.store.Get(info.ID)
	if !ok {
		t.Fatal("session missing in store")
	}
	sawFinal := false
	for _, tTurn := range stored.Turns {
		if isInternalRecoveryMessageID(tTurn.Info.ID) {
			t.Fatalf("internal recovery message leaked into visible transcript: %s", tTurn.Info.ID)
		}
		for _, p := range tTurn.Parts {
			if p.Type == "text" && p.Text == "OPENCODE_RESUMED_OK" {
				sawFinal = true
			}
		}
	}
	if !sawFinal {
		t.Fatalf("expected OPENCODE_RESUMED_OK in transcript, got %+v", stored.Turns)
	}
	if entries := svc2.ledger.listEntries(); len(entries) != 0 {
		t.Fatalf("expected ledger empty after completion, got %+v", entries)
	}
}

func TestUngracefulCrashCorruptBackupAndCrossRestartIdempotency(t *testing.T) {
	repoDir := initTestGitRepo(t)
	stateRoot := t.TempDir()
	storeDir := filepath.Join(stateRoot, "sessions")
	ledgerPath := filepath.Join(stateRoot, taskLedgerFile)

	// 1. Corrupt ledger file must be backed up before starting clean (Guardrail #4).
	if err := os.WriteFile(ledgerPath, []byte("{corrupt-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	svcCorrupt := NewService(repoDir, worktree.NewManager(repoDir), statusbar.NewTracker(), storeDir)
	svcCorrupt.CloseAll()

	matches, _ := filepath.Glob(ledgerPath + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("expected corrupt task-ledger.json backup, found %v", matches)
	}

	// 2. Ungraceful crash (GracefulRestart == false) must NOT auto-resume; it must
	// settle dangling running tool parts and stamp RunnerInterruptedError.
	crashedID := "ses_cld_crashed_1"
	now := time.Now().UnixMilli()
	st := NewStore(storeDir)
	if err := st.Put(SessionRecord{
		ID:           crashedID,
		Runtime:      string(RuntimeClaudeCode),
		ProjectDir:   repoDir,
		WorktreePath: repoDir,
		SandboxMode:  "local",
		Title:        "Crashed Session",
		CreatedAt:    now,
		UpdatedAt:    now,
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_u_c1", SessionID: crashedID, Role: "user", Time: MessageTime{Created: now}},
				Parts: []MessagePart{{ID: "prt_u_c1", SessionID: crashedID, MessageID: "msg_u_c1", Type: "text", Text: "run test"}},
			},
			{
				Info: MessageInfo{ID: "msg_a_c1", SessionID: crashedID, Role: "assistant", ParentID: "msg_u_c1", Time: MessageTime{Created: now + 1}},
				Parts: []MessagePart{{
					ID: "prt_t_c1", SessionID: crashedID, MessageID: "msg_a_c1", Type: "tool", Tool: "bash",
					State: &ToolState{Status: "running", Input: map[string]interface{}{"command": "go test ./..."}, Time: &PartTime{Start: now + 1}},
				}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	rawLedger, _ := json.Marshal(taskLedgerDoc{
		Version: taskLedgerVersion,
		Entries: []InflightTurnEntry{{
			SessionID:       crashedID,
			Runtime:         string(RuntimeClaudeCode),
			UserMsgID:       "msg_u_c1",
			AsstMsgID:       "msg_a_c1",
			StartedAt:       now,
			UpdatedAt:       now,
			GracefulRestart: false, // Simulates kill -9 / unexpected crash
		}},
	})
	if err := os.WriteFile(ledgerPath, rawLedger, 0o600); err != nil {
		t.Fatal(err)
	}

	svcAfterCrash := NewService(repoDir, worktree.NewManager(repoDir), statusbar.NewTracker(), storeDir)
	svcAfterCrash.ResumeInterruptedTurns(context.Background())

	settled, ok := svcAfterCrash.store.Get(crashedID)
	if !ok || len(settled.Turns) != 2 {
		t.Fatalf("unexpected settled turns: %+v", settled.Turns)
	}
	lastTurn := settled.Turns[1]
	if lastTurn.Info.Finish != "error" || lastTurn.Info.Error == nil || lastTurn.Info.Error.Name != "RunnerInterruptedError" {
		t.Fatalf("expected crashed assistant turn stamped with RunnerInterruptedError, got %+v", lastTurn.Info)
	}
	if lastTurn.Parts[0].State.Status != "error" || lastTurn.Parts[0].State.Error == "" {
		t.Fatalf("expected dangling running tool settled as error, got %+v", lastTurn.Parts[0].State)
	}
	if len(svcAfterCrash.ledger.listEntries()) != 0 {
		t.Fatalf("expected crashed entry removed from ledger")
	}

	// 3. Cross-restart idempotency receipts (prompt + permission) and delete cleanup.
	svcAfterCrash.storePromptReceipt(crashedID, "client-req-restart-1", PromptReceipt{
		OK:              true,
		InstructionID:   7,
		MessageID:       "msg_u_c1",
		ClientRequestID: "client-req-restart-1",
	})
	svcAfterCrash.storePermissionReply("per_rnr_abc", crashedID, "perm-req-restart-1", "once", []byte("true\n"))
	svcAfterCrash.CloseAll()

	svcReloaded := NewService(repoDir, worktree.NewManager(repoDir), statusbar.NewTracker(), storeDir)
	defer svcReloaded.CloseAll()

	if got, dup := svcReloaded.lookupPromptReceipt(crashedID, "client-req-restart-1"); !dup || got.InstructionID != 7 {
		t.Fatalf("expected prompt receipt deduplicated across restart, got dup=%v receipt=%+v", dup, got)
	}
	if rawReply, dup := svcReloaded.lookupPermissionReply("per_rnr_abc", "perm-req-restart-1", "once"); !dup || string(rawReply) != "true\n" {
		t.Fatalf("expected permission reply deduplicated across restart, got dup=%v raw=%q", dup, string(rawReply))
	}

	if err := svcReloaded.DeleteConversation(context.Background(), crashedID); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	if _, dup := svcReloaded.lookupPromptReceipt(crashedID, "client-req-restart-1"); dup {
		t.Fatal("expected prompt receipt purged on conversation delete")
	}
	svcReloaded2 := NewService(repoDir, worktree.NewManager(repoDir), statusbar.NewTracker(), storeDir)
	defer svcReloaded2.CloseAll()
	if _, dup := svcReloaded2.lookupPermissionReply("per_rnr_abc", "perm-req-restart-1", "once"); dup {
		t.Fatal("expected persisted permission reply purged from disk on conversation delete")
	}
}

func TestDelayedOpenCodeBootSingleflightAndAbort(t *testing.T) {
	repoDir := initTestGitRepo(t)
	stateRoot := t.TempDir()
	storeDir := filepath.Join(stateRoot, "sessions")

	var (
		healthy        atomic.Bool
		resumeMsgCalls atomic.Int32
		sseMu          sync.Mutex
		sseSubs        []chan string
	)
	healthy.Store(true)

	broadcastSSE := func(evt map[string]interface{}) {
		raw, _ := json.Marshal(evt)
		line := "data: " + string(raw) + "\n\n"
		sseMu.Lock()
		subs := append([]chan string(nil), sseSubs...)
		sseMu.Unlock()
		for _, ch := range subs {
			select {
			case ch <- line:
			default:
			}
		}
	}

	ocServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			if !healthy.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/config/providers":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"providers":[{"id":"anthropic","source":"config","models":{"claude-sonnet-4-6":{}}}],"default":{"anthropic":"claude-sonnet-4-6"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			ch := make(chan string, 16)
			sseMu.Lock()
			sseSubs = append(sseSubs, ch)
			sseMu.Unlock()
			if flusher != nil {
				flusher.Flush()
			}
			for {
				select {
				case <-r.Context().Done():
					return
				case msg := <-ch:
					_, _ = fmt.Fprint(w, msg)
					if flusher != nil {
						flusher.Flush()
					}
				}
			}
		case r.Method == http.MethodGet && (r.URL.Path == "/permission" || r.URL.Path == "/question"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"oc_boot_1":{"type":"idle"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/oc_boot_1/message":
			now := time.Now().UnixMilli()
			turns := []MessageTurn{{
				Info:  MessageInfo{ID: "msg_u_boot_1", SessionID: "oc_boot_1", Role: "user", Time: MessageTime{Created: now - 50}},
				Parts: []MessagePart{{ID: "prt_u_boot_1", SessionID: "oc_boot_1", MessageID: "msg_u_boot_1", Type: "text", Text: "run convoy step"}},
			}}
			if resumeMsgCalls.Load() > 0 {
				turns = append(turns, MessageTurn{
					Info:  MessageInfo{ID: "msg_a_boot_1", SessionID: "oc_boot_1", Role: "assistant", ParentID: "msg_u_boot_1", Time: MessageTime{Created: now, Completed: now + 5}, Finish: "stop"},
					Parts: []MessagePart{{ID: "prt_a_boot_1", SessionID: "oc_boot_1", MessageID: "msg_a_boot_1", Type: "text", Text: "SINGLEFLIGHT_DONE"}},
				})
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(turns)
		case r.Method == http.MethodPost && r.URL.Path == "/session/oc_boot_1/message":
			resumeMsgCalls.Add(1)
			now := time.Now().UnixMilli()
			asstInfo := MessageInfo{ID: "msg_a_boot_1", SessionID: "oc_boot_1", Role: "assistant", ParentID: "msg_u_boot_1", Time: MessageTime{Created: now, Completed: now + 5}, Finish: "stop"}
			asstPart := MessagePart{ID: "prt_a_boot_1", SessionID: "oc_boot_1", MessageID: "msg_a_boot_1", Type: "text", Text: "SINGLEFLIGHT_DONE"}
			broadcastSSE(map[string]interface{}{"type": "message.updated", "properties": map[string]interface{}{"info": asstInfo}})
			broadcastSSE(map[string]interface{}{"type": "message.part.updated", "properties": map[string]interface{}{"part": asstPart}})
			broadcastSSE(map[string]interface{}{"type": "session.idle", "properties": map[string]interface{}{"sessionID": "oc_boot_1"}})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer ocServer.Close()

	origPoll := restartEnginePollIntervalMs.Swap(25)
	defer restartEnginePollIntervalMs.Store(origPoll)

	now := time.Now().UnixMilli()
	seedStore := NewStore(storeDir)
	for _, id := range []string{"ses_boot_1", "ses_boot_abort"} {
		ext := "oc_boot_1"
		if id == "ses_boot_abort" {
			ext = "oc_boot_abort"
		}
		if err := seedStore.Put(SessionRecord{
			ID: id, Runtime: "opencode", ExternalID: ext, OpenCodeIDs: []string{ext},
			ProjectDir: repoDir, WorktreePath: repoDir, SandboxMode: "local",
			ProviderID: "anthropic", ModelID: "claude-sonnet-4-6",
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	rawLedger, _ := json.Marshal(taskLedgerDoc{
		Version: taskLedgerVersion,
		Entries: []InflightTurnEntry{
			{SessionID: "ses_boot_1", Runtime: "opencode", ExternalID: "oc_boot_1", UserMsgID: "msg_u_boot_1", Mode: "build", ProviderID: "anthropic", ModelID: "claude-sonnet-4-6", StartedAt: now, UpdatedAt: now, GracefulRestart: true},
			{SessionID: "ses_boot_abort", Runtime: "opencode", ExternalID: "oc_boot_abort", UserMsgID: "msg_u_abort", Mode: "build", ProviderID: "anthropic", ModelID: "claude-sonnet-4-6", StartedAt: now, UpdatedAt: now, GracefulRestart: true},
		},
	})
	if err := os.WriteFile(filepath.Join(stateRoot, taskLedgerFile), rawLedger, 0o600); err != nil {
		t.Fatal(err)
	}

	// Simulate OpenCode daemon taking 150ms to come online after Hoxi restarts.
	healthy.Store(false)
	svc := NewService(repoDir, worktree.NewManager(repoDir), statusbar.NewTracker(), storeDir)
	defer svc.CloseAll()
	svc.ConfigureOpenCode(ocServer.URL, "secret")

	// Zero-flicker check: both sessions must already report busy before ResumeInterruptedTurns runs.
	if !svc.isSessionBusy("ses_boot_1") || !svc.isSessionBusy("ses_boot_abort") {
		t.Fatalf("expected queued boot-resume sessions to report busy immediately, got %v", svc.busySessionIDs())
	}

	svc.ResumeInterruptedTurns(context.Background())

	// Concurrent Convoy ResumeOrRunPromptAndWait must join the same singleflight without duplicate /message calls.
	convoyOutCh := make(chan string, 1)
	convoyErrCh := make(chan error, 1)
	go func() {
		out, err := svc.ResumeOrRunPromptAndWait(context.Background(), "ses_boot_1", PromptRequest{Text: "run convoy step", Agent: "build"})
		if err != nil {
			convoyErrCh <- err
			return
		}
		convoyOutCh <- out
	}()

	// Aborting ses_boot_abort while OpenCode is still unhealthy must return fast without deadlocking turnMu.
	abortDone := make(chan struct{})
	go func() {
		svc.AbortSession("ses_boot_abort")
		close(abortDone)
	}()
	select {
	case <-abortDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("AbortSession hung while OpenCode readiness wait was in flight")
	}
	if svc.isSessionBusy("ses_boot_abort") {
		t.Fatal("expected aborted boot-resume session to clear busy state immediately")
	}

	time.Sleep(120 * time.Millisecond)
	healthy.Store(true)

	select {
	case err := <-convoyErrCh:
		t.Fatalf("ResumeOrRunPromptAndWait error: %v", err)
	case out := <-convoyOutCh:
		if !strings.Contains(out, "SINGLEFLIGHT_DONE") {
			t.Fatalf("unexpected convoy resume outcome: %q", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for singleflight resume completion")
	}

	if got := resumeMsgCalls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 /message call across ResumeInterruptedTurns + Convoy singleflight, got %d", got)
	}
}

func agyBinPathWithSystem(agyDir string) string {
	sys := "/usr/bin:/bin"
	return agyDir + string(os.PathListSeparator) + sys
}
