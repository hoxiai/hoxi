package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/testutil/execstub"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestAgyEventTranslationWithPhase0Fixtures(t *testing.T) {
	t.Parallel()
	tr := NewTurnTranslator("ses_agy_test", string(RuntimeAntigravity), "msg_a_1", "msg_u_1", "antigravity", "claude-sonnet-4-6", "build", "")
	tr.lookupThink = func(extID string, stepIdx int) string {
		if extID == "00000000-0000-0000-0000-000000000001" && stepIdx == 1 {
			return "Checking workspace directory before running command."
		}
		return ""
	}

	fixtures := []string{
		`{"conversation_id":"00000000-0000-0000-0000-000000000001","event":"init","init":{"cwd":"/tmp/.hoxi/worktrees/ses_agy_test","model":"claude-sonnet-4-6","permission_mode":"accept-edits"}}`,
		`{"conversation_id":"00000000-0000-0000-0000-000000000001","event":"step_update","step_update":{"conversation_id":"00000000-0000-0000-0000-000000000001","duration_seconds":2.03,"state":"DONE","step_index":1,"step_type":"agent_response","text_delta":"","thinking":"","thinking_delta":"","tool_name":"","usage":{"cache_read_tokens":0,"input_tokens":10903,"output_tokens":144,"thinking_tokens":45,"total_tokens":11047}}}`,
		`{"conversation_id":"00000000-0000-0000-0000-000000000001","event":"step_update","step_update":{"conversation_id":"00000000-0000-0000-0000-000000000001","duration_seconds":0,"state":"ACTIVE","step_index":2,"step_type":"tool","text_delta":"","thinking":"","thinking_delta":"","tool_info":{"name":"run_command","output":"","parameters":{"CommandLine":"echo ok","Cwd":"/tmp/.hoxi/worktrees/ses_agy_test","WaitMsBeforeAsync":1000,"toolAction":"Running echo command","toolSummary":"Run echo command"}},"tool_name":"run_command"}}`,
		`{"conversation_id":"00000000-0000-0000-0000-000000000001","event":"step_update","step_update":{"conversation_id":"00000000-0000-0000-0000-000000000001","duration_seconds":0.068,"state":"DONE","step_index":2,"step_type":"tool","text_delta":"","thinking":"","thinking_delta":"","tool_info":{"name":"run_command","output":"ok\n","parameters":{"CommandLine":"echo ok","Cwd":"/tmp/.hoxi/worktrees/ses_agy_test","WaitMsBeforeAsync":1000,"toolAction":"Running echo command","toolSummary":"Run echo command"}},"tool_name":"run_command"}}`,
		`{"conversation_id":"00000000-0000-0000-0000-000000000001","event":"step_update","step_update":{"conversation_id":"00000000-0000-0000-0000-000000000001","duration_seconds":0,"state":"ACTIVE","step_index":4,"step_type":"agent_response","text_delta":"PONG_1","thinking":"","thinking_delta":"","tool_name":""}}`,
		`{"conversation_id":"00000000-0000-0000-0000-000000000001","event":"step_update","step_update":{"conversation_id":"00000000-0000-0000-0000-000000000001","duration_seconds":1.425,"state":"DONE","step_index":4,"step_type":"agent_response","text_delta":"","thinking":"","thinking_delta":"","tool_name":"","usage":{"cache_read_tokens":10903,"input_tokens":349,"output_tokens":9,"thinking_tokens":0,"total_tokens":11261}}}`,
		`{"conversation_id":"00000000-0000-0000-0000-000000000001","event":"result","result":{"conversation_id":"00000000-0000-0000-0000-000000000001","duration_seconds":6.68,"error":"","num_turns":2,"response":"PONG_1","status":"DONE","usage":{"cache_read_tokens":10903,"input_tokens":11252,"output_tokens":153,"thinking_tokens":45,"total_tokens":22308}}}`,
	}

	var allEvents []SSEEvent
	for _, line := range fixtures {
		allEvents = append(allEvents, tr.FeedLine(line)...)
	}

	if got := tr.ExternalID(); got != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("expected external conversation ID extracted, got %q", got)
	}
	if !tr.IsTurnFinished() {
		t.Fatalf("expected turn finished after result event")
	}

	var toolStatuses []string
	hasReasoning := false
	hasTextDelta := false
	hasIdle := false

	for _, evt := range allEvents {
		switch evt.Type {
		case "message.part.updated":
			p := evt.Properties["part"].(MessagePart)
			if p.Type == "reasoning" {
				hasReasoning = true
			}
			if p.Type == "tool" && p.State != nil {
				toolStatuses = append(toolStatuses, p.State.Status)
				if p.State.Status == "completed" {
					if p.State.Time == nil || p.State.Time.End <= p.State.Time.Start {
						t.Fatalf("expected positive duration on completed tool part: %+v", p.State.Time)
					}
				}
			}
		case "message.part.delta":
			if evt.Properties["delta"] == "PONG_1" {
				hasTextDelta = true
			}
		case "session.idle":
			hasIdle = true
		}
	}

	if !hasReasoning {
		t.Fatalf("expected reasoning part created from thinking lookup")
	}
	if !hasTextDelta {
		t.Fatalf("expected text delta PONG_1")
	}
	if !hasIdle {
		t.Fatalf("expected session.idle event")
	}
	wantStatuses := "pending,running,completed"
	if got := strings.Join(toolStatuses, ","); got != wantStatuses {
		t.Fatalf("expected tool status sequence %q, got %q", wantStatuses, got)
	}
}

