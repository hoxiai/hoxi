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
