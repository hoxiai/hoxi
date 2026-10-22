package runner

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/agyhook"
	"github.com/hoxiai/hoxi/internal/statusbar"
)

type gateFixture struct {
	svc     *Service
	srv     *httptest.Server
	tracker *statusbar.Tracker
	dir     string
	env     map[string]string
	revoke  func()
}

func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	dir := t.TempDir()
	tracker := statusbar.NewTracker()
	svc := NewService(dir, nil, tracker, filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeSmart})
	if err := svc.store.Put(SessionRecord{ID: "ses_agy_gate", Runtime: "antigravity", ProjectDir: dir, WorktreePath: dir, ExternalID: "conv-1"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	svc.SetHookBaseURL(srv.URL)
	envList, revoke := svc.issueHookEnv("ses_agy_gate", "msg_a_1")
	env := map[string]string{}
	for _, kv := range envList {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return &gateFixture{svc: svc, srv: srv, tracker: tracker, dir: dir, env: env, revoke: revoke}
}

func (f *gateFixture) payload(command, cwd, conv string) []byte {
	if cwd == "" {
		cwd = f.dir
	}
	raw, _ := json.Marshal(map[string]interface{}{
		"conversationId": conv,
		"stepIdx":        4,
		"workspacePaths": []string{f.dir},
		"toolCall": map[string]interface{}{
			"name": "run_command",
			"args": map[string]interface{}{"CommandLine": command, "Cwd": cwd, "toolSummary": "Run it"},
		},
	})
	return raw
}

// callHook goes through the real `hoxi agy-hook` client, exactly as agy would.
func (f *gateFixture) callHook(t *testing.T, body []byte) agyhook.Decision {
	t.Helper()
	var out bytes.Buffer
	agyhook.Run(bytes.NewReader(body), &out, func(k string) string { return f.env[k] })
	var d agyhook.Decision
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatalf("bad hook output %q", out.String())
	}
	return d
}

func (f *gateFixture) asyncHook(t *testing.T, body []byte) chan agyhook.Decision {
	ch := make(chan agyhook.Decision, 1)
	go func() { ch <- f.callHook(t, body) }()
	return ch
}

func waitForEvent(t *testing.T, ch chan SSEEvent, typ string) SSEEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type == typ {
				return evt
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", typ)
		}
	}
}

