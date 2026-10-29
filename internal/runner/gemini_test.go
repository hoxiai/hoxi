package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agyhook"
	"github.com/hoxiai/hoxi/internal/sandbox"
	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/testutil/execstub"
)

func TestIsAgyBinaryRecognizesWindowsLaunchers(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/usr/local/bin/agy", true},
		{`C:\Program Files\Gemini\agy.exe`, true},
		{`C:\Users\x\agy.CMD`, true},
		{`C:\Users\x\agy.bat`, true},
		{`C:\Users\x\gemini.exe`, false},
		{`C:\Users\x\agy.exe.cmd`, false},
		{"", false},
	} {
		if got := isAgyBinary(tc.path); got != tc.want {
			t.Errorf("isAgyBinary(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestResolveGeminiTurnBinaryUsesAgyProtocolForExplicitWindowsBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agy.exe")
	if err := os.WriteFile(path, []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GEMINI_BIN", path)
	got, useAgy := ResolveGeminiTurnBinary("auto")
	if got != path || !useAgy {
		t.Fatalf("ResolveGeminiTurnBinary = %q, %v; want %q, true", got, useAgy, path)
	}
}

func TestBuildGeminiArgs(t *testing.T) {
	gotBuild := BuildGeminiArgs("build", "gemini-2.5-pro", "high", "ses-ext-1")
	wantBuild := []string{
		"--output-format", "stream-json",
		"--approval-mode", "yolo",
		"--model", "gemini-2.5-pro",
		"--resume", "ses-ext-1",
		"-p", "",
	}
	if !reflect.DeepEqual(gotBuild, wantBuild) {
		t.Fatalf("BuildGeminiArgs(build) = %v, want %v", gotBuild, wantBuild)
	}

	gotPlan := BuildGeminiArgs("plan", "auto", "", "")
	wantPlan := []string{
		"--output-format", "stream-json",
		"--approval-mode", "plan",
		"-p", "",
	}
	if !reflect.DeepEqual(gotPlan, wantPlan) {
		t.Fatalf("BuildGeminiArgs(plan) = %v, want %v", gotPlan, wantPlan)
	}
}

func TestGeminiStreamJSONTranslation(t *testing.T) {
	tr := NewTurnTranslator("ses_gem_test", string(RuntimeGeminiCLI), "msg_a_1", "msg_u_1", "gemini-cli", "gemini-2.5-pro", "build", "")

	lines := []string{
		`{"type":"init","timestamp":"2026-10-24T00:00:00Z","session_id":"gem-sess-99","model":"gemini-2.5-pro"}`,
		`{"type":"message","timestamp":"2026-10-24T00:00:01Z","role":"user","content":"fix bug"}`,
		`{"type":"message","timestamp":"2026-10-24T00:00:02Z","role":"assistant","thought":"Checking files...","delta":true}`,
		`{"type":"tool_use","timestamp":"2026-10-24T00:00:03Z","tool_name":"run_shell_command","tool_id":"call_1","parameters":{"command":"go test ./...","description":"Run Go tests"}}`,
		`{"type":"tool_result","timestamp":"2026-10-24T00:00:04Z","tool_id":"call_1","status":"success","output":"ok  github.com/hoxiai/hoxi\n"}`,
		`{"type":"tool_use","timestamp":"2026-10-24T00:00:05Z","tool_name":"replace","tool_id":"call_2","parameters":{"file_path":"/repo/main.go","old_string":"a","new_string":"b"}}`,
		`{"type":"tool_result","timestamp":"2026-10-24T00:00:06Z","tool_id":"call_2","status":"success","output":"Updated /repo/main.go"}`,
		`{"type":"message","timestamp":"2026-10-24T00:00:07Z","role":"assistant","content":"Fixed and verified.","delta":true}`,
		`{"type":"result","timestamp":"2026-10-24T00:00:08Z","status":"success","stats":{"total_tokens":1500,"input_tokens":1200,"output_tokens":300,"cached":400,"models":{"gemini-2.5-pro":{"tokens":{"prompt":1200,"candidates":250,"thoughts":50,"cached":400}}}}}`,
	}

	for _, line := range lines {
		_ = tr.FeedLine(line)
	}

	if !tr.IsTurnFinished() {
		t.Fatal("expected IsTurnFinished() == true after result event")
	}
	if tr.ExternalID() != "gem-sess-99" {
		t.Fatalf("ExternalID() = %q, want gem-sess-99", tr.ExternalID())
	}

	if tr.msgInfo.Tokens == nil {
		t.Fatal("expected token usage on tr.msgInfo")
	}
	if tr.msgInfo.Tokens.Input != 1200 || tr.msgInfo.Tokens.Output != 300 || tr.msgInfo.Tokens.Reasoning != 50 || tr.msgInfo.Tokens.Cache.Read != 400 {
		t.Fatalf("unexpected tokens: %+v", tr.msgInfo.Tokens)
	}

	var sawBash, sawEdit, sawText, sawReasoning bool
	for _, p := range tr.partsByID {
		switch p.Type {
		case "tool":
			if p.Tool == "bash" && p.State != nil && p.State.Status == "completed" && strings.Contains(p.State.Output, "ok  github.com/hoxiai/hoxi") {
				sawBash = true
			}
			if p.Tool == "edit" && p.State != nil && p.State.Status == "completed" && p.State.Input["filePath"] == "/repo/main.go" {
				sawEdit = true
			}
		case "text":
			if p.Text == "Fixed and verified." {
				sawText = true
			}
		case "reasoning":
			if p.Text == "Checking files..." {
				sawReasoning = true
			}
		}
	}
	if !sawBash || !sawEdit || !sawText || !sawReasoning {
		t.Fatalf("missing translated parts: bash=%v edit=%v text=%v reasoning=%v (parts=%+v)", sawBash, sawEdit, sawText, sawReasoning, tr.partsByID)
	}
}

func TestGeminiHookBeforeToolGate(t *testing.T) {
	dir := t.TempDir()
	tracker := statusbar.NewTracker()
	svc := NewService(dir, nil, tracker, filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeSmart})
	if err := svc.store.Put(SessionRecord{
		ID:           "ses_gem_gate",
		Runtime:      string(RuntimeGeminiCLI),
		ProjectDir:   dir,
		WorktreePath: dir,
		Mode:         "build",
	}); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	svc.SetHookBaseURL(srv.URL)

	envList, revoke := svc.issueGeminiHook("build", "ses_gem_gate", "msg_a_1")
	t.Cleanup(revoke)
	env := map[string]string{}
	for _, kv := range envList {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	homeDir := env[geminiCLIHomeEnv]
	if homeDir == "" {
		t.Fatal("expected non-empty GEMINI_CLI_HOME path")
	}
	if env[geminiTrustWorkspaceEnv] != "true" {
		t.Fatalf("expected %s=true, got %q", geminiTrustWorkspaceEnv, env[geminiTrustWorkspaceEnv])
	}
	settingsPath := filepath.Join(homeDir, ".gemini", "settings.json")
	rawSettings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", settingsPath, err)
	}
	if !strings.Contains(string(rawSettings), "BeforeTool") {
		t.Fatalf("expected BeforeTool hook in %s, got: %s", settingsPath, string(rawSettings))
	}
	if strings.Contains(string(rawSettings), "SessionStart") || strings.Contains(string(rawSettings), "SessionEnd") {
		t.Fatalf("expected third-party SessionStart/SessionEnd hooks to be excluded from %s, got: %s", settingsPath, string(rawSettings))
	}

	callGeminiHook := func(toolName string, toolInput map[string]interface{}) agyhook.Decision {
		body, _ := json.Marshal(map[string]interface{}{
			"hook_event_name": "BeforeTool",
			"cwd":             dir,
			"tool_name":       toolName,
			"tool_input":      toolInput,
		})
		var out bytes.Buffer
		agyhook.Run(bytes.NewReader(body), &out, func(k string) string { return env[k] })
		var d agyhook.Decision
		if err := json.Unmarshal(out.Bytes(), &d); err != nil {
			t.Fatalf("bad hook output %q: %v", out.String(), err)
		}
		return d
	}

	// 1. Safe read tool inside worktree is allowed immediately.
	resAllow := callGeminiHook("read_file", map[string]interface{}{"file_path": filepath.Join(dir, "README.md")})
	if resAllow.Decision != "allow" {
		t.Fatalf("expected decision=allow for read_file, got %+v", resAllow)
	}

	// 2. Reading a secret file (.env) triggers the gate; rejecting returns decision=deny with the note.
	events, unsub := svc.Subscribe("ses_gem_gate")
	defer unsub()
	go func() {
		evt := waitForEvent(t, events, "permission.asked")
		reqID, _ := evt.Properties["id"].(string)
		replyBody, _ := json.Marshal(map[string]string{"reply": "reject", "message": "blocked secret read"})
		resp, err := http.Post(srv.URL+"/api/hoxi/runner/permission/"+reqID+"/reply", "application/json", bytes.NewReader(replyBody))
		if err == nil {
			resp.Body.Close()
		}
	}()
	resDeny := callGeminiHook("read_file", map[string]interface{}{"file_path": filepath.Join(dir, ".env")})
	if resDeny.Decision != "deny" || !strings.Contains(resDeny.Reason, "blocked secret read") {
		t.Fatalf("expected decision=deny for .env read, got %+v", resDeny)
	}
}

func TestGeminiCLIAgyFallbackAndTranslation(t *testing.T) {
	if gotModel, gotEff := ResolveGeminiAgyModelAndEffort("auto", ""); gotModel != "" || gotEff != "" {
		t.Fatalf("ResolveGeminiAgyModelAndEffort(auto) = (%q, %q), want empty", gotModel, gotEff)
	}
	if gotModel, _ := ResolveGeminiAgyModelAndEffort("gemini-2.5-pro", ""); gotModel != "gemini-4-argon" {
		t.Fatalf("ResolveGeminiAgyModelAndEffort(gemini-2.5-pro) = %q, want gemini-4-argon", gotModel)
	}
	if gotModel, _ := ResolveGeminiAgyModelAndEffort("gemini-2.5-flash", ""); gotModel != "gemini-3.8-flash" {
		t.Fatalf("ResolveGeminiAgyModelAndEffort(gemini-2.5-flash) = %q, want gemini-3.8-flash", gotModel)
	}
	if gotModel, gotEff := ResolveGeminiAgyModelAndEffort("gemini-4-argon", "high"); gotModel != "gemini-4-argon" || gotEff != "high" {
		t.Fatalf("ResolveGeminiAgyModelAndEffort(gemini-4-argon, high) = (%q, %q), want (gemini-4-argon, high)", gotModel, gotEff)
	}

	tr := NewTurnTranslator("ses_gem_agy", string(RuntimeGeminiCLI), "msg_a_2", "msg_u_2", "gemini-cli", "auto", "build", "")
	lines := []string{
		`{"event":"init","conversation_id":"conv-agy-123","init":{"model":"gemini-4-argon","cwd":"/repo","permission_mode":"accept-edits"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-agy-123","step_index":1,"state":"ACTIVE","step_type":"agent_response","thinking_delta":"Analyzing...","text_delta":"All set."}}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-agy-123","step_index":1,"state":"DONE","step_type":"agent_response","duration_seconds":0.5,"usage":{"input_tokens":500,"output_tokens":40,"thinking_tokens":20,"cache_read_tokens":100,"total_tokens":560}}}`,
		`{"event":"result","result":{"conversation_id":"conv-agy-123","status":"completed","response":"All set.","duration_seconds":0.6,"num_turns":1,"usage":{"input_tokens":500,"output_tokens":40,"thinking_tokens":20,"cache_read_tokens":100,"total_tokens":560}}}`,
	}
	for _, line := range lines {
		_ = tr.FeedLine(line)
	}
	if !tr.IsTurnFinished() {
		t.Fatal("expected IsTurnFinished() == true for agy stream inside gemini-cli translator")
	}
	if tr.ExternalID() != "conv-agy-123" {
		t.Fatalf("ExternalID() = %q, want conv-agy-123", tr.ExternalID())
	}
	if tr.msgInfo.ModelID != "gemini-4-argon" {
		t.Fatalf("msgInfo.ModelID = %q, want gemini-4-argon", tr.msgInfo.ModelID)
	}
}

func TestGeminiCLISandboxAllowsHookHomeAndProtectsSettings(t *testing.T) {
	if !sandbox.Available() {
		t.Skip("seatbelt backend unavailable")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	t.Setenv("GEMINI_HOME", filepath.Join(home, ".gemini"))

	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(workspace, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	svc.SetHookBaseURL(srv.URL)

	envList, revoke := svc.issueGeminiHook("build", "ses_gem_sbx", "msg_a_sbx")
	t.Cleanup(revoke)
	var homeDir string
	for _, kv := range envList {
		if k, v, ok := strings.Cut(kv, "="); ok && k == geminiCLIHomeEnv {
			homeDir = v
		}
	}
	if homeDir == "" {
		t.Fatal("expected GEMINI_CLI_HOME to be set")
	}

	rec := SessionRecord{
		ID:             "ses_gem_sbx",
		Runtime:        string(RuntimeGeminiCLI),
		ProjectDir:     workspace,
		WorktreePath:   workspace,
		CommandSandbox: true,
	}

	// 1. Confined Gemini CLI process can write to GEMINI_CLI_HOME/.gemini/state.json and symlinked tmp/history.
	stateProbe := filepath.Join(homeDir, ".gemini", "state.json")
	tmpProbe := filepath.Join(homeDir, ".gemini", "tmp", "session-1.json")
	histProbe := filepath.Join(homeDir, ".gemini", "history", "hist-1.json")
	bin, args, err := svc.confineRuntimeCommand(rec, "/bin/sh", []string{
		"-c",
		`printf '{"ok":true}' > "$1" && printf '{"chat":1}' > "$2" && printf '{"h":1}' > "$3"`,
		"sh", stateProbe, tmpProbe, histProbe,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		t.Fatalf("expected confined Gemini CLI writes to succeed, got %v: %s", err, string(out))
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "tmp", "session-1.json")); err != nil {
		t.Fatalf("expected write through tmp symlink to persist in ~/.gemini/tmp: %v", err)
	}

	// 2. Confined process still cannot tamper with GEMINI_CLI_HOME/.gemini/settings.json (BeforeTool hook config).
	hookSettings := filepath.Join(homeDir, ".gemini", "settings.json")
	bin, args, err = svc.confineRuntimeCommand(rec, "/bin/sh", []string{
		"-c",
		`printf '{}' > "$1"`,
		"sh", hookSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(bin, args...).Run(); err == nil {
		t.Fatalf("expected confined write to %s to be denied by Seatbelt", hookSettings)
	}
}

func TestGeminiCLIStaleResumeFallsBackWithConversationHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))
	t.Setenv("GEMINI_API_KEY", "AIzaSyTestDirectKey123456")

	workDir := filepath.Join(t.TempDir(), ".hoxi", "worktrees", "ses_gem_resume")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projDir := filepath.Dir(filepath.Dir(filepath.Dir(workDir)))

	logFile := filepath.Join(t.TempDir(), "invocations.log")
	script := `#!/bin/sh
stdin_payload="$(cat)"
printf "===INVOCATION===\nARGS:%s|STDIN:%s\n" "$*" "$stdin_payload" >> "` + logFile + `"
for arg in "$@"; do
  if [ "$arg" = "--resume" ]; then
    printf '{"type":"system","subtype":"init","session_id":"stale-gem-id"}\n'
    echo "Error: session stale-gem-id not found" >&2
    exit 1
  fi
done
printf '{"type":"system","subtype":"init","session_id":"fresh-gem-id"}\n'
printf '{"type":"init","session_id":"fresh-gem-id","model":"gemini-2.5-pro"}\n'
printf '{"type":"message","role":"assistant","content":"Recovered without resume.","delta":true}\n'
printf '{"type":"result","status":"success"}\n'
`
	gemStub := execstub.Write(t, t.TempDir(), "gemini", script)
	t.Setenv("GEMINI_BIN", gemStub)

	svc := NewService(projDir, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	if err := svc.store.Put(SessionRecord{
		ID:           "ses_gem_resume",
		Runtime:      string(RuntimeGeminiCLI),
		ExternalID:   "stale-gem-id",
		ProjectDir:   projDir,
		WorktreePath: workDir,
		SandboxMode:  "worktree",
		Mode:         "plan",
		ModelID:      "gemini-2.5-pro",
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_u_prev", SessionID: "ses_gem_resume", Role: "user"},
				Parts: []MessagePart{{ID: "prt_u_prev", Type: "text", Text: "Remember code word PHOENIX-42"}},
			},
			{
				Info:  MessageInfo{ID: "msg_a_prev", SessionID: "ses_gem_resume", Role: "assistant"},
				Parts: []MessagePart{{ID: "prt_a_prev", Type: "text", Text: "Understood, PHOENIX-42."}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	svc.recordCLISystemDelivered("ses_gem_resume", string(RuntimeGeminiCLI), "Caller custom system rule")
	reply, err := svc.RunPromptAndWait(context.Background(), "ses_gem_resume", PromptRequest{
		Text:   "What was the code word?",
		Agent:  "plan",
		System: "Caller custom system rule",
	})
	if err != nil {
		t.Fatalf("RunPromptAndWait failed: %v", err)
	}
	if !strings.Contains(reply, "Recovered without resume.") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	rec, _ := svc.store.Get("ses_gem_resume")
	if rec.ExternalID != "fresh-gem-id" {
		t.Fatalf("ExternalID = %q, want fresh-gem-id", rec.ExternalID)
	}
	rawLog, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimSpace(string(rawLog)), "===INVOCATION===")
	var lines []string
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 CLI invocations (1 failed resume + 1 fresh retry), got %d:\n%s", len(lines), string(rawLog))
	}
	if !strings.Contains(lines[0], "--resume stale-gem-id") {
		t.Fatalf("expected first invocation to attempt --resume stale-gem-id, got: %s", lines[0])
	}
	if strings.Contains(lines[1], "--resume") {
		t.Fatalf("expected second invocation to omit --resume, got: %s", lines[1])
	}
	if !strings.Contains(lines[1], "PHOENIX-42") {
		t.Fatalf("expected second invocation stdin to include prior conversation history (PHOENIX-42), got: %s", lines[1])
	}
	if strings.Contains(lines[0], "Caller custom system rule") {
		t.Fatalf("expected first invocation with --resume to skip already-delivered system instructions, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "Caller custom system rule") || !strings.Contains(lines[1], "Hoxi command execution:") {
		t.Fatalf("expected second invocation stdin to restore system instructions on fresh retry, got: %s", lines[1])
	}
}

func TestLinkOrCopyGeminiEntry(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "oauth_creds.json")
	if err := os.WriteFile(srcFile, []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dstFile := filepath.Join(dir, "isolated", "oauth_creds.json")
	if err := os.MkdirAll(filepath.Dir(dstFile), 0o700); err != nil {
		t.Fatal(err)
	}
	linkOrCopyGeminiEntry(srcFile, dstFile)
	got, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", dstFile, err)
	}
	if string(got) != `{"token":"secret"}` {
		t.Fatalf("unexpected linked/copied file content: %q", string(got))
	}

	srcDir := filepath.Join(dir, "history")
	if err := os.MkdirAll(srcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(dir, "isolated", "history")
	linkOrCopyGeminiEntry(srcDir, dstDir)
	if info, err := os.Stat(dstDir); err != nil || !info.IsDir() {
		t.Fatalf("expected %s to exist as directory, got info=%v err=%v", dstDir, info, err)
	}
}