func TestAgyEditStepsRecoverPayloadFromTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GEMINI_HOME", home)
	const conv = "11111111-2222-3333-4444-555555555555"
	logDir := agyTranscriptLogDir(filepath.Join(home, "antigravity-cli", "brain"), conv)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// One planner turn queues a write and two edits; agy runs them later as
	// separate stdout steps that only repeat the path. Arguments are stored
	// JSON-encoded, the way transcript.jsonl writes them.
	row, err := json.Marshal(map[string]interface{}{
		"step_index": 7,
		"type":       "PLANNER_RESPONSE",
		"tool_calls": []map[string]interface{}{
			{"name": "write_file", "args": map[string]string{
				"Path":    `"/repo/new.txt"`,
				"Content": `"package new\n"`,
			}},
			{"name": "edit_file", "args": map[string]string{
				"Path":        `"/repo/a.go"`,
				"Target":      `"const a = 1\n"`,
				"Replacement": `"const a = 2\n"`,
			}},
			{"name": "edit_file", "args": map[string]string{
				"Path":        `"/repo/b.go"`,
				"Target":      `"old\n"`,
				"Replacement": `"new\n"`,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), append(row, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	tr := NewTurnTranslator("ses_agy_edit", string(RuntimeAntigravity), "msg_a_edit", "msg_u_edit", "antigravity", "gemini", "build", "")
	step := func(idx int, path string) string {
		return fmt.Sprintf(`{"conversation_id":"%s","event":"step_update","step_update":{"conversation_id":"%s","state":"DONE","step_index":%d,"step_type":"tool","tool_name":"edit_file","tool_info":{"name":"edit_file","parameters":{"Path":"%s"}}}}`, conv, conv, idx, path)
	}
	var evts []SSEEvent
	for _, idx := range []int{40, 41, 42} {
		name := []string{"new.txt", "a.go", "b.go"}[idx-40]
		evts = append(evts, tr.FeedLine(step(idx, "/repo/"+name))...)
	}

	var got []MessagePart
	for _, evt := range evts {
		if evt.Type != "message.part.updated" {
			continue
		}
		if p := evt.Properties["part"].(MessagePart); p.Type == "tool" && p.State != nil && p.State.Status == "completed" {
			got = append(got, p)
		}
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 completed edit steps, got %d", len(got))
	}
	if got[0].Tool != "write" || got[0].State.Input["Content"] != "package new\n" || got[0].State.Input["filePath"] != "/repo/new.txt" {
		t.Fatalf("write payload not recovered: tool=%s input=%v", got[0].Tool, got[0].State.Input)
	}
	if got[1].Tool != "edit" || got[1].State.Input["Target"] != "const a = 1\n" || got[1].State.Input["Replacement"] != "const a = 2\n" {
		t.Fatalf("first edit paired with the wrong payload: %+v", got[1].State.Input)
	}
	if got[2].State.Input["Target"] != "old\n" || got[2].State.Input["filePath"] != "/repo/b.go" {
		t.Fatalf("second edit paired with the wrong payload: %+v", got[2].State.Input)
	}
}

func TestAgyEditPayloadNeverOverridesStdout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GEMINI_HOME", home)
	const conv = "22222222-3333-4444-5555-666666666666"
	logDir := agyTranscriptLogDir(filepath.Join(home, "antigravity-cli", "brain"), conv)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	row, err := json.Marshal(map[string]interface{}{
		"step_index": 3,
		"type":       "PLANNER_RESPONSE",
		"tool_calls": []map[string]interface{}{
			{"name": "replace_file_content", "args": map[string]string{
				"TargetFile":         `"/repo/planned.go"`,
				"TargetContent":      `"planned"`,
				"ReplacementContent": `"planned-new"`,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), append(row, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	tr := NewTurnTranslator("ses_agy_keep", string(RuntimeAntigravity), "msg_a_keep", "msg_u_keep", "antigravity", "gemini", "build", "")
	line := fmt.Sprintf(`{"conversation_id":"%s","event":"step_update","step_update":{"conversation_id":"%s","state":"DONE","step_index":9,"step_type":"tool","tool_name":"replace_file_content","tool_info":{"name":"replace_file_content","parameters":{"TargetFile":"/repo/actual.go","TargetContent":"actual","ReplacementContent":"actual-new"}}}}`, conv, conv)
	evts := tr.FeedLine(line)
	for _, evt := range evts {
		if evt.Type != "message.part.updated" {
			continue
		}
		p := evt.Properties["part"].(MessagePart)
		if p.Type == "tool" && p.State != nil && p.State.Status == "completed" {
			if p.State.Input["filePath"] != "/repo/actual.go" || p.State.Input["TargetContent"] != "actual" {
				t.Fatalf("stdout payload was overwritten by the transcript: %+v", p.State.Input)
			}
			return
		}
	}
	t.Fatal("completed tool part not found")
}

func TestAgyDeniedToolAndInterruptTranslation(t *testing.T) {
	t.Parallel()
	tr := NewTurnTranslator("ses_agy_2", string(RuntimeAntigravity), "msg_a_2", "msg_u_2", "antigravity", "claude-sonnet-4-6", "build", "")
	lines := []string{
		`{"conversation_id":"c2","event":"step_update","step_update":{"conversation_id":"c2","duration_seconds":0,"state":"ERROR","step_index":2,"step_type":"tool","tool_info":{"error":{"message":"user denied permission to run command","type":"CommandExecutionError"},"name":"run_command","output":"","parameters":{"CommandLine":"rm -f probe.txt"}},"tool_name":"run_command"}}`,
		`{"conversation_id":"c2","event":"result","result":{"conversation_id":"c2","denied_actions":[{"action":"run_command","display_name":"rm -f probe.txt"}],"duration_seconds":3.96,"error":"","num_turns":1,"response":"","status":"DONE"}}`,
	}
	var evts []SSEEvent
	for _, l := range lines {
		evts = append(evts, tr.FeedLine(l)...)
	}
	sawToolError := false
	sawDeniedNotice := false
	for _, evt := range evts {
		if evt.Type == "message.part.updated" {
			p := evt.Properties["part"].(MessagePart)
			if p.Type == "tool" && p.State != nil && p.State.Status == "error" && strings.Contains(p.State.Error, "denied") {
				sawToolError = true
			}
			if p.Type == "text" && strings.Contains(p.Text, "rm -f probe.txt") {
				sawDeniedNotice = true
			}
		}
	}
	if !sawToolError || !sawDeniedNotice {
		t.Fatalf("expected tool error (%v) and denied notice (%v)", sawToolError, sawDeniedNotice)
	}

	// Test interrupted result maps to aborted without session.error
	trAbort := NewTurnTranslator("ses_agy_3", string(RuntimeAntigravity), "msg_a_3", "msg_u_3", "antigravity", "claude-sonnet-4-6", "build", "")
	abortEvts := trAbort.FeedLine(`{"conversation_id":"c3","event":"result","result":{"conversation_id":"c3","error":"interrupted","status":"ERROR"}}`)
	for _, evt := range abortEvts {
		if evt.Type == "session.error" {
			t.Fatalf("interrupted turn must not emit session.error")
		}
	}
	if trAbort.msgInfo.Finish != "aborted" {
		t.Fatalf("expected finish=aborted, got %q", trAbort.msgInfo.Finish)
	}
}

func TestBuildAgyArgsAndWorktreeValidation(t *testing.T) {
	t.Parallel()
	args := BuildAgyArgs("build", "claude-sonnet-4-6", "high", "conv-123")
	joined := strings.Join(args, " ")
	for _, must := range []string{
		"--input-format stream-json",
		"--output-format stream-json",
		"--sandbox",
		"--mode accept-edits",
		"--model claude-sonnet-4-6",
		"--effort high",
		"--conversation conv-123",
	} {
		if !strings.Contains(joined, must) {
			t.Fatalf("BuildAgyArgs missing %q in %v", must, args)
		}
	}
	if args[len(args)-1] != "-p=" {
		t.Fatalf("BuildAgyArgs must end with -p= to prevent flag swallowing, got %q", args[len(args)-1])
	}

	planArgs := BuildAgyArgs("plan", "", "", "")
	if !strings.Contains(strings.Join(planArgs, " "), "--mode plan") {
		t.Fatalf("expected --mode plan for plan agent, got %v", planArgs)
	}

	// Verify agy effort-suffixed model slugs never conflict with --effort
	conflictArgs := BuildAgyArgs("build", "gemini-4-argon-low", "high", "")
	conflictJoined := strings.Join(conflictArgs, " ")
	if !strings.Contains(conflictJoined, "--model gemini-4-argon-high") || !strings.Contains(conflictJoined, "--effort high") {
		t.Fatalf("expected gemini-4-argon-low + high effort to resolve to --model gemini-4-argon-high --effort high, got: %v", conflictArgs)
	}
	ossArgs := BuildAgyArgs("build", "gpt-oss-120b-medium", "high", "")
	ossJoined := strings.Join(ossArgs, " ")
	if !strings.Contains(ossJoined, "--model gpt-oss-120b-medium") || !strings.Contains(ossJoined, "--effort medium") {
		t.Fatalf("expected gpt-oss-120b-medium + high effort to clamp to medium, got: %v", ossArgs)
	}
	autoAgyArgs := BuildAgyArgs("build", "gemini-4-argon-low", "", "")
	if strings.Contains(strings.Join(autoAgyArgs, " "), "--effort") {
		t.Fatalf("expected empty effort on suffixed agy slug to omit redundant --effort, got: %v", autoAgyArgs)
	}

	// Verify Claude vendor-level effort capability filtering
	haikuArgs := BuildClaudeArgs("build", "claude-haiku-4-5-20251001", "high", "")
	if strings.Contains(strings.Join(haikuArgs, " "), "--effort") {
		t.Fatalf("expected claude-haiku-4-5 to omit unsupported --effort, got: %v", haikuArgs)
	}
	sonnetArgs := BuildClaudeArgs("build", "claude-sonnet-4-6", "xhigh", "")
	if !strings.Contains(strings.Join(sonnetArgs, " "), "--effort xhigh") {
		t.Fatalf("expected claude-sonnet-4-6 to pass vendor-supported --effort xhigh, got: %v", sonnetArgs)
	}
	opus55Args := BuildClaudeArgs("build", "claude-opus-5-5", "max", "")
	if !strings.Contains(strings.Join(opus55Args, " "), "--effort max") {
		t.Fatalf("expected claude-opus-5-5 to pass vendor-supported --effort max, got: %v", opus55Args)
	}

	// Worktree validation: must reject repo root and home dir, accept .hoxi/worktrees/<id>
	repoRoot := t.TempDir()
	wtDir := filepath.Join(repoRoot, ".hoxi", "worktrees", "ses_agy_ok")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorktreeDir(wtDir, repoRoot); err != nil {
		t.Fatalf("expected valid worktree dir to pass, got: %v", err)
	}
	if err := ValidateWorktreeDir(repoRoot, repoRoot); err == nil {
		t.Fatalf("expected main repo root to be rejected by ValidateWorktreeDir")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if err := ValidateWorktreeDir(home, repoRoot); err == nil {
			t.Fatalf("expected user home dir to be rejected by ValidateWorktreeDir")
		}
	}
}

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	run("init")
	run("config", "user.email", "test@hoxi.ai")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial\n\nCo-authored-by: Hoxi <code@hoxi.ai>")
	return dir
}

func TestRunnerSessionMissingBinaryAndSubprocessLifecycle(t *testing.T) {
	repoDir := initTestGitRepo(t)
	storePath := filepath.Join(t.TempDir(), "runner-sessions.json")
	tracker := statusbar.NewTracker()
	wtMgr := worktree.NewManager(repoDir)
	svc := NewService(repoDir, wtMgr, tracker, storePath)
	defer svc.CloseAll()

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// 1. Create runner session -> automatically provisions .hoxi/worktrees/<session-id>
	createBody, _ := json.Marshal(map[string]string{
		"runtime":     "antigravity",
		"title":       "Runner Test",
		"modelID":     "claude-sonnet-4-6",
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
	if !strings.Contains(filepath.ToSlash(created.Directory), "/.hoxi/worktrees/"+created.ID) {
		t.Fatalf("expected isolated worktree directory, got %q", created.Directory)
	}

	// 2. Missing agy binary -> returns clear HTTP 400 error
	emptyBin := t.TempDir()
	t.Setenv("GEMINI_HOME", emptyBin)
	t.Setenv("PATH", emptyBin)
	promptReqBody := `{"text":"hello"}`
	recMissing := httptest.NewRecorder()
	mux.ServeHTTP(recMissing, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+created.ID+"/prompt", strings.NewReader(promptReqBody)))
	if recMissing.Code != http.StatusBadRequest || !strings.Contains(recMissing.Body.String(), "binary not found") {
		t.Fatalf("expected 400 missing binary error, got %d: %s", recMissing.Code, recMissing.Body.String())
	}

	// 3. Synthetic agy binary -> verifies --sandbox, cmd.Dir == worktree, conversation resume
	agyDir := filepath.Join(emptyBin, "bin")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	argsLog := filepath.Join(t.TempDir(), "agy_args.txt")
	cwdLog := filepath.Join(t.TempDir(), "agy_cwd.txt")
	t.Setenv("TEST_ARGS_LOG", argsLog)
	t.Setenv("TEST_CWD_LOG", cwdLog)
	script := `#!/bin/sh
printf '%s\n' "$@" > "$TEST_ARGS_LOG"
pwd > "$TEST_CWD_LOG"
cat >/dev/null
printf '%s\n' '{"conversation_id":"conv-synth-99","event":"init","init":{"model":"claude-sonnet-4-6","permission_mode":"accept-edits"}}'
printf '%s\n' '{"conversation_id":"conv-synth-99","event":"step_update","step_update":{"conversation_id":"conv-synth-99","state":"ACTIVE","step_index":1,"step_type":"agent_response","text_delta":"SYNTH_OK"}}'
printf '%s\n' '{"conversation_id":"conv-synth-99","event":"result","result":{"conversation_id":"conv-synth-99","response":"SYNTH_OK","status":"DONE"}}'
`
	execstub.Write(t, agyDir, "agy", script)

	ch, unsub := svc.Subscribe(created.ID)
	defer unsub()

	recPrompt := httptest.NewRecorder()
	mux.ServeHTTP(recPrompt, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+created.ID+"/prompt", strings.NewReader(promptReqBody)))
	if recPrompt.Code != http.StatusOK {
		t.Fatalf("prompt failed (%d): %s", recPrompt.Code, recPrompt.Body.String())
	}

	deadline := time.After(5 * time.Second)
	sawIdle := false
	for !sawIdle {
		select {
		case evt := <-ch:
			if evt.Type == "session.idle" {
				sawIdle = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for session.idle from synthetic agy")
		}
	}

	loggedArgs, _ := os.ReadFile(argsLog)
	if !strings.Contains(string(loggedArgs), "--sandbox") {
		t.Fatalf("expected --sandbox in process args, got: %s", string(loggedArgs))
	}
	loggedCWD, _ := os.ReadFile(cwdLog)
	evalWant, _ := filepath.EvalSymlinks(created.Directory)
	evalGot, _ := filepath.EvalSymlinks(strings.TrimSpace(string(loggedCWD)))
	if evalGot != evalWant {
		t.Fatalf("expected process CWD %q, got %q", evalWant, evalGot)
	}

	// Wait briefly for store flush and verify ExternalID persisted for --conversation resume
	time.Sleep(30 * time.Millisecond)
	recStored, ok := svc.store.Get(created.ID)
	if !ok || recStored.ExternalID != "conv-synth-99" {
		t.Fatalf("expected ExternalID conv-synth-99 persisted, got %+v", recStored)
	}
}

func TestRunnerModelsEndpointsIncludeInstalledAndModels(t *testing.T) {
	binDir := t.TempDir()
	agyBinDir := filepath.Join(binDir, "bin")
	if err := os.MkdirAll(agyBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GEMINI_HOME", binDir)
	t.Setenv("PATH", agyBinDir)
	t.Setenv("CODEBUDDY_BIN", filepath.Join(agyBinDir, "codebuddy"))
	t.Setenv("QODER_CN_BIN", filepath.Join(agyBinDir, "qoderclicn"))
	t.Setenv("QODER_BIN", "")
	agyScript := "#!/bin/sh\nprintf 'gemini-4-argon-thinking   Gemini 4 Argon (High)\\nclaude-sonnet-5-5-thinking Claude Sonnet 5.5 (Thinking)\\n'\n"
	execstub.Write(t, agyBinDir, "agy", agyScript)
	execstub.Write(t, agyBinDir, "claude", "exit 0\n")
	cbScript := "#!/bin/sh\nprintf '  --model <model>  Currently supported: (default-model, hy4-preview, deepseek-v4.1-flash, gpt-5.5)\\n'\n"
	execstub.Write(t, agyBinDir, "codebuddy", cbScript)
	qoderScript := "#!/bin/sh\nprintf 'auto\\nqwen3-coder-plus\\ndeepseek-v3\\n'\n"
	execstub.Write(t, agyBinDir, "qoderclicn", qoderScript)

	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "store.json"))
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	for _, ep := range []string{
		"/api/hoxi/runner/antigravity/models?refresh=1",
		"/api/hoxi/runner/claude-code/models",
		"/api/hoxi/runner/codebuddy/models?refresh=1",
		"/api/hoxi/runner/qoder/models?refresh=1",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ep, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", ep, rec.Code, rec.Body.String())
		}
		var payload struct {
			Installed bool                 `json:"installed"`
			Available bool                 `json:"available"`
			Default   string               `json:"default"`
			Models    []agentcli.ModelItem `json:"models"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s invalid JSON: %v", ep, err)
		}
		if !payload.Installed || !payload.Available || payload.Default == "" || len(payload.Models) == 0 {
			t.Fatalf("%s expected installed=true, default!='', models>0, got: %+v", ep, payload)
		}
	}
}

func TestCodeBuddyAndQoderArgs(t *testing.T) {
	cbArgs := BuildCodeBuddyArgs("plan", "hy4-preview", "high", "ses-cb-1")
	cbJoined := strings.Join(cbArgs, " ")
	if !strings.Contains(cbJoined, "--permission-mode plan") || !strings.Contains(cbJoined, "--model hy4-preview") || !strings.Contains(cbJoined, "--resume ses-cb-1") {
		t.Fatalf("unexpected CodeBuddy args: %v", cbArgs)
	}

	t.Setenv("QODER_BIN", "")
	t.Setenv("QODER_CN_BIN", "")
	qArgs := BuildQoderArgs("build", "cn:qwen3-coder-plus", "high", "ses-q-1")
	qJoined := strings.Join(qArgs, " ")
	if !strings.Contains(qJoined, "--permission-mode accept_edits") || !strings.Contains(qJoined, "--model qwen3-coder-plus") || !strings.Contains(qJoined, "--reasoning-effort high") {
		t.Fatalf("unexpected Qoder args: %v", qArgs)
	}
}

func TestImageOnlyPromptAndAttachmentEncoding(t *testing.T) {
	t.Parallel()
	const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	wtDir := t.TempDir()
	parts := []MessagePart{
		{
			Type:     "file",
			Mime:     "image/png",
			Filename: "screenshot.png",
			URL:      tinyPNG,
		},
	}

	saved := materializeTurnAttachments(wtDir, 1700000000000, 1, parts)
	if len(saved) != 1 {
		t.Fatalf("expected 1 materialized attachment, got %d", len(saved))
	}
	if _, err := os.Stat(saved[0]); err != nil {
		t.Fatalf("materialized file missing: %v", err)
	}

	claudeBytes, err := EncodeStreamInputTurnWithParts(string(RuntimeClaudeCode), "Analyze image", parts)
	if err != nil {
		t.Fatalf("EncodeStreamInputTurnWithParts claude error: %v", err)
	}
	if !bytes.Contains(claudeBytes, []byte(`"type":"image"`)) || !bytes.Contains(claudeBytes, []byte(`"media_type":"image/png"`)) {
		t.Fatalf("expected Claude stream-json input to contain base64 image block, got: %s", string(claudeBytes))
	}

	agyBytes, err := EncodeStreamInputTurnWithParts(string(RuntimeAntigravity), "Analyze image", parts)
	if err != nil {
		t.Fatalf("EncodeStreamInputTurnWithParts agy error: %v", err)
	}
	if bytes.Contains(agyBytes, []byte(`"type":"image"`)) || !bytes.Contains(agyBytes, []byte(`"content":"Analyze image"`)) {
		t.Fatalf("expected Antigravity stream-json input to use string content, got: %s", string(agyBytes))
	}
}

func TestRuntimeCapabilitiesMatrix(t *testing.T) {
	t.Parallel()
	// 1. Subagent: all capabilities false
	subCaps := RuntimeCapabilities("opencode", true)
	if subCaps.Approval || subCaps.Steer || subCaps.Resume || subCaps.Revert || subCaps.Fork || subCaps.Attachments || subCaps.CostStats {
		t.Fatalf("expected subagent to have zero mutable capabilities, got %+v", subCaps)
	}

	// 2. OpenCode: full capabilities
	ocCaps := RuntimeCapabilities("opencode", false)
	if !ocCaps.Approval || !ocCaps.Steer || !ocCaps.Resume || !ocCaps.Revert || !ocCaps.Fork || !ocCaps.Attachments || !ocCaps.CostStats {
		t.Fatalf("expected opencode to support full capability matrix, got %+v", ocCaps)
	}

	// 3. Claude Code: approval supported, steer false
	cldCaps := RuntimeCapabilities(string(RuntimeClaudeCode), false)
	if !cldCaps.Approval || cldCaps.Steer || !cldCaps.Resume || !cldCaps.Revert || !cldCaps.Fork || !cldCaps.Attachments || !cldCaps.CostStats {
		t.Fatalf("expected claude-code capabilities mismatch, got %+v", cldCaps)
	}

	// 4. Antigravity: approval supported, steer false
	agyCaps := RuntimeCapabilities(string(RuntimeAntigravity), false)
	if !agyCaps.Approval || agyCaps.Steer || !agyCaps.Resume || !agyCaps.Revert || !agyCaps.Fork || !agyCaps.Attachments || !agyCaps.CostStats {
		t.Fatalf("expected antigravity capabilities mismatch, got %+v", agyCaps)
	}

	// 5. CodeBuddy & Qoder: approval false (no PreToolUse hook gate)
	cbCaps := RuntimeCapabilities(string(RuntimeCodeBuddy), false)
	if cbCaps.Approval || cbCaps.Steer || !cbCaps.Resume || !cbCaps.Revert || !cbCaps.Fork || cbCaps.Attachments || !cbCaps.CostStats {
		t.Fatalf("expected codebuddy capabilities mismatch, got %+v", cbCaps)
	}

	qdCaps := RuntimeCapabilities(string(RuntimeQoder), false)
	if qdCaps.Approval || qdCaps.Steer || !qdCaps.Resume || !qdCaps.Revert || !qdCaps.Fork || qdCaps.Attachments || !qdCaps.CostStats {
		t.Fatalf("expected qoder capabilities mismatch, got %+v", qdCaps)
	}
}

func TestPromptReceiptAndClientRequestIdDeduplication(t *testing.T) {
	repoDir := initTestGitRepo(t)
	storePath := filepath.Join(t.TempDir(), "runner-sessions.json")
	svc := NewService(repoDir, worktree.NewManager(repoDir), statusbar.NewTracker(), storePath)
	defer svc.CloseAll()

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	binRoot := t.TempDir()
	agyDir := filepath.Join(binRoot, "bin")
	if err := os.MkdirAll(agyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	countFile := filepath.Join(t.TempDir(), "invocations.log")
	t.Setenv("GEMINI_HOME", binRoot)
	t.Setenv("PATH", agyDir)
	t.Setenv("TEST_COUNT_FILE", countFile)
	script := `#!/bin/sh
printf '1\n' >> "$TEST_COUNT_FILE"
cat >/dev/null
printf '%s\n' '{"conversation_id":"conv-idemp-1","event":"init","init":{"model":"claude-sonnet-4-6","permission_mode":"accept-edits"}}'
printf '%s\n' '{"conversation_id":"conv-idemp-1","event":"result","result":{"conversation_id":"conv-idemp-1","response":"OK","status":"DONE"}}'
`
	execstub.Write(t, agyDir, "agy", script)

	createBody, _ := json.Marshal(map[string]string{
		"runtime":     "antigravity",
		"title":       "Idempotency Test",
		"modelID":     "claude-sonnet-4-6",
		"directory":   repoDir,
		"sandboxMode": "local",
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

	ch, unsub := svc.Subscribe(created.ID)
	defer unsub()

	sendPrompt := func(body string, headerKey string) (int, PromptReceipt) {
		req := httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+created.ID+"/prompt_async", strings.NewReader(body))
		if headerKey != "" {
			req.Header.Set("X-Idempotency-Key", headerKey)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var receipt PromptReceipt
		_ = json.Unmarshal(rec.Body.Bytes(), &receipt)
		return rec.Code, receipt
	}

	code1, r1 := sendPrompt(`{"text":"first command","clientRequestId":"req-101"}`, "")
	if code1 != http.StatusOK || !r1.OK || r1.InstructionID != 1 || !strings.HasPrefix(r1.MessageID, "msg_u_") || r1.ClientRequestID != "req-101" || r1.Deduplicated {
		t.Fatalf("unexpected first receipt (%d): %+v", code1, r1)
	}

	// Retry with same clientRequestId in body (simulating client timeout/retry) -> deduplicated receipt, no 409 or second process
	codeDup1, rDup1 := sendPrompt(`{"text":"first command","clientRequestId":"req-101"}`, "")
	if codeDup1 != http.StatusOK || !rDup1.OK || !rDup1.Deduplicated || rDup1.InstructionID != r1.InstructionID || rDup1.MessageID != r1.MessageID {
		t.Fatalf("expected deduplicated receipt matching first turn, got (%d): %+v", codeDup1, rDup1)
	}

	// Retry via X-Idempotency-Key header -> also deduplicated
	codeDup2, rDup2 := sendPrompt(`{"text":"first command"}`, "req-101")
	if codeDup2 != http.StatusOK || !rDup2.Deduplicated || rDup2.InstructionID != r1.InstructionID {
		t.Fatalf("expected header-based deduplication, got (%d): %+v", codeDup2, rDup2)
	}

	waitForEvent(t, ch, "session.idle")
	for deadline := time.Now().Add(2 * time.Second); len(svc.busySessionIDs()) > 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}

	// New clientRequestId -> executes second turn with instructionId = 2
	code2, r2 := sendPrompt(`{"text":"second command","clientRequestId":"req-102"}`, "")
	if code2 != http.StatusOK || !r2.OK || r2.InstructionID != 2 || r2.MessageID == r1.MessageID || r2.Deduplicated {
		t.Fatalf("unexpected second receipt (%d): %+v", code2, r2)
	}
	waitForEvent(t, ch, "session.idle")

	invocations, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(invocations)), "\n"); len(lines) != 2 {
		t.Fatalf("expected exactly 2 CLI invocations after 3 requests (1 deduplicated), got %d", len(lines))
	}
}

func TestPermissionReplyClientRequestIdIdempotency(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()

	body, _ := json.Marshal(map[string]interface{}{
		"conversationId": "conv-1",
		"stepIdx":        9,
		"toolCall": map[string]interface{}{
			"name": "mystery_tool_idemp",
			"args": map[string]interface{}{"Target": "workspace"},
		},
	})
	result := f.asyncHook(t, body)
	asked := waitForEvent(t, events, "permission.asked")
	id, _ := asked.Properties["id"].(string)
	if id == "" {
		t.Fatal("missing permission id")
	}

	postReply := func(payload map[string]string) int {
		raw, _ := json.Marshal(payload)
		resp, err := http.Post(f.srv.URL+"/api/hoxi/permission/"+id+"/reply", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := postReply(map[string]string{"reply": "once", "clientRequestId": "perm-req-1"}); code != http.StatusOK {
		t.Fatalf("first permission reply failed: %d", code)
	}
	if d := <-result; d.Decision != "auto_approve" {
		t.Fatalf("expected auto_approve after once reply, got %q", d.Decision)
	}

	// Same clientRequestId + same decision retried after network blip -> 200 OK
	if code := postReply(map[string]string{"reply": "once", "clientRequestId": "perm-req-1"}); code != http.StatusOK {
		t.Fatalf("idempotent retry with same clientRequestId should return 200, got %d", code)
	}

	// Same clientRequestId with different decision -> 404 (not treated as idempotent match)
	if code := postReply(map[string]string{"reply": "reject", "clientRequestId": "perm-req-1"}); code != http.StatusNotFound {
		t.Fatalf("conflicting decision with same clientRequestId should return 404, got %d", code)
	}

	// Duplicate reply without clientRequestId -> 404
	if code := postReply(map[string]string{"reply": "once"}); code != http.StatusNotFound {
		t.Fatalf("duplicate reply without clientRequestId should return 404, got %d", code)
	}
}