func (f *gateFixture) reply(t *testing.T, id, reply, message string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"reply": reply, "message": message})
	resp, err := http.Post(f.srv.URL+"/api/hoxi/runner/permission/"+id+"/reply", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestHookGateRejectsBadCredentials(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	post := func(token, session string) int {
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+hookPreToolPath, bytes.NewReader(f.payload("git status", "", "")))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set(agyhook.HeaderSession, session)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	token := f.env[agyhook.EnvHookToken]
	if got := post("", "ses_agy_gate"); got != http.StatusUnauthorized {
		t.Fatalf("missing token: %d", got)
	}
	if got := post(token, "ses_other"); got != http.StatusUnauthorized {
		t.Fatalf("wrong session: %d", got)
	}
	if got := post(token, "ses_agy_gate"); got != http.StatusOK {
		t.Fatalf("valid credential: %d", got)
	}
	f.revoke()
	if got := post(token, "ses_agy_gate"); got != http.StatusUnauthorized {
		t.Fatalf("revoked token: %d", got)
	}
	if d := f.callHook(t, f.payload("git status", "", "")); d.Decision != "deny" {
		t.Fatalf("hook with revoked token must fail closed, got %q", d.Decision)
	}
}

func TestHookGateAutoApprovesSafeCommandsAndPassesControlTools(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	if d := f.callHook(t, f.payload("git status && go test ./...", "", "conv-1")); d.Decision != "auto_approve" {
		t.Fatalf("safe command: %q", d.Decision)
	}
	for _, tool := range []string{"finish", "task_boundary", "ask_question"} {
		other, _ := json.Marshal(map[string]interface{}{"toolCall": map[string]interface{}{"name": tool}})
		if d := f.callHook(t, other); d.Decision != "allow" {
			t.Fatalf("control tool %s should pass through, got %q", tool, d.Decision)
		}
	}
}

func TestHookGateGatesUnknownTools(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	body, _ := json.Marshal(map[string]interface{}{
		"conversationId": "conv-1",
		"stepIdx":        7,
		"toolCall": map[string]interface{}{
			"name": "mystery_action",
			"args": map[string]interface{}{"Target": "workspace"},
		},
	})
	result := f.asyncHook(t, body)
	asked := waitForEvent(t, events, "permission.asked")
	if asked.Properties["permission"] != "tool" {
		t.Fatalf("unknown tool should ask through Hoxi, got %#v", asked.Properties)
	}
	id, _ := asked.Properties["id"].(string)
	if id == "" {
		t.Fatalf("missing permission id")
	}
	if code := f.reply(t, id, "always", ""); code != http.StatusOK {
		t.Fatalf("reply status %d", code)
	}
	if d := <-result; d.Decision != "auto_approve" {
		t.Fatalf("unknown tool after approval: %q", d.Decision)
	}
	if d := f.callHook(t, body); d.Decision != "auto_approve" {
		t.Fatalf("repeat unknown tool should reuse the always grant, got %q", d.Decision)
	}
	for _, tool := range []string{
		"read_secret",
		"view_secret",
		"list_secrets",
		"find_secrets",
		"grep_secrets",
		"browser_get_cookies",
		"capture_browser_cookies",
	} {
		call, _ := json.Marshal(map[string]interface{}{
			"conversationId": "conv-1",
			"stepIdx":        8,
			"toolCall": map[string]interface{}{
				"name": tool,
				"args": map[string]interface{}{"url": "https://example.com"},
			},
		})
		res := f.asyncHook(t, call)
		evt := waitForEvent(t, events, "permission.asked")
		if evt.Properties["permission"] != "tool" {
			t.Fatalf("%s must ask through Hoxi, got %#v", tool, evt.Properties)
		}
		reqID, _ := evt.Properties["id"].(string)
		f.reply(t, reqID, "reject", "blocked")
		if d := <-res; d.Decision != "deny" {
			t.Fatalf("%s after reject: %q", tool, d.Decision)
		}
	}
	// Network tools follow the webfetch rule: allowed in smart mode, asked in manual.
	for _, tool := range []string{"read_url_content", "search_web"} {
		call, _ := json.Marshal(map[string]interface{}{
			"conversationId": "conv-1",
			"toolCall":       map[string]interface{}{"name": tool, "args": map[string]interface{}{"Url": "https://example.com"}},
		})
		if d := f.callHook(t, call); d.Decision != "auto_approve" {
			t.Fatalf("%s in smart mode: %q", tool, d.Decision)
		}
		setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeManual})
		res := f.asyncHook(t, call)
		reqID, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)
		f.reply(t, reqID, "reject", "manual")
		if d := <-res; d.Decision != "deny" {
			t.Fatalf("%s in manual mode after reject: %q", tool, d.Decision)
		}
		setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeSmart})
	}
}

func TestHookGateAutoApprovesReadsInWorktreeAndGatesOutsideReads(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	read := func(tool, path string) []byte {
		raw, _ := json.Marshal(map[string]interface{}{
			"conversationId": "conv-1",
			"stepIdx":        4,
			"workspacePaths": []string{f.dir},
			"toolCall": map[string]interface{}{
				"name": tool,
				"args": map[string]interface{}{"Path": path, "AbsolutePath": path, "DirectoryPath": path, "SearchPath": path, "toolSummary": "Read file"},
			},
		})
		return raw
	}
	outsideFile := filepath.Join(t.TempDir(), "outside.txt")
	for _, tool := range []string{"view_file", "read_file", "list_dir", "find_by_name", "grep_search"} {
		if d := f.callHook(t, read(tool, filepath.Join(f.dir, "inside.txt"))); d.Decision != "auto_approve" {
			t.Fatalf("%s inside workspace: %q", tool, d.Decision)
		}
		if d := f.callHook(t, read(tool, outsideFile)); d.Decision != "auto_approve" {
			t.Fatalf("%s routine outside read in smart mode: %q", tool, d.Decision)
		}
	}
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeManual})
	for _, tool := range []string{"view_file", "read_file", "list_dir", "find_by_name", "grep_search"} {
		if d := f.callHook(t, read(tool, filepath.Join(f.dir, "inside.txt"))); d.Decision != "auto_approve" {
			t.Fatalf("%s inside workspace in manual mode: %q", tool, d.Decision)
		}
		res := f.asyncHook(t, read(tool, outsideFile))
		asked := waitForEvent(t, events, "permission.asked")
		if asked.Properties["permission"] != "tool" {
			t.Fatalf("%s outside workspace must ask, got %#v", tool, asked.Properties)
		}
		id, _ := asked.Properties["id"].(string)
		f.reply(t, id, "once", "")
		if d := <-res; d.Decision != "auto_approve" {
			t.Fatalf("%s outside workspace after once: %q", tool, d.Decision)
		}
	}
}

