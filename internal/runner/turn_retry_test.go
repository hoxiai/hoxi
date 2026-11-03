package runner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/testutil/execstub"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestIsRetryableTurnError(t *testing.T) {
	t.Parallel()
	retryableCases := []string{
		`API error (attempt 1): request failed: Post "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse": read tcp 172.19.0.1:58835->172.217.113.4:443: read: connection reset by peer`,
		`Post "https://api.openai.com/v1/chat/completions": dial tcp 1.2.3.4:443: i/o timeout`,
		`context deadline exceeded (Client.Timeout or context cancellation while reading body)`,
		`net/http: TLS handshake timeout`,
		`unexpected EOF`,
		`http2: client connection lost`,
		`502 Bad Gateway`,
		`503 Service Unavailable`,
		`504 Gateway Timeout`,
		`529 Overloaded`,
		`status 429: Too Many Requests - please try again in 2s`,
		`请求超时，请稍后重试`,
		`网络异常：连接中断`,
	}
	for _, tc := range retryableCases {
		if !isRetryableTurnError(tc) {
			t.Fatalf("expected error to be retryable: %q", tc)
		}
	}

	nonRetryableCases := []string{
		"",
		"interrupted",
		"aborted",
		"context canceled",
		"HTTP 401: Invalid API key provided",
		"status 400: prompt is too long (maximum context length exceeded)",
		"insufficient_quota: You exceeded your current quota, please check your plan and billing details",
		"余额不足，请充值",
		"Action blocked by Hoxi safety policy",
		"model_not_found: The model `foo` does not exist",
	}
	for _, tc := range nonRetryableCases {
		if isRetryableTurnError(tc) {
			t.Fatalf("expected error to be non-retryable: %q", tc)
		}
	}

	delayCases := []struct {
		msg  string
		want time.Duration
	}{
		{"please retry after 500ms", 500 * time.Millisecond},
		{"try again in 4s", 4 * time.Second},
		{"请 6 秒后重试", 6 * time.Second},
		{"retry after 2 minutes", maxTurnRetryDelay},
		{"请 1 分钟后重试", maxTurnRetryDelay},
	}
	for _, tc := range delayCases {
		if got := parseTurnRetryDelay(tc.msg); got != tc.want {
			t.Fatalf("parseTurnRetryDelay(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestRunnerTransientErrorAutoRetriesBeforeToolExecution(t *testing.T) {
	repoDir := initTestGitRepo(t)
	storePath := filepath.Join(t.TempDir(), "runner-sessions.json")
	tracker := statusbar.NewTracker()
	wtMgr := worktree.NewManager(repoDir)
	svc := NewService(repoDir, wtMgr, tracker, storePath)
	svc.turnRetryDelays = []time.Duration{15 * time.Millisecond, 30 * time.Millisecond}
	defer svc.CloseAll()

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	createBody, _ := json.Marshal(map[string]string{
		"runtime":     "antigravity",
		"title":       "Retry Test",
		"modelID":     "gemini-4-argon-high",
		"directory":   repoDir,
		"sandboxMode": "worktree",
	})
	recCreate := httptest.NewRecorder()
	mux.ServeHTTP(recCreate, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions", bytes.NewReader(createBody)))
	if recCreate.Code != http.StatusOK {
		t.Fatalf("create session failed (%d): %s", recCreate.Code, recCreate.Body.String())
	}
	var created SessionInfo
	if err := json.Unmarshal(recCreate.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	binRoot := t.TempDir()
	agyDir := filepath.Join(binRoot, "bin")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(created.Directory, ".attempt_count")
	t.Setenv("GEMINI_HOME", binRoot)
	t.Setenv("PATH", agyDir+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("TEST_ATTEMPT_FILE", countFile)

	script := `#!/bin/sh
cat >/dev/null
count=0
if [ -f "$TEST_ATTEMPT_FILE" ]; then
  read -r count < "$TEST_ATTEMPT_FILE"
fi
count=$((count + 1))
printf '%s\n' "$count" > "$TEST_ATTEMPT_FILE"
if [ "$count" -eq 1 ]; then
  printf '%s\n' '{"conversation_id":"conv-retry-1","event":"init","init":{"model":"gemini-4-argon-high","permission_mode":"accept-edits"}}'
  printf '%s\n' '{"conversation_id":"conv-retry-1","event":"step_update","step_update":{"conversation_id":"conv-retry-1","state":"ACTIVE","step_index":1,"step_type":"agent_response","text_delta":"PARTIAL_DRAFT"}}'
  printf '%s\n' '{"conversation_id":"conv-retry-1","event":"result","result":{"conversation_id":"conv-retry-1","error":"API error (attempt 1): request failed: Post \"https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse\": read tcp 172.19.0.1:58835->172.217.113.4:443: read: connection reset by peer","status":"ERROR"}}'
  exit 0
fi
printf '%s\n' '{"conversation_id":"conv-retry-1","event":"init","init":{"model":"gemini-4-argon-high","permission_mode":"accept-edits"}}'
printf '%s\n' '{"conversation_id":"conv-retry-1","event":"step_update","step_update":{"conversation_id":"conv-retry-1","state":"ACTIVE","step_index":1,"step_type":"agent_response","text_delta":"RECOVERED_OK"}}'
printf '%s\n' '{"conversation_id":"conv-retry-1","event":"result","result":{"conversation_id":"conv-retry-1","response":"RECOVERED_OK","status":"DONE"}}'
`
	execstub.Write(t, agyDir, "agy", script)

	ch, unsub := svc.Subscribe(created.ID)
	defer unsub()

	recPrompt := httptest.NewRecorder()
	mux.ServeHTTP(recPrompt, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+created.ID+"/prompt", strings.NewReader(`{"text":"hello"}`)))
	if recPrompt.Code != http.StatusOK {
		t.Fatalf("prompt failed (%d): %s", recPrompt.Code, recPrompt.Body.String())
	}

	deadline := time.After(5 * time.Second)
	sawRetryStatus := false
	sawBusyAfterRetry := false
	sawSessionError := false
	sawIdle := false
	for !sawIdle {
		select {
		case evt := <-ch:
			switch evt.Type {
			case "session.status":
				if st, ok := evt.Properties["status"].(SessionStatus); ok {
					if st.Type == "retry" && st.Attempt == 1 && strings.Contains(st.Message, "connection reset by peer") {
						sawRetryStatus = true
					} else if st.Type == "busy" && sawRetryStatus {
						sawBusyAfterRetry = true
					}
				}
			case "session.error":
				sawSessionError = true
			case "session.idle":
				sawIdle = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for session.idle after transient retry")
		}
	}

	if !sawRetryStatus {
		t.Fatalf("expected session.status retry event before recovery")
	}
	if !sawBusyAfterRetry {
		t.Fatalf("expected session.status busy event when retry process launched")
	}
	if sawSessionError {
		t.Fatalf("did not expect session.error when retry succeeded")
	}

	time.Sleep(30 * time.Millisecond)
	recStored, ok := svc.store.Get(created.ID)
	if !ok || len(recStored.Turns) < 2 {
		t.Fatalf("expected user + assistant turns stored, got %+v", recStored)
	}
	asstTurn := recStored.Turns[len(recStored.Turns)-1]
	if asstTurn.Info.Error != nil {
		t.Fatalf("expected assistant turn without error, got %+v", asstTurn.Info.Error)
	}
	var finalTexts []string
	for _, p := range asstTurn.Parts {
		if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
			finalTexts = append(finalTexts, p.Text)
		}
	}
	if got := strings.Join(finalTexts, ""); got != "RECOVERED_OK" {
		t.Fatalf("expected recovered text RECOVERED_OK without duplicate partial draft, got %q", got)
	}
}

func TestRunnerTransientErrorResumesConversationAfterToolExecution(t *testing.T) {
	repoDir := initTestGitRepo(t)
	storePath := filepath.Join(t.TempDir(), "runner-sessions.json")
	tracker := statusbar.NewTracker()
	wtMgr := worktree.NewManager(repoDir)
	svc := NewService(repoDir, wtMgr, tracker, storePath)
	svc.turnRetryDelays = []time.Duration{15 * time.Millisecond}
	defer svc.CloseAll()

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	createBody, _ := json.Marshal(map[string]string{
		"runtime":     "antigravity",
		"title":       "Resume Tool Retry Test",
		"modelID":     "gemini-4-argon-high",
		"directory":   repoDir,
		"sandboxMode": "worktree",
	})
	recCreate := httptest.NewRecorder()
	mux.ServeHTTP(recCreate, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions", bytes.NewReader(createBody)))
	if recCreate.Code != http.StatusOK {
		t.Fatalf("create session failed (%d): %s", recCreate.Code, recCreate.Body.String())
	}
	var created SessionInfo
	if err := json.Unmarshal(recCreate.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	binRoot := t.TempDir()
	agyDir := filepath.Join(binRoot, "bin")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(created.Directory, ".attempt_tool_count")
	argsFile := filepath.Join(created.Directory, ".attempt2_args")
	t.Setenv("GEMINI_HOME", binRoot)
	t.Setenv("PATH", agyDir+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("TEST_ATTEMPT_FILE", countFile)
	t.Setenv("TEST_ARGS2_FILE", argsFile)

	script := `#!/bin/sh
cat >/dev/null
count=0
if [ -f "$TEST_ATTEMPT_FILE" ]; then
  read -r count < "$TEST_ATTEMPT_FILE"
fi
count=$((count + 1))
printf '%s\n' "$count" > "$TEST_ATTEMPT_FILE"
if [ "$count" -eq 1 ]; then
  printf '%s\n' '{"conversation_id":"conv-resume-77","event":"init","init":{"model":"gemini-4-argon-high","permission_mode":"accept-edits"}}'
  printf '%s\n' '{"conversation_id":"conv-resume-77","event":"step_update","step_update":{"conversation_id":"conv-resume-77","duration_seconds":0.05,"state":"DONE","step_index":1,"step_type":"tool","tool_info":{"name":"run_command","output":"ok\n","parameters":{"CommandLine":"echo ok"}},"tool_name":"run_command"}}'
  printf '%s\n' '{"conversation_id":"conv-resume-77","event":"result","result":{"conversation_id":"conv-resume-77","error":"request failed: i/o timeout","status":"ERROR"}}'
  exit 0
fi
printf '%s\n' "$@" > "$TEST_ARGS2_FILE"
printf '%s\n' '{"conversation_id":"conv-resume-77","event":"step_update","step_update":{"conversation_id":"conv-resume-77","state":"ACTIVE","step_index":2,"step_type":"agent_response","text_delta":"RESUMED_AFTER_TOOL"}}'
printf '%s\n' '{"conversation_id":"conv-resume-77","event":"result","result":{"conversation_id":"conv-resume-77","response":"RESUMED_AFTER_TOOL","status":"DONE"}}'
`
	execstub.Write(t, agyDir, "agy", script)

	ch, unsub := svc.Subscribe(created.ID)
	defer unsub()

	recPrompt := httptest.NewRecorder()
	mux.ServeHTTP(recPrompt, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+created.ID+"/prompt", strings.NewReader(`{"text":"run echo and reply"}`)))
	if recPrompt.Code != http.StatusOK {
		t.Fatalf("prompt failed (%d): %s", recPrompt.Code, recPrompt.Body.String())
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type == "session.idle" {
				goto done
			}
		case <-deadline:
			t.Fatal("timed out waiting for session.idle after resumed tool retry")
		}
	}
done:
	args2Bytes, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("expected attempt 2 args file: %v", err)
	}
	if !strings.Contains(string(args2Bytes), "conv-resume-77") {
		t.Fatalf("expected attempt 2 to pass --conversation conv-resume-77, got:\n%s", string(args2Bytes))
	}

	time.Sleep(30 * time.Millisecond)
	recStored, ok := svc.store.Get(created.ID)
	if !ok || len(recStored.Turns) < 2 {
		t.Fatalf("expected user + assistant turns, got %+v", recStored)
	}
	asstTurn := recStored.Turns[len(recStored.Turns)-1]
	hasTool := false
	hasText := false
	for _, p := range asstTurn.Parts {
		if p.Type == "tool" && p.State != nil && p.State.Status == "completed" {
			hasTool = true
		}
		if p.Type == "text" && strings.Contains(p.Text, "RESUMED_AFTER_TOOL") {
			hasText = true
		}
	}
	if !hasTool || !hasText {
		t.Fatalf("expected both completed tool part and resumed text part, got %+v", asstTurn.Parts)
	}
}

func TestOpenCodeStatusRelayForwardsRetryAndRecoveryBusy(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "store.json"))
	defer svc.CloseAll()

	const sid = "ses_oc_retry_relay"
	ch, unsub := svc.Subscribe(sid)
	defer unsub()

	ap := &activeProcess{sessionID: sid, runtime: "opencode"}
	svc.mu.Lock()
	svc.procs[sid] = ap
	svc.mu.Unlock()
	rec := SessionRecord{ID: sid, Runtime: "opencode", ExternalID: "oc_ext_1"}

	// Ordinary busy status before any retry should not be re-broadcast.
	svc.relayOpenCodeEvent(rec, []byte(`{"type":"session.status","properties":{"sessionID":"oc_ext_1","status":{"type":"busy"}}}`))
	select {
	case evt := <-ch:
		t.Fatalf("unexpected relay of initial busy status: %+v", evt)
	default:
	}

	// Retry status must be forwarded with mapped Hoxi sessionID.
	svc.relayOpenCodeEvent(rec, []byte(`{"type":"session.status","properties":{"sessionID":"oc_ext_1","status":{"type":"retry","attempt":2,"message":"rate limit exceeded","next":123456789}}}`))
	select {
	case evt := <-ch:
		if evt.Type != "session.status" || evt.Properties["sessionID"] != sid {
			t.Fatalf("unexpected retry event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("expected retry status event to be forwarded")
	}

	// Recovery busy status right after retry must also be forwarded once.
	svc.relayOpenCodeEvent(rec, []byte(`{"type":"session.status","properties":{"sessionID":"oc_ext_1","status":{"type":"busy"}}}`))
	select {
	case evt := <-ch:
		if evt.Type != "session.status" || evt.Properties["sessionID"] != sid {
			t.Fatalf("unexpected recovery busy event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("expected recovery busy status event to be forwarded")
	}
}

func TestRunnerAbortDuringRetryBackoffCancelsImmediately(t *testing.T) {
	repoDir := initTestGitRepo(t)
	storePath := filepath.Join(t.TempDir(), "runner-sessions.json")
	tracker := statusbar.NewTracker()
	wtMgr := worktree.NewManager(repoDir)
	svc := NewService(repoDir, wtMgr, tracker, storePath)
	svc.turnRetryDelays = []time.Duration{5 * time.Second}
	defer svc.CloseAll()

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	createBody, _ := json.Marshal(map[string]string{
		"runtime":     "antigravity",
		"title":       "Abort Retry Backoff Test",
		"modelID":     "gemini-4-argon-high",
		"directory":   repoDir,
		"sandboxMode": "worktree",
	})
	recCreate := httptest.NewRecorder()
	mux.ServeHTTP(recCreate, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions", bytes.NewReader(createBody)))
	if recCreate.Code != http.StatusOK {
		t.Fatalf("create session failed (%d): %s", recCreate.Code, recCreate.Body.String())
	}
	var created SessionInfo
	if err := json.Unmarshal(recCreate.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	binRoot := t.TempDir()
	agyDir := filepath.Join(binRoot, "bin")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(created.Directory, ".abort_attempt_count")
	t.Setenv("GEMINI_HOME", binRoot)
	t.Setenv("PATH", agyDir+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("TEST_ATTEMPT_FILE", countFile)

	script := `#!/bin/sh
cat >/dev/null
count=0
if [ -f "$TEST_ATTEMPT_FILE" ]; then
  read -r count < "$TEST_ATTEMPT_FILE"
fi
count=$((count + 1))
printf '%s\n' "$count" > "$TEST_ATTEMPT_FILE"
printf '%s\n' '{"conversation_id":"conv-abort-1","event":"init","init":{"model":"gemini-4-argon-high","permission_mode":"accept-edits"}}'
printf '%s\n' '{"conversation_id":"conv-abort-1","event":"result","result":{"conversation_id":"conv-abort-1","error":"read: connection reset by peer","status":"ERROR"}}'
`
	execstub.Write(t, agyDir, "agy", script)

	ch, unsub := svc.Subscribe(created.ID)
	defer unsub()

	recPrompt := httptest.NewRecorder()
	mux.ServeHTTP(recPrompt, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+created.ID+"/prompt", strings.NewReader(`{"text":"hello"}`)))
	if recPrompt.Code != http.StatusOK {
		t.Fatalf("prompt failed (%d): %s", recPrompt.Code, recPrompt.Body.String())
	}

	deadline := time.After(3 * time.Second)
	aborted := false
	for {
		select {
		case evt := <-ch:
			if evt.Type == "session.status" {
				if st, ok := evt.Properties["status"].(SessionStatus); ok {
					if st.Type == "retry" && !aborted {
						aborted = true
						svc.AbortSession(created.ID)
					} else if st.Type == "idle" && aborted {
						goto finished
					}
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for immediate abort during retry backoff")
		}
	}
finished:
	countBytes, _ := os.ReadFile(countFile)
	if strings.TrimSpace(string(countBytes)) != "1" {
		t.Fatalf("expected only 1 CLI invocation before abort during backoff, got %q", strings.TrimSpace(string(countBytes)))
	}
}

func TestRunnerIgnoresStaleAgyResultErrorWhenTurnResponseCompleted(t *testing.T) {
	repoDir := initTestGitRepo(t)
	storePath := filepath.Join(t.TempDir(), "runner-sessions.json")
	tracker := statusbar.NewTracker()
	wtMgr := worktree.NewManager(repoDir)
	svc := NewService(repoDir, wtMgr, tracker, storePath)
	svc.turnRetryDelays = []time.Duration{15 * time.Millisecond, 30 * time.Millisecond}
	defer svc.CloseAll()

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	createBody, _ := json.Marshal(map[string]string{
		"runtime":     "antigravity",
		"title":       "Stale Result Error Test",
		"modelID":     "gemini-4-argon-high",
		"directory":   repoDir,
		"sandboxMode": "worktree",
	})
	recCreate := httptest.NewRecorder()
	mux.ServeHTTP(recCreate, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions", bytes.NewReader(createBody)))
	if recCreate.Code != http.StatusOK {
		t.Fatalf("create session failed (%d): %s", recCreate.Code, recCreate.Body.String())
	}
	var created SessionInfo
	if err := json.Unmarshal(recCreate.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	binRoot := t.TempDir()
	agyDir := filepath.Join(binRoot, "bin")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	convID := "conv-stale-err-1"
	logDir := agyTranscriptLogDir(filepath.Join(binRoot, "antigravity-cli", "brain"), convID)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcriptRow := `{"step_index":1,"type":"GENERIC","status":"DONE","content":"The following is a <SYSTEM_MESSAGE> not actually sent by the user.\n\n<SYSTEM_MESSAGE>\n[Message] timestamp=2026-11-01T09:16:08Z sender=system priority=MESSAGE_PRIORITY_LOW content=[Notice] All your subagents and background tasks have been stopped due to server restart. If you need any of them to continue, please restart them.\n</SYSTEM_MESSAGE>"}` + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(transcriptRow), 0o600); err != nil {
		t.Fatal(err)
	}

	countFile := filepath.Join(created.Directory, ".stale_attempt_count")
	t.Setenv("GEMINI_HOME", binRoot)
	t.Setenv("PATH", agyDir+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("TEST_ATTEMPT_FILE", countFile)

	script := `#!/bin/sh
cat >/dev/null
count=0
if [ -f "$TEST_ATTEMPT_FILE" ]; then
  read -r count < "$TEST_ATTEMPT_FILE"
fi
count=$((count + 1))
printf '%s\n' "$count" > "$TEST_ATTEMPT_FILE"
printf '%s\n' '{"conversation_id":"conv-stale-err-1","event":"init","init":{"model":"gemini-4-argon-high","permission_mode":"accept-edits"}}'
printf '%s\n' '{"conversation_id":"conv-stale-err-1","event":"step_update","step_update":{"conversation_id":"conv-stale-err-1","state":"DONE","step_index":1,"step_type":"system_message"}}'
printf '%s\n' '{"conversation_id":"conv-stale-err-1","event":"step_update","step_update":{"conversation_id":"conv-stale-err-1","duration_seconds":0.05,"state":"DONE","step_index":2,"step_type":"tool","tool_info":{"name":"run_command","output":"ok\n","parameters":{"CommandLine":"echo ok"}},"tool_name":"run_command"}}'
printf '%s\n' '{"conversation_id":"conv-stale-err-1","event":"step_update","step_update":{"conversation_id":"conv-stale-err-1","state":"ACTIVE","step_index":3,"step_type":"agent_response","text_delta":"所有改动与验证均已完成并提交。"}}'
printf '%s\n' '{"conversation_id":"conv-stale-err-1","event":"step_update","step_update":{"conversation_id":"conv-stale-err-1","duration_seconds":1.1,"state":"DONE","step_index":3,"step_type":"agent_response"}}'
printf '%s\n' '{"conversation_id":"conv-stale-err-1","event":"result","result":{"conversation_id":"conv-stale-err-1","error":"There was a network issue connecting to the server, please try again.","status":"ERROR"}}'
`
	execstub.Write(t, agyDir, "agy", script)

	ch, unsub := svc.Subscribe(created.ID)
	defer unsub()

	recPrompt := httptest.NewRecorder()
	mux.ServeHTTP(recPrompt, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+created.ID+"/prompt", strings.NewReader(`{"text":"status"}`)))
	if recPrompt.Code != http.StatusOK {
		t.Fatalf("prompt failed (%d): %s", recPrompt.Code, recPrompt.Body.String())
	}

	deadline := time.After(5 * time.Second)
	sawRetry := false
	sawSessionError := false
	sawIdle := false
	for !sawIdle {
		select {
		case evt := <-ch:
			switch evt.Type {
			case "session.status":
				if st, ok := evt.Properties["status"].(SessionStatus); ok && st.Type == "retry" {
					sawRetry = true
				}
			case "session.error":
				sawSessionError = true
			case "session.idle":
				sawIdle = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for session.idle")
		}
	}

	if sawRetry {
		t.Fatal("did not expect retry when final agent_response already completed")
	}
	if sawSessionError {
		t.Fatal("did not expect session.error when final agent_response already completed")
	}
	countBytes, _ := os.ReadFile(countFile)
	if strings.TrimSpace(string(countBytes)) != "1" {
		t.Fatalf("expected exactly 1 agy invocation, got %q", strings.TrimSpace(string(countBytes)))
	}

	time.Sleep(30 * time.Millisecond)
	recStored, ok := svc.store.Get(created.ID)
	if !ok || len(recStored.Turns) < 2 {
		t.Fatalf("expected user + assistant turns stored, got %+v", recStored)
	}
	asstTurn := recStored.Turns[len(recStored.Turns)-1]
	if asstTurn.Info.Error != nil || asstTurn.Info.Finish != "stop" {
		t.Fatalf("expected clean assistant turn (finish=stop, err=nil), got finish=%q err=%+v", asstTurn.Info.Finish, asstTurn.Info.Error)
	}
	for _, p := range asstTurn.Parts {
		if p.Type == "tool" && p.Tool == "agent_report" {
			t.Fatalf("did not expect server-restart system_message to create agent_report tool part: %+v", p)
		}
	}
}

func TestLookupAgyTranscriptResetsOnStepZeroUserInput(t *testing.T) {
	binRoot := t.TempDir()
	t.Setenv("GEMINI_HOME", binRoot)

	convID := "conv-step-reset-1"
	logDir := agyTranscriptLogDir(filepath.Join(binRoot, "antigravity-cli", "brain"), convID)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rows := strings.Join([]string{
		`{"step_index":0,"type":"USER_INPUT","content":"Turn 1 question"}`,
		`{"step_index":1,"type":"PLANNER_RESPONSE","thinking":"Turn 1 stale thinking step 1","tool_calls":[{"name":"write_file","args":{"Path":"old.go","Content":"old-body"}}]}`,
		`{"step_index":2,"type":"PLANNER_RESPONSE","thinking":"Turn 1 stale thinking step 2","content":"Turn 1 answer"}`,
		`{"step_index":0,"type":"USER_INPUT","content":"Turn 2 question after split-brain reset"}`,
		`{"step_index":1,"type":"PLANNER_RESPONSE","content":"Turn 2 step 1 without thinking","tool_calls":[{"name":"write_file","args":{"Path":"new.go","Content":"new-body"}}]}`,
		`{"step_index":2,"type":"PLANNER_RESPONSE","thinking":"Turn 2 fresh thinking step 2","content":"Turn 2 answer"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "transcript_full.jsonl"), []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := lookupAgyTranscriptThinking(convID, 1); got != "" {
		t.Fatalf("expected step 1 after reset with empty thinking not to leak Turn 1 thinking, got %q", got)
	}
	if got := lookupAgyTranscriptThinking(convID, 2); got != "Turn 2 fresh thinking step 2" {
		t.Fatalf("expected step 2 to return Turn 2 thinking, got %q", got)
	}

	cursor := newAgyEditPayloadCursor(convID)
	name, args := cursor.lookup(2, map[string]interface{}{"Path": "new.go"}, false)
	if name != "write_file" || args["Content"] != "new-body" {
		t.Fatalf("expected edit payload cursor to return post-reset edit for step 2, got name=%q args=%+v", name, args)
	}
}

func TestRunnerAgySplitBrainConversationRecoversWithFullPlanContext(t *testing.T) {
	repoDir := initTestGitRepo(t)
	storePath := filepath.Join(t.TempDir(), "runner-sessions.json")
	tracker := statusbar.NewTracker()
	wtMgr := worktree.NewManager(repoDir)
	svc := NewService(repoDir, wtMgr, tracker, storePath)
	defer svc.CloseAll()

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	createBody, _ := json.Marshal(map[string]string{
		"runtime":     "antigravity",
		"title":       "Backup Plan Split-Brain Test",
		"modelID":     "gemini-4-argon-high",
		"directory":   repoDir,
		"sandboxMode": "worktree",
	})
	recCreate := httptest.NewRecorder()
	mux.ServeHTTP(recCreate, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions", bytes.NewReader(createBody)))
	if recCreate.Code != http.StatusOK {
		t.Fatalf("create session failed (%d): %s", recCreate.Code, recCreate.Body.String())
	}
	var created SessionInfo
	if err := json.Unmarshal(recCreate.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	binRoot := t.TempDir()
	agyDir := filepath.Join(binRoot, "bin")
	convDir := filepath.Join(binRoot, "antigravity-cli", "conversations")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate Turn 1 having already completed with a 4,500-char backup plan,
	// while agy wrote conv-split-1.pb + stub conv-split-1.db due to SQLite lock.
	longPlan := "## 备份架构规划\n" + strings.Repeat("详细数据库备份与恢复阶段性实施细则说明段落。", 180) + "\nTAIL_MARKER_BACKUP_PLAN_END"
	_, _, _ = svc.store.Update(created.ID, func(r *SessionRecord) {
		r.ExternalID = "conv-split-1"
		r.ContextPending = false
		r.Turns = []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_u_1", SessionID: created.ID, Role: "user"},
				Parts: []MessagePart{{ID: "prt_u_1", Type: "text", Text: "帮我规划一下关于数据库备份的方案"}},
			},
			{
				Info:  MessageInfo{ID: "msg_a_1", SessionID: created.ID, Role: "assistant"},
				Parts: []MessagePart{{ID: "prt_a_1", Type: "text", Text: longPlan}},
			},
		}
	})
	if err := os.WriteFile(filepath.Join(convDir, "conv-split-1.pb"), []byte("proto-trajectory-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "conv-split-1.db"), []byte("sqlite-empty-stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Healthy conversation created on Turn 2 has only a .db file (no .pb file).
	if err := os.WriteFile(filepath.Join(convDir, "conv-healed-2.db"), []byte("sqlite-healthy-trajectory"), 0o600); err != nil {
		t.Fatal(err)
	}

	argsFile := filepath.Join(created.Directory, ".split_args")
	stdinFile := filepath.Join(created.Directory, ".split_stdin")
	t.Setenv("GEMINI_HOME", binRoot)
	t.Setenv("PATH", agyDir+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("TEST_SPLIT_ARGS", argsFile)
	t.Setenv("TEST_SPLIT_STDIN", stdinFile)

	script := `#!/bin/sh
printf '%s\n' "$@" > "$TEST_SPLIT_ARGS"
cat > "$TEST_SPLIT_STDIN"
printf '%s\n' '{"conversation_id":"conv-healed-2","event":"init","init":{"model":"gemini-4-argon-high","permission_mode":"accept-edits"}}'
printf '%s\n' '{"conversation_id":"conv-healed-2","event":"step_update","step_update":{"conversation_id":"conv-healed-2","state":"ACTIVE","step_index":1,"step_type":"agent_response","text_delta":"STARTED_IMPLEMENTATION"}}'
printf '%s\n' '{"conversation_id":"conv-healed-2","event":"result","result":{"conversation_id":"conv-healed-2","response":"STARTED_IMPLEMENTATION","status":"DONE"}}'
`
	execstub.Write(t, agyDir, "agy", script)

	ch, unsub := svc.Subscribe(created.ID)
	defer unsub()

	// Turn 2: should detect conv-split-1 (.pb + .db), omit --conversation conv-split-1,
	// and inject Turn 1's full backup plan including TAIL_MARKER_BACKUP_PLAN_END.
	recPrompt := httptest.NewRecorder()
	mux.ServeHTTP(recPrompt, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+created.ID+"/prompt", strings.NewReader(`{"text":"好，结合项目实际情况开始做"}`)))
	if recPrompt.Code != http.StatusOK {
		t.Fatalf("prompt 2 failed (%d): %s", recPrompt.Code, recPrompt.Body.String())
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type == "session.idle" {
				goto turn2Done
			}
		case <-deadline:
			t.Fatal("timed out waiting for Turn 2 session.idle")
		}
	}
turn2Done:
	argsRaw, _ := os.ReadFile(argsFile)
	if strings.Contains(string(argsRaw), "conv-split-1") {
		t.Fatalf("expected Turn 2 not to pass broken --conversation conv-split-1, got args:\n%s", string(argsRaw))
	}
	stdinRaw, _ := os.ReadFile(stdinFile)
	if !strings.Contains(string(stdinRaw), "TAIL_MARKER_BACKUP_PLAN_END") {
		t.Fatalf("expected Turn 2 stdin to carry the full Turn 1 backup plan tail marker, got len=%d:\n%s", len(stdinRaw), string(stdinRaw))
	}

	// Turn 3: conv-healed-2 only has .db (no .pb), so it must resume natively via --conversation conv-healed-2.
	recPrompt3 := httptest.NewRecorder()
	mux.ServeHTTP(recPrompt3, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+created.ID+"/prompt", strings.NewReader(`{"text":"继续下一步"}`)))
	if recPrompt3.Code != http.StatusOK {
		t.Fatalf("prompt 3 failed (%d): %s", recPrompt3.Code, recPrompt3.Body.String())
	}
	deadline3 := time.After(5 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type == "session.idle" {
				goto turn3Done
			}
		case <-deadline3:
			t.Fatal("timed out waiting for Turn 3 session.idle")
		}
	}
turn3Done:
	args3Raw, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args3Raw), "conv-healed-2") {
		t.Fatalf("expected Turn 3 to resume healthy --conversation conv-healed-2, got args:\n%s", string(args3Raw))
	}
}
