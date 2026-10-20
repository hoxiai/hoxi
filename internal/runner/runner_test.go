package runner

import (
	"bytes"
	"encoding/json"
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
	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestAgyEventTranslationWithPhase0Fixtures(t *testing.T) {
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

func TestAgyDeniedToolAndInterruptTranslation(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(agyDir, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

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
	agyScript := "#!/bin/sh\nprintf 'gemini-4-argon-thinking   Gemini 4 Argon (High)\\nclaude-sonnet-5-5-thinking Claude Sonnet 5.5 (Thinking)\\n'\n"
	if err := os.WriteFile(filepath.Join(agyBinDir, "agy"), []byte(agyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agyBinDir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cbScript := "#!/bin/sh\nprintf '  --model <model>  Currently supported: (default-model, hy4-preview, deepseek-v4.1-flash, gpt-5.5)\\n'\n"
	if err := os.WriteFile(filepath.Join(agyBinDir, "codebuddy"), []byte(cbScript), 0o755); err != nil {
		t.Fatal(err)
	}
	qoderScript := "#!/bin/sh\nprintf 'auto\\nqwen3-coder-plus\\ndeepseek-v3\\n'\n"
	if err := os.WriteFile(filepath.Join(agyBinDir, "qoderclicn"), []byte(qoderScript), 0o755); err != nil {
		t.Fatal(err)
	}

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