func TestHookGateEditsStayInsideWorktree(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	write := func(tool, path string) []byte {
		raw, _ := json.Marshal(map[string]interface{}{
			"conversationId": "conv-1",
			"stepIdx":        4,
			"toolCall": map[string]interface{}{
				"name": tool,
				"args": map[string]interface{}{"TargetFile": path, "toolSummary": "Edit file"},
			},
		})
		return raw
	}
	for _, tool := range []string{"write_to_file", "replace_file_content", "multi_replace_file_content", "sed_file"} {
		if d := f.callHook(t, write(tool, filepath.Join(f.dir, "inside.txt"))); d.Decision != "auto_approve" {
			t.Fatalf("%s inside workspace: %q", tool, d.Decision)
		}
		// Routine targets outside the worktree follow the approval mode, as
		// OpenCode's external_directory requests do.
		if d := f.callHook(t, write(tool, filepath.Join(t.TempDir(), "outside.txt"))); d.Decision != "auto_approve" {
			t.Fatalf("%s outside workspace in smart mode: %q", tool, d.Decision)
		}
		// Relative targets are resolved against the session worktree.
		if d := f.callHook(t, write(tool, "sub/inside.txt")); d.Decision != "auto_approve" {
			t.Fatalf("%s relative target: %q", tool, d.Decision)
		}
		if d := f.callHook(t, write(tool, ".vscode/extensions.json")); d.Decision != "auto_approve" {
			t.Fatalf("%s non-protected .vscode file: %q", tool, d.Decision)
		}
	}

	// Execution-controlling paths inside the worktree must still ask.
	for _, rel := range []string{
		".git/config",
		".git/hooks/pre-commit",
		".agents/hooks.json",
		".opencode/plugins/hoxi-permissions.js",
		".claude/settings.json",
		".hoxi/config.json",
		".vscode/settings.json",
		".envrc",
	} {
		res := f.asyncHook(t, write("write_to_file", filepath.Join(f.dir, rel)))
		asked := waitForEvent(t, events, "permission.asked")
		if asked.Properties["permission"] != "tool" {
			t.Fatalf("protected path %s must ask, got %#v", rel, asked.Properties)
		}
		id, _ := asked.Properties["id"].(string)
		f.reply(t, id, "reject", "protected path")
		if d := <-res; d.Decision != "deny" {
			t.Fatalf("protected path %s after reject: %q", rel, d.Decision)
		}
	}

	// Edit calls without a recognized path argument must ask instead of returning "allow".
	noPath, _ := json.Marshal(map[string]interface{}{
		"conversationId": "conv-1",
		"stepIdx":        5,
		"toolCall": map[string]interface{}{
			"name": "write_to_file",
			"args": map[string]interface{}{"unrecognized": "foo.txt"},
		},
	})
	res := f.asyncHook(t, noPath)
	asked := waitForEvent(t, events, "permission.asked")
	id, _ := asked.Properties["id"].(string)
	f.reply(t, id, "reject", "missing path")
	if d := <-res; d.Decision != "deny" {
		t.Fatalf("edit without path after reject: %q", d.Decision)
	}

	// Symlinks (including dangling symlinks) escaping the worktree must be rejected.
	outsideDir := t.TempDir()
	escapeExisting := filepath.Join(f.dir, "escape-existing")
	if err := os.Symlink(outsideDir, escapeExisting); err != nil {
		t.Fatal(err)
	}
	escapeDangling := filepath.Join(f.dir, "escape-dangling")
	if err := os.Symlink(filepath.Join(outsideDir, "nonexistent-target.txt"), escapeDangling); err != nil {
		t.Fatal(err)
	}
	if pathWithin(f.dir, filepath.Join(escapeExisting, "file.txt")) {
		t.Fatal("pathWithin must reject symlink pointing outside worktree")
	}
	if pathWithin(f.dir, escapeDangling) {
		t.Fatal("pathWithin must reject dangling symlink pointing outside worktree")
	}

	// Manual mode asks for outside edits (including a symlink escape) and
	// for ordinary in-worktree edits.
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeManual})
	for _, target := range []string{escapeDangling, filepath.Join(t.TempDir(), "outside.txt"), filepath.Join(f.dir, "inside.txt")} {
		res := f.asyncHook(t, write("write_to_file", target))
		asked := waitForEvent(t, events, "permission.asked")
		id, _ := asked.Properties["id"].(string)
		f.reply(t, id, "reject", "manual")
		if d := <-res; d.Decision != "deny" {
			t.Fatalf("manual edit of %s after reject: %q", target, d.Decision)
		}
	}
	// A credential path outside the worktree asks even in auto mode.
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	res = f.asyncHook(t, write("write_to_file", filepath.Join(t.TempDir(), ".ssh", "authorized_keys")))
	asked = waitForEvent(t, events, "permission.asked")
	if meta, _ := asked.Properties["metadata"].(map[string]interface{}); meta["destructive"] != true {
		t.Fatalf("credential edit must be destructive, got %#v", asked.Properties)
	}
	id, _ = asked.Properties["id"].(string)
	f.reply(t, id, "reject", "no")
	if d := <-res; d.Decision != "deny" {
		t.Fatalf("credential edit after reject: %q", d.Decision)
	}
}

