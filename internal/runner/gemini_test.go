package runner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agyhook"
	"github.com/hoxiai/hoxi/internal/statusbar"
)

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
	settingsPath := env[geminiSystemSettingsEnv]
	if settingsPath == "" {
		t.Fatal("expected non-empty Gemini system settings path")
	}
	rawSettings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", settingsPath, err)
	}
	if !strings.Contains(string(rawSettings), "BeforeTool") {
		t.Fatalf("expected BeforeTool hook in %s, got: %s", settingsPath, string(rawSettings))
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
