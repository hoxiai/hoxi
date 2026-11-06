package runner

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/statusbar"
)

// Claude Code's Monitor runs its command in a background shell. It used to
// take the generic tool gate, which allowed `sudo` or a force push in auto and
// full trust without asking, asked for `ls` in smart mode and refused every
// call while unattended. It now takes the Bash command policy.
func TestClaudeHookMonitorUsesCommandPolicy(t *testing.T) {
	t.Parallel()
	f := newClaudeGateFixture(t)
	if d := f.decide(t, f.payload("Monitor", "tail -n 20 app.log", "", "cc-uuid-1")); d != "allow|" {
		t.Fatalf("smart mode, read-only Monitor command: %q", d)
	}
	for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
		setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: mode})
		for _, cmd := range []string{"sudo rm -rf /", "git push --force origin main", "cat .env"} {
			events, unsub := f.svc.Subscribe("ses_cc_gate")
			result := f.asyncDecide(t, f.payload("Monitor", cmd, "", "cc-uuid-1"))
			asked := waitForEvent(t, events, "permission.asked")
			unsub()
			meta, _ := asked.Properties["metadata"].(map[string]interface{})
			if asked.Properties["permission"] != "bash" || meta["command"] != cmd {
				t.Fatalf("%s: Monitor %q asked as %#v", mode, cmd, asked.Properties)
			}
			id, _ := asked.Properties["id"].(string)
			if code := f.reply(t, id, "reject", ""); code != http.StatusOK {
				t.Fatalf("reply status %d", code)
			}
			if d := <-result; !strings.HasPrefix(d, "deny|") {
				t.Fatalf("%s: rejected Monitor %q decided %q", mode, cmd, d)
			}
		}
	}
}

// newUnattendedHookService returns a service in unattended mode with one
// isolated, command-sandboxed session per runtime.
func newUnattendedHookService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	worktree := filepath.Join(root, ".hoxi", "worktrees", "ses_1")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(root, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeUnattended})
	for id, runtime := range map[string]string{"ses_un_cc": "claude-code", "ses_un_gm": "gemini-cli"} {
		if err := svc.store.Put(SessionRecord{ID: id, Runtime: runtime, ProjectDir: root, WorktreePath: worktree, CommandSandbox: true}); err != nil {
			t.Fatal(err)
		}
	}
	return svc, worktree
}

func TestUnattendedMonitorAndGeminiTodos(t *testing.T) {
	t.Parallel()
	svc, worktree := newUnattendedHookService(t)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	claude := func(tool, command string) (string, string) {
		var p claudeHookPayload
		p.ToolName, p.Cwd, p.ToolUseID = tool, worktree, "toolu_"+randomHex(4)
		p.ToolInput.Command = command
		p.rawInput = marshalJSON(map[string]interface{}{"command": command}, false)
		return svc.decideClaudeHookCall(r, hookCredential{runtime: "claude-code", sessionID: "ses_un_cc", messageID: "msg_1"}, p)
	}
	if d, reason := claude("Monitor", "go test ./... 2>&1 | tail -20"); d != "allow" {
		t.Fatalf("unattended Monitor of a plain command: %q %q", d, reason)
	}
	if d, reason := claude("Monitor", "git push origin main"); d != "deny" || !strings.Contains(reason, "push/publish") {
		t.Fatalf("unattended Monitor push must be hard-denied: %q %q", d, reason)
	}
	gemini := func(tool string, input string) (string, string) {
		p := geminiHookPayload{ToolName: tool, Cwd: worktree, ToolUseID: "g_" + randomHex(4), ToolInput: []byte(input)}
		return svc.decideGeminiHookCall(r, hookCredential{runtime: "gemini-cli", sessionID: "ses_un_gm", messageID: "msg_1"}, p)
	}
	if d, reason := gemini("write_todos", `{"todos":[{"description":"x","status":"pending"}]}`); d != "allow" {
		t.Fatalf("Gemini write_todos is a todo list, not an unsupported tool: %q %q", d, reason)
	}
	// Only the todo list became a control tool; other unknown tools keep the gate.
	if d, _ := gemini("save_memory", `{"fact":"x"}`); d != "deny" {
		t.Fatalf("unattended save_memory must stay refused, got %q", d)
	}
}