func TestHookGateEditsAcceptQuotedPaths(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	raw, _ := json.Marshal(map[string]interface{}{
		"conversationId": "conv-1",
		"stepIdx":        4,
		"toolCall": map[string]interface{}{
			"name": "read_file",
			"args": map[string]interface{}{"Path": `"` + filepath.Join(f.dir, "inside.txt") + `"`},
		},
	})
	if d := f.callHook(t, raw); d.Decision != "auto_approve" {
		t.Fatalf("quoted path: %q", d.Decision)
	}
	outside, _ := json.Marshal(map[string]interface{}{
		"conversationId": "conv-1",
		"toolCall": map[string]interface{}{
			"name": "write_to_file",
			"args": map[string]interface{}{"TargetFile": `"` + filepath.Join(t.TempDir(), "outside.txt") + `"`},
		},
	})
	if d := f.callHook(t, outside); d.Decision != "auto_approve" {
		t.Fatalf("quoted routine outside path in smart mode: %q", d.Decision)
	}
}

func TestHookGateDeniesForeignConversationAndDirectory(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	if d := f.callHook(t, f.payload("git status", "", "conv-other")); d.Decision != "deny" {
		t.Fatalf("foreign conversation: %q", d.Decision)
	}
	if d := f.callHook(t, f.payload("git status", t.TempDir(), "conv-1")); d.Decision != "deny" {
		t.Fatalf("outside cwd: %q", d.Decision)
	}
	if d := f.callHook(t, f.payload("git status", filepath.Join(f.dir, "sub", ".."), "conv-1")); d.Decision != "auto_approve" {
		t.Fatalf("cwd inside session dir: %q", d.Decision)
	}
}

