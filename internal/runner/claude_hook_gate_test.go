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

	"github.com/hoxiai/hoxi/internal/agyhook"
	"github.com/hoxiai/hoxi/internal/statusbar"
)

type claudeGateFixture struct {
	svc     *Service
	srv     *httptest.Server
	tracker *statusbar.Tracker
	dir     string
	url     string
	headers map[string]string
	revoke  func()
}

// newClaudeGateFixture reads the hook URL and headers back out of the
// `--settings` document and expands them the way Claude Code does.
func newClaudeGateFixture(t *testing.T) *claudeGateFixture {
	t.Helper()
	dir := t.TempDir()
	tracker := statusbar.NewTracker()
	svc := NewService(dir, nil, tracker, filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeSmart})
	if err := svc.store.Put(SessionRecord{ID: "ses_cc_gate", Runtime: "claude-code", ProjectDir: dir, WorktreePath: dir, ExternalID: "cc-uuid-1"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	svc.SetHookBaseURL(srv.URL)
	args, envList, revoke := svc.issueClaudeHook("build", "ses_cc_gate", "msg_c_1")
	if len(args) != 2 || args[0] != "--settings" {
		t.Fatalf("hook args = %v", args)
	}
	env := map[string]string{}
	for _, kv := range envList {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	if strings.Contains(args[1], env[claudeHookTokenEnv]) {
		t.Fatal("hook token must not appear in argv")
	}
	var settings struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type           string            `json:"type"`
					URL            string            `json:"url"`
					Timeout        int               `json:"timeout"`
					Headers        map[string]string `json:"headers"`
					AllowedEnvVars []string          `json:"allowedEnvVars"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(args[1]), &settings); err != nil {
		t.Fatalf("settings are not JSON: %v", err)
	}
	if len(settings.Hooks.PreToolUse) != 1 || settings.Hooks.PreToolUse[0].Matcher != "*" {
		t.Fatalf("unexpected PreToolUse config: %s", args[1])
	}
	hook := settings.Hooks.PreToolUse[0].Hooks[0]
	if hook.Type != "http" || hook.Timeout != agyhook.TimeoutSeconds || len(hook.AllowedEnvVars) != 2 {
		t.Fatalf("unexpected hook: %+v", hook)
	}
	headers := map[string]string{}
	for k, v := range hook.Headers {
		for _, name := range hook.AllowedEnvVars {
			v = strings.ReplaceAll(v, "$"+name, env[name])
		}
		headers[k] = v
	}
	return &claudeGateFixture{svc: svc, srv: srv, tracker: tracker, dir: dir, url: hook.URL, headers: headers, revoke: revoke}
}

func (f *claudeGateFixture) payload(tool, command, cwd, session string) []byte {
	if cwd == "" {
		cwd = f.dir
	}
	raw, _ := json.Marshal(map[string]interface{}{
		"session_id":      session,
		"cwd":             cwd,
		"permission_mode": "acceptEdits",
		"hook_event_name": "PreToolUse",
		"tool_name":       tool,
		"tool_use_id":     "toolu_01abc",
		"tool_input":      map[string]interface{}{"command": command, "description": "Run it"},
	})
	return raw
}

func (f *claudeGateFixture) post(t *testing.T, body []byte) (int, claudeHookOutput) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range f.headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out claudeHookOutput
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (f *claudeGateFixture) decide(t *testing.T, body []byte) string {
	t.Helper()
	code, out := f.post(t, body)
	if code != http.StatusOK {
		t.Fatalf("hook status %d", code)
	}
	if out.HookSpecificOutput == nil {
		return ""
	}
	if out.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Fatalf("hookEventName = %q", out.HookSpecificOutput.HookEventName)
	}
	return out.HookSpecificOutput.PermissionDecision + "|" + out.HookSpecificOutput.PermissionDecisionReason
}

func (f *claudeGateFixture) asyncDecide(t *testing.T, body []byte) chan string {
	ch := make(chan string, 1)
	go func() { ch <- f.decide(t, body) }()
	return ch
}

func (f *claudeGateFixture) reply(t *testing.T, id, reply, message string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"reply": reply, "message": message})
	resp, err := http.Post(f.srv.URL+"/api/hoxi/runner/permission/"+id+"/reply", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestClaudeHookAsksForPythonAndAllowsOnce(t *testing.T) {
	t.Parallel()
	f := newClaudeGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_cc_gate")
	defer unsub()
	result := f.asyncDecide(t, f.payload("Bash", "python3 sum.py", "", "cc-uuid-1"))

	asked := waitForEvent(t, events, "permission.asked")
	id, _ := asked.Properties["id"].(string)
	if !strings.HasPrefix(id, runnerPermissionID) || asked.Properties["permission"] != "bash" {
		t.Fatalf("unexpected request: %#v", asked.Properties)
	}
	tool, _ := asked.Properties["tool"].(map[string]interface{})
	if tool["callID"] != "toolu_01abc" || tool["messageID"] != "msg_c_1" {
		t.Fatalf("request not linked to the tool card: %#v", tool)
	}
	meta, _ := asked.Properties["metadata"].(map[string]interface{})
	if meta["source"] != "claude-code" || meta["command"] != "python3 sum.py" || meta["description"] != "Run it" {
		t.Fatalf("metadata: %#v", meta)
	}
	if f.tracker.Snapshot().Attention != 1 {
		t.Fatalf("status bar attention = %d, want 1", f.tracker.Snapshot().Attention)
	}
	if code := f.reply(t, id, "once", ""); code != http.StatusOK {
		t.Fatalf("reply status %d", code)
	}
	if d := <-result; d != "allow|" {
		t.Fatalf("once: %q", d)
	}
	if f.tracker.Snapshot().Attention != 0 {
		t.Fatalf("status bar attention not cleared")
	}
}

func TestClaudeHookSafeCommandsAndOtherTools(t *testing.T) {
	t.Parallel()
	f := newClaudeGateFixture(t)
	if d := f.decide(t, f.payload("Bash", "git status && go test ./...", "", "cc-uuid-1")); d != "allow|" {
		t.Fatalf("safe command: %q", d)
	}
	// Normal in-worktree Edit and Read pass through to Claude Code's acceptEdits mode.
	editInWorktree, _ := json.Marshal(map[string]interface{}{
		"session_id":      "cc-uuid-1",
		"cwd":             f.dir,
		"permission_mode": "acceptEdits",
		"hook_event_name": "PreToolUse",
		"tool_name":       "Edit",
		"tool_use_id":     "toolu_edit_1",
		"tool_input":      map[string]interface{}{"file_path": filepath.Join(f.dir, "src", "main.go")},
	})
	if d := f.decide(t, editInWorktree); d != "" {
		t.Fatalf("in-worktree Edit should be left to Claude Code, got %q", d)
	}
	readInWorktree, _ := json.Marshal(map[string]interface{}{
		"session_id":      "cc-uuid-1",
		"cwd":             f.dir,
		"permission_mode": "acceptEdits",
		"hook_event_name": "PreToolUse",
		"tool_name":       "Read",
		"tool_use_id":     "toolu_read_1",
		"tool_input":      map[string]interface{}{"file_path": filepath.Join(f.dir, "README.md")},
	})
	if d := f.decide(t, readInWorktree); d != "" {
		t.Fatalf("in-worktree Read should be left to Claude Code, got %q", d)
	}
	plan := map[string]interface{}{}
	_ = json.Unmarshal(f.payload("Bash", "python3 sum.py", "", "cc-uuid-1"), &plan)
	plan["permission_mode"] = "plan"
	raw, _ := json.Marshal(plan)
	if d := f.decide(t, raw); d != "" {
		t.Fatalf("plan mode should be left to Claude Code, got %q", d)
	}
}

func TestClaudeHookGatesProtectedEditsOutsideReadsAndExternalTools(t *testing.T) {
	t.Parallel()
	f := newClaudeGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_cc_gate")
	defer unsub()

	callTool := func(tool, callID string, input map[string]interface{}) chan string {
		raw, _ := json.Marshal(map[string]interface{}{
			"session_id":      "cc-uuid-1",
			"cwd":             f.dir,
			"permission_mode": "acceptEdits",
			"hook_event_name": "PreToolUse",
			"tool_name":       tool,
			"tool_use_id":     callID,
			"tool_input":      input,
		})
		return f.asyncDecide(t, raw)
	}

	// 1. Protected in-worktree path (.claude/settings.json) must ask.
	resProtected := callTool("Write", "toolu_prot", map[string]interface{}{
		"file_path": filepath.Join(f.dir, ".claude", "settings.json"),
	})
	asked1 := waitForEvent(t, events, "permission.asked")
	if asked1.Properties["permission"] != "tool" {
		t.Fatalf("expected tool permission for protected edit, got %#v", asked1.Properties)
	}
	id1, _ := asked1.Properties["id"].(string)
	f.reply(t, id1, "reject", "do not touch .claude")
	if d := <-resProtected; !strings.HasPrefix(d, "deny|") || !strings.Contains(d, "do not touch .claude") {
		t.Fatalf("expected deny on protected path, got %q", d)
	}

	// 2. Smart mode reads a routine out-of-worktree file without asking;
	// manual mode asks and returns allow when approved.
	outsideFile := filepath.Join(t.TempDir(), "secrets.txt")
	if d := <-callTool("Read", "toolu_out_read_smart", map[string]interface{}{"file_path": outsideFile}); d != "allow|" {
		t.Fatalf("smart mode out-of-worktree Read: %q", d)
	}
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeManual})
	resReadOutside := callTool("Read", "toolu_out_read", map[string]interface{}{
		"file_path": outsideFile,
	})
	asked2 := waitForEvent(t, events, "permission.asked")
	id2, _ := asked2.Properties["id"].(string)
	f.reply(t, id2, "once", "")
	if d := <-resReadOutside; d != "allow|" {
		t.Fatalf("expected allow after once for out-of-worktree Read, got %q", d)
	}

	// 3. WebFetch / MCP tools must ask and return allow when approved (instead of silent denial under --permission-prompts none).
	resWeb := callTool("WebFetch", "toolu_web", map[string]interface{}{
		"url": "https://example.com",
	})
	asked3 := waitForEvent(t, events, "permission.asked")
	meta3, _ := asked3.Properties["metadata"].(map[string]interface{})
	if meta3["source"] != "claude-code" {
		t.Fatalf("expected claude-code source metadata, got %#v", meta3)
	}
	id3, _ := asked3.Properties["id"].(string)
	f.reply(t, id3, "once", "")
	if d := <-resWeb; d != "allow|" {
		t.Fatalf("expected allow after once for WebFetch, got %q", d)
	}

	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeSmart})

	// 4. Lowercase "bash" tool name is gated identically to "Bash".
	resLowerBash := f.asyncDecide(t, f.payload("bash", "rm -rf build", "", "cc-uuid-1"))
	asked4 := waitForEvent(t, events, "permission.asked")
	id4, _ := asked4.Properties["id"].(string)
	f.reply(t, id4, "reject", "no rm")
	if d := <-resLowerBash; !strings.HasPrefix(d, "deny|") {
		t.Fatalf("expected deny for lowercase bash rm, got %q", d)
	}

	// 5. Glob with an escaping pattern must NOT be auto-allowed as in-worktree.
	resEscapingGlob := callTool("Glob", "toolu_glob_escape", map[string]interface{}{
		"pattern": "../../etc/passwd",
	})
	asked5 := waitForEvent(t, events, "permission.asked")
	meta5, _ := asked5.Properties["metadata"].(map[string]interface{})
	if meta5["externalDirectory"] != true {
		t.Fatalf("expected externalDirectory metadata on escaping Glob, got %#v", meta5)
	}
	id5, _ := asked5.Properties["id"].(string)
	f.reply(t, id5, "reject", "stay in worktree")
	if d := <-resEscapingGlob; !strings.HasPrefix(d, "deny|") {
		t.Fatalf("expected deny on escaping Glob, got %q", d)
	}

	// 6. Safe internal orchestration tools (TodoWrite) pass through, while unknown tools ask.
	rawTodo, _ := json.Marshal(map[string]interface{}{
		"session_id":      "cc-uuid-1",
		"cwd":             f.dir,
		"permission_mode": "acceptEdits",
		"hook_event_name": "PreToolUse",
		"tool_name":       "TodoWrite",
		"tool_use_id":     "toolu_todo",
		"tool_input":      map[string]interface{}{"todos": []interface{}{}},
	})
	if d := f.decide(t, rawTodo); d != "" {
		t.Fatalf("TodoWrite should pass through, got %q", d)
	}
	resUnknown := callTool("CustomExternalTool", "toolu_custom", map[string]interface{}{
		"target": "prod",
	})
	asked6 := waitForEvent(t, events, "permission.asked")
	id6, _ := asked6.Properties["id"].(string)
	f.reply(t, id6, "reject", "unknown tool")
	if d := <-resUnknown; !strings.HasPrefix(d, "deny|") {
		t.Fatalf("expected deny on unknown tool, got %q", d)
	}

	// 7. Symlink inside worktree pointing to .env must be gated as a secret read.
	if err := os.WriteFile(filepath.Join(f.dir, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkEnv := filepath.Join(f.dir, "readme-link.txt")
	if err := os.Symlink(filepath.Join(f.dir, ".env"), symlinkEnv); err != nil {
		t.Fatal(err)
	}
	resSymlinkEnv := callTool("Read", "toolu_symlink_env", map[string]interface{}{
		"file_path": symlinkEnv,
	})
	asked7 := waitForEvent(t, events, "permission.asked")
	if meta7, _ := asked7.Properties["metadata"].(map[string]interface{}); meta7["destructive"] != true {
		t.Fatalf("expected symlink to .env to be marked destructive, got %#v", asked7.Properties)
	}
	id7, _ := asked7.Properties["id"].(string)
	f.reply(t, id7, "reject", "no symlink secret")
	if d := <-resSymlinkEnv; !strings.HasPrefix(d, "deny|") {
		t.Fatalf("expected deny on symlink to .env, got %q", d)
	}

	// 8. Relative file_path when p.Cwd is outside the worktree must be gated as an external edit.
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeManual})
	outsideCwd := t.TempDir()
	rawRelOutside, _ := json.Marshal(map[string]interface{}{
		"session_id":      "cc-uuid-1",
		"cwd":             outsideCwd,
		"permission_mode": "acceptEdits",
		"hook_event_name": "PreToolUse",
		"tool_name":       "Write",
		"tool_use_id":     "toolu_rel_outside",
		"tool_input":      map[string]interface{}{"file_path": "escaped.go"},
	})
	resRelOutside := f.asyncDecide(t, rawRelOutside)
	asked8 := waitForEvent(t, events, "permission.asked")
	if meta8, _ := asked8.Properties["metadata"].(map[string]interface{}); meta8["externalDirectory"] != true {
		t.Fatalf("expected relative path under external cwd to be flagged externalDirectory, got %#v", asked8.Properties)
	}
	id8, _ := asked8.Properties["id"].(string)
	f.reply(t, id8, "reject", "outside cwd")
	if d := <-resRelOutside; !strings.HasPrefix(d, "deny|") {
		t.Fatalf("expected deny on relative path under external cwd, got %q", d)
	}
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeSmart})
}

func TestClaudeHookDestructiveAlwaysAsks(t *testing.T) {
	t.Parallel()
	f := newClaudeGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_cc_gate")
	defer unsub()
	for _, cmd := range []string{"python3 sum.py", "rm -rf build", "git push --force"} {
		result := f.asyncDecide(t, f.payload("Bash", cmd, "", "cc-uuid-1"))
		id, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)
		f.reply(t, id, "always", "")
		if d := <-result; d != "allow|" {
			t.Fatalf("always %q: %q", cmd, d)
		}
	}
	if d := f.decide(t, f.payload("Bash", "python3 sum.py", "", "cc-uuid-1")); d != "allow|" {
		t.Fatalf("repeat after always should skip the prompt, got %q", d)
	}
	result := f.asyncDecide(t, f.payload("Bash", "rm -rf build", "", "cc-uuid-1"))
	id, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)
	f.reply(t, id, "reject", "keep the build dir")
	if d := <-result; !strings.HasPrefix(d, "deny|") || !strings.Contains(d, "keep the build dir") {
		t.Fatalf("destructive repeat must ask again, got %q", d)
	}
}

func TestClaudeHookCancelledOnStop(t *testing.T) {
	t.Parallel()
	f := newClaudeGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_cc_gate")
	defer unsub()
	result := f.asyncDecide(t, f.payload("Bash", "python3 sum.py", "", "cc-uuid-1"))
	id, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)
	f.svc.abortActiveProcess("ses_cc_gate")
	if d := <-result; !strings.HasPrefix(d, "deny|") {
		t.Fatalf("stop must deny the pending command, got %q", d)
	}
	if code := f.reply(t, id, "once", ""); code != http.StatusNotFound {
		t.Fatalf("reply after stop: %d", code)
	}
	if len(f.svc.listPendingApprovals("")) != 0 || f.tracker.Snapshot().Attention != 0 {
		t.Fatalf("cancelled request was not cleaned up")
	}
}

func TestClaudeHookDeniesForeignSessionAndDirectory(t *testing.T) {
	t.Parallel()
	f := newClaudeGateFixture(t)
	if d := f.decide(t, f.payload("Bash", "git status", "", "cc-uuid-other")); !strings.HasPrefix(d, "deny|") {
		t.Fatalf("foreign session: %q", d)
	}
	if d := f.decide(t, f.payload("Bash", "git status", t.TempDir(), "cc-uuid-1")); !strings.HasPrefix(d, "deny|") {
		t.Fatalf("outside cwd: %q", d)
	}
	if d := f.decide(t, f.payload("Bash", "  ", "", "cc-uuid-1")); !strings.HasPrefix(d, "deny|") {
		t.Fatalf("empty command: %q", d)
	}
}

func TestClaudeHookCredentialIsBoundToRuntime(t *testing.T) {
	t.Parallel()
	f := newClaudeGateFixture(t)
	agyEnv, revokeAgy := f.svc.issueHookEnv("ses_cc_gate", "msg_a")
	defer revokeAgy()
	agyToken := ""
	for _, kv := range agyEnv {
		if k, v, _ := strings.Cut(kv, "="); k == agyhook.EnvHookToken {
			agyToken = v
		}
	}
	claudeAuth := f.headers["Authorization"]
	f.headers["Authorization"] = "Bearer " + agyToken
	if code, _ := f.post(t, f.payload("Bash", "git status", "", "cc-uuid-1")); code != http.StatusUnauthorized {
		t.Fatalf("agy token on the Claude endpoint: %d", code)
	}
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+hookPreToolPath, bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", claudeAuth)
	req.Header.Set(agyhook.HeaderSession, "ses_cc_gate")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Claude token on the agy endpoint: %d", resp.StatusCode)
	}
	f.headers["Authorization"] = claudeAuth
	f.revoke()
	if code, _ := f.post(t, f.payload("Bash", "git status", "", "cc-uuid-1")); code != http.StatusUnauthorized {
		t.Fatalf("revoked token: %d", code)
	}
}

func TestIssueClaudeHookSkipsPlanModeAndMissingBaseURL(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	if args, env, revoke := svc.issueClaudeHook("build", "ses_cc_x", "msg"); args != nil || env != nil {
		revoke()
		t.Fatalf("expected no hook before SetHookBaseURL, got %v %v", args, env)
	}
	svc.SetHookBaseURL("http://127.0.0.1:1")
	if args, env, revoke := svc.issueClaudeHook("plan", "ses_cc_x", "msg"); args != nil || env != nil {
		revoke()
		t.Fatalf("plan mode must keep Claude's own policy, got %v %v", args, env)
	}
}