func TestHookGateAsksUserAndRelaysOnce(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	result := f.asyncHook(t, f.payload("rm -rf build", "", "conv-1"))

	asked := waitForEvent(t, events, "permission.asked")
	id, _ := asked.Properties["id"].(string)
	if !strings.HasPrefix(id, runnerPermissionID) || asked.Properties["permission"] != "bash" {
		t.Fatalf("unexpected request: %#v", asked.Properties)
	}
	tool, _ := asked.Properties["tool"].(map[string]interface{})
	if tool["callID"] != "call_msg_a_1_4" || tool["messageID"] != "msg_a_1" {
		t.Fatalf("request not linked to the tool card: %#v", tool)
	}
	if f.tracker.Snapshot().Attention != 1 {
		t.Fatalf("status bar attention = %d, want 1", f.tracker.Snapshot().Attention)
	}
	if list := f.svc.listPendingApprovals(f.dir); len(list) != 1 {
		t.Fatalf("pending list = %d, want 1", len(list))
	}
	if list := f.svc.listPendingApprovals(t.TempDir()); len(list) != 0 {
		t.Fatalf("pending list for another directory = %d, want 0", len(list))
	}

	if code := f.reply(t, id, "once", ""); code != http.StatusOK {
		t.Fatalf("reply status %d", code)
	}
	if d := <-result; d.Decision != "auto_approve" {
		t.Fatalf("once: %q", d.Decision)
	}
	replied := waitForEvent(t, events, "permission.replied")
	if replied.Properties["requestID"] != id || replied.Properties["reply"] != "once" {
		t.Fatalf("replied event: %#v", replied.Properties)
	}
	if f.tracker.Snapshot().Attention != 0 {
		t.Fatalf("status bar attention not cleared")
	}
	if code := f.reply(t, id, "once", ""); code != http.StatusNotFound {
		t.Fatalf("second reply status %d, want 404", code)
	}
}

func TestHookGateRejectCarriesUserMessage(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	result := f.asyncHook(t, f.payload("npm install lodash", "", "conv-1"))
	id, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)
	f.reply(t, id, "reject", "use pnpm instead")
	d := <-result
	if d.Decision != "deny" || !strings.Contains(d.Reason, "use pnpm instead") {
		t.Fatalf("reject: %+v", d)
	}
}

func TestHookGateAlwaysSkipsRepeatsButNeverDestructive(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	for _, cmd := range []string{"npm install lodash", "rm -rf build"} {
		result := f.asyncHook(t, f.payload(cmd, "", "conv-1"))
		id, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)
		f.reply(t, id, "always", "")
		if d := <-result; d.Decision != "auto_approve" {
			t.Fatalf("always %q: %q", cmd, d.Decision)
		}
	}
	if d := f.callHook(t, f.payload("npm install lodash", "", "conv-1")); d.Decision != "auto_approve" {
		t.Fatalf("repeat after always should skip the prompt, got %q", d.Decision)
	}
	result := f.asyncHook(t, f.payload("rm -rf build", "", "conv-1"))
	id, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)
	f.svc.cancelSessionApprovals("ses_agy_gate", "stopped")
	if d := <-result; d.Decision != "deny" {
		t.Fatalf("destructive repeat must ask again and deny on stop, got %q", d.Decision)
	}
	if code := f.reply(t, id, "once", ""); code != http.StatusNotFound {
		t.Fatalf("reply after cancel: %d", code)
	}
}

func TestHookGateAsksForRiskyTools(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	payload, _ := json.Marshal(map[string]interface{}{
		"conversationId": "conv-1",
		"stepIdx":        4,
		"toolCall": map[string]interface{}{
			"name": "call_mcp_tool",
			"args": map[string]interface{}{"ServerName": "github", "ToolName": "create_issue"},
		},
	})
	result := f.asyncHook(t, payload)
	asked := waitForEvent(t, events, "permission.asked")
	if asked.Properties["permission"] != "tool" {
		t.Fatalf("unexpected request: %#v", asked.Properties)
	}
	if patterns, _ := asked.Properties["patterns"].([]string); len(patterns) != 1 || patterns[0] != "call_mcp_tool" {
		t.Fatalf("patterns: %#v", asked.Properties["patterns"])
	}
	id, _ := asked.Properties["id"].(string)
	if code := f.reply(t, id, "once", ""); code != http.StatusOK {
		t.Fatalf("reply status %d", code)
	}
	if d := <-result; d.Decision != "auto_approve" {
		t.Fatalf("once: %q", d.Decision)
	}

	// "always" remembers the exact tool + args, but a different MCP call asks again.
	result = f.asyncHook(t, payload)
	id, _ = waitForEvent(t, events, "permission.asked").Properties["id"].(string)
	f.reply(t, id, "always", "")
	if d := <-result; d.Decision != "auto_approve" {
		t.Fatalf("always: %q", d.Decision)
	}
	if d := f.callHook(t, payload); d.Decision != "auto_approve" {
		t.Fatalf("repeat after always: %q", d.Decision)
	}
	different, _ := json.Marshal(map[string]interface{}{
		"conversationId": "conv-1",
		"toolCall": map[string]interface{}{
			"name": "execute_browser_javascript",
			"args": map[string]interface{}{"Script": "fetch('/admin')"},
		},
	})
	result = f.asyncHook(t, different)
	id, _ = waitForEvent(t, events, "permission.asked").Properties["id"].(string)
	f.reply(t, id, "reject", "no browser JS")
	if d := <-result; d.Decision != "deny" || !strings.Contains(d.Reason, "no browser JS") {
		t.Fatalf("reject risky tool: %+v", d)
	}
}

func TestHookGateTimesOutClosed(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	f.svc.gate.waitLimit = 50 * time.Millisecond
	d := f.callHook(t, f.payload("rm -rf build", "", "conv-1"))
	if d.Decision != "deny" || !strings.Contains(d.Reason, "timed out") {
		t.Fatalf("timeout: %+v", d)
	}
	if len(f.svc.listPendingApprovals("")) != 0 || f.tracker.Snapshot().Attention != 0 {
		t.Fatalf("timed-out request was not cleaned up")
	}
}

func TestIssueHookEnvWithoutBaseURLIsPassthrough(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	env, revoke := svc.issueHookEnv("ses_agy_x", "msg")
	defer revoke()
	if env != nil {
		t.Fatalf("expected no hook env before SetHookBaseURL, got %v", env)
	}
}

func TestApprovalAuditAndSessionGrantsLifecycle(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()

	cmd := "curl -H 'Authorization: Bearer sk-secret-token-123456' https://example.com/api"
	result := f.asyncHook(t, f.payload(cmd, "", "conv-1"))
	asked := waitForEvent(t, events, "permission.asked")
	if _, ok := asked.Properties["expiresAt"].(int64); !ok {
		t.Fatalf("expected expiresAt on permission.asked, got %#v", asked.Properties["expiresAt"])
	}
	id, _ := asked.Properties["id"].(string)
	if code := f.reply(t, id, "always", "ok with Authorization: Bearer sk-note-secret"); code != http.StatusOK {
		t.Fatalf("reply status %d", code)
	}
	if d := <-result; d.Decision != "auto_approve" {
		t.Fatalf("always decision: %+v", d)
	}

	// Verify active grants via HTTP GET /api/hoxi/permission/grants.
	resp, err := http.Get(f.srv.URL + "/api/hoxi/permission/grants")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	grantsRaw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var grantsResp struct {
		Grants []ApprovalGrant `json:"grants"`
	}
	if err := json.Unmarshal(grantsRaw, &grantsResp); err != nil {
		t.Fatal(err)
	}
	grants := grantsResp.Grants
	if len(grants) != 1 || grants[0].SessionID != "ses_agy_gate" || grants[0].Kind != "bash" {
		t.Fatalf("unexpected grants: %+v", grants)
	}
	if strings.Contains(grants[0].Pattern, "sk-secret-token-123456") {
		t.Fatalf("grant pattern leaked secret: %q", grants[0].Pattern)
	}
	// The raw key (which still contains the command verbatim) must never leave
	// the process: only the redacted pattern is serialized.
	if strings.Contains(string(grantsRaw), "sk-secret-token-123456") || strings.Contains(string(grantsRaw), `"key"`) {
		t.Fatalf("grant response leaked the raw key: %s", grantsRaw)
	}

	// Revoke the grant via DELETE /api/hoxi/permission/grants?id=...
	delReq, _ := http.NewRequest(http.MethodDelete, f.srv.URL+"/api/hoxi/permission/grants?id="+grants[0].ID, nil)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete grant status %d", delResp.StatusCode)
	}

	// Repeat command must ask again now that the grant was revoked.
	result2 := f.asyncHook(t, f.payload(cmd, "", "conv-1"))
	asked2 := waitForEvent(t, events, "permission.asked")
	id2, _ := asked2.Properties["id"].(string)
	f.reply(t, id2, "reject", "no longer allowed")
	if d := <-result2; d.Decision != "deny" {
		t.Fatalf("expected deny after grant revocation, got %+v", d)
	}

	// Verify persistent audit log via HTTP GET /api/hoxi/permission/audit and reload from disk.
	auditResp, err := http.Get(f.srv.URL + "/api/hoxi/permission/audit")
	if err != nil {
		t.Fatal(err)
	}
	defer auditResp.Body.Close()
	var auditBody struct {
		Entries []ApprovalAuditEntry `json:"entries"`
	}
	if err := json.NewDecoder(auditResp.Body).Decode(&auditBody); err != nil {
		t.Fatal(err)
	}
	entries := auditBody.Entries
	if len(entries) != 2 || entries[0].Decision != "reject" || entries[1].Decision != "always" {
		t.Fatalf("unexpected audit entries: %+v", entries)
	}
	for _, e := range entries {
		if strings.Contains(e.Summary, "sk-secret") || strings.Contains(e.Message, "sk-note-secret") {
			t.Fatalf("audit entry leaked secret: %+v", e)
		}
	}
	reloaded := newApprovalAuditStore(f.svc.audit.path)
	if len(reloaded.list("", 10)) != 2 {
		t.Fatalf("expected 2 persisted entries on disk, got %d", len(reloaded.list("", 10)))
	}
}

func TestApprovalRejectsAlwaysFromTunnelAndDeduplicatesCallID(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()

	// Two concurrent hook invocations with the same stepIdx/callID coalesce into one pending card.
	body := f.payload("npm install react", "", "conv-1")
	res1 := f.asyncHook(t, body)
	asked := waitForEvent(t, events, "permission.asked")
	id, _ := asked.Properties["id"].(string)
	res2 := f.asyncHook(t, body)
	time.Sleep(40 * time.Millisecond)
	if len(f.svc.listPendingApprovals("")) != 1 {
		t.Fatalf("expected deduplicated single pending approval, got %d", len(f.svc.listPendingApprovals("")))
	}

	// Tunnel attempt with reply="always" is rejected with 403 and leaves the request pending.
	alwaysBody, _ := json.Marshal(map[string]string{"reply": "always"})
	tunnelReq, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/hoxi/permission/"+id+"/reply", bytes.NewReader(alwaysBody))
	tunnelReq.Header.Set("Content-Type", "application/json")
	tunnelReq.Header.Set("X-Hoxi-Request-Source", "tunnel")
	tunnelResp, err := http.DefaultClient.Do(tunnelReq)
	if err != nil {
		t.Fatal(err)
	}
	tunnelResp.Body.Close()
	if tunnelResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for tunnel always, got %d", tunnelResp.StatusCode)
	}

	// Tunnel reply="once" succeeds, unblocks both deduplicated waiters, and records source="tunnel".
	onceBody, _ := json.Marshal(map[string]string{"reply": "once"})
	onceReq, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/hoxi/permission/"+id+"/reply", bytes.NewReader(onceBody))
	onceReq.Header.Set("Content-Type", "application/json")
	onceReq.Header.Set("X-Hoxi-Request-Source", "tunnel")
	onceResp, err := http.DefaultClient.Do(onceReq)
	if err != nil {
		t.Fatal(err)
	}
	onceResp.Body.Close()
	if onceResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for tunnel once, got %d", onceResp.StatusCode)
	}
	if d1, d2 := <-res1, <-res2; d1.Decision != "auto_approve" || d2.Decision != "auto_approve" {
		t.Fatalf("deduplicated waiters did not both resolve: %+v / %+v", d1, d2)
	}
	entries := f.svc.audit.list("ses_agy_gate", 10)
	if len(entries) != 1 || entries[0].Source != "tunnel" || entries[0].Decision != "once" {
		t.Fatalf("expected single tunnel once audit entry, got %+v", entries)
	}
}
