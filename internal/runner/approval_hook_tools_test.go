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

// "Always" on a command full trust can reduce is kept, so the identical
// command no longer prompts. An opaque or destructive command still asks every
// time: the card hides the button for it (sessionGrantRefusal in
// opaque-command.js) and the gate never stores the grant.
func TestSessionGrantSticksOnlyWhenTheGateKeepsIt(t *testing.T) {
	t.Parallel()
	f := newClaudeGateFixture(t)
	askAndAnswer := func(cmd, reply string) {
		t.Helper()
		events, unsub := f.svc.Subscribe("ses_cc_gate")
		defer unsub()
		result := f.asyncDecide(t, f.payload("Bash", cmd, "", "cc-uuid-1"))
		asked := waitForEvent(t, events, "permission.asked")
		id, _ := asked.Properties["id"].(string)
		if code := f.reply(t, id, reply, ""); code != http.StatusOK {
			t.Fatalf("reply status %d", code)
		}
		if d := <-result; d != "allow|" {
			t.Fatalf("%q answered %s decided %q", cmd, reply, d)
		}
	}
	kept := "python3 -c 'import os; os.mkdir(0)'"
	askAndAnswer(kept, "always")
	if d := f.decide(t, f.payload("Bash", kept, "", "cc-uuid-1")); d != "allow|" {
		t.Fatalf("granted command asked again: %q", d)
	}
	for _, cmd := range []string{`bash -c "$X"`, "env | grep -i token", "rm -rf build"} {
		if refusal := sessionGrantRefusal(cmd); refusal == "" {
			t.Fatalf("%q must not get a session grant", cmd)
		}
		askAndAnswer(cmd, "always")
		askAndAnswer(cmd, "once") // asked again: the grant was not stored
	}
}

// A grant from another mode never outranks the unattended hard-refuse classes.
func TestUnattendedHardDenyOutranksSessionGrant(t *testing.T) {
	t.Parallel()
	svc, worktree := newUnattendedHookService(t)
	svc.gate.addAlways("ses_un_cc", "git push origin main", "desktop")
	if !svc.gate.hasAlways("ses_un_cc", "git push origin main") {
		t.Fatal("fixture: a plain push is grantable outside unattended mode")
	}
	var p claudeHookPayload
	p.ToolName, p.Cwd, p.ToolUseID = "Bash", worktree, "toolu_push"
	p.ToolInput.Command = "git push origin main"
	p.rawInput = marshalJSON(map[string]interface{}{"command": p.ToolInput.Command}, false)
	d, reason := svc.decideClaudeHookCall(httptest.NewRequest(http.MethodPost, "/", nil), hookCredential{runtime: "claude-code", sessionID: "ses_un_cc", messageID: "msg_1"}, p)
	if d != "deny" || !strings.Contains(reason, "push/publish") {
		t.Fatalf("granted push while unattended: %q %q", d, reason)
	}
}

// OpenCode remembers "always" itself, so Hoxi answers it once for a command
// the gate would never grant for the session, and passes it through otherwise.
func TestOpenCodeAlwaysOnlyForCommandsTheGateWouldGrant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	svc := NewService(dir, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	fake := &fakeOpenCodePermissions{replies: map[string]string{}}
	upstream := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer upstream.Close()
	svc.ConfigureOpenCode(upstream.URL, "secret")
	if err := svc.store.Put(SessionRecord{ID: "ses_oc_always", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-always"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rec, _ := svc.store.Get("ses_oc_always")
	events, unsub := svc.Subscribe("ses_oc_always")
	defer unsub()
	for i, c := range []struct{ command, want string }{
		{`bash -c "$X"`, "once"},
		{"env | grep -i token", "once"},
		{"rm -rf dist", "once"},
		{"python3 -c 'import os; os.mkdir(0)'", "always"},
	} {
		id := "per_oc_always" + string(rune('a'+i))
		event, _ := json.Marshal(map[string]interface{}{"type": "permission.asked", "properties": map[string]interface{}{
			"id": id, "sessionID": "oc-always", "permission": "bash", "patterns": []string{c.command},
			"metadata": map[string]interface{}{"command": c.command},
		}})
		svc.relayOpenCodeEvent(rec, event)
		if asked := waitForEvent(t, events, "permission.asked"); asked.Properties["id"] != id {
			t.Fatalf("%q: asked %#v", c.command, asked.Properties)
		}
		body, _ := json.Marshal(map[string]string{"reply": "always"})
		resp, err := http.Post(srv.URL+"/api/hoxi/permission/"+id+"/reply?directory="+dir, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%q: reply status %d", c.command, resp.StatusCode)
		}
		if got := fake.reply(id); got != c.want {
			t.Fatalf("%q: OpenCode got %q, want %q", c.command, got, c.want)
		}
	}
}

func TestAgyHookDeniesForeignBrainPathsAndAllowsOwnBrain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GEMINI_HOME", filepath.Join(home, ".gemini"))
	dir := t.TempDir()
	svc := NewService(dir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeSmart})
	if err := svc.store.Put(SessionRecord{ID: "ses_agy_brain", Runtime: "antigravity", ProjectDir: dir, WorktreePath: dir, ExternalID: "conv-1"}); err != nil {
		t.Fatal(err)
	}

	ownBrainFile := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "conv-1", ".system_generated", "logs", "transcript.jsonl")
	foreignBrainFile := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "conv-old-split", ".system_generated", "logs", "transcript_full.jsonl")
	if err := os.MkdirAll(filepath.Dir(ownBrainFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(foreignBrainFile), 0o755); err != nil {
		t.Fatal(err)
	}

	callRead := func(path string) (string, string) {
		var p hookPayload
		p.ConversationID = "conv-1"
		p.StepIdx = 3
		p.WorkspacePaths = []string{dir}
		p.ToolCall.Name = "read_file"
		p.ToolCall.Args = map[string]interface{}{"Path": path}
		return svc.decideHookCall(httptest.NewRequest(http.MethodPost, "/", nil), hookCredential{runtime: string(RuntimeAntigravity), sessionID: "ses_agy_brain", messageID: "msg_a_1"}, p)
	}

	for _, mode := range []string{ApprovalModeSmart, ApprovalModeManual} {
		setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: mode})
		if d, reason := callRead(ownBrainFile); d != "auto_approve" {
			t.Fatalf("%s: expected own conversation brain read to be approved, got %q (%q)", mode, d, reason)
		}
		if d, reason := callRead(foreignBrainFile); d != "deny" || !strings.Contains(reason, "conv-old-split") {
			t.Fatalf("%s: expected foreign conversation brain read to be denied by hook so agy does not abort the turn, got %q (%q)", mode, d, reason)
		}
	}
}

func TestAgyTranslatorDistinguishesGeminiPathDenialFromMissingHook(t *testing.T) {
	t.Parallel()
	tr := NewTurnTranslator("ses_agy_gempath", string(RuntimeAntigravity), "msg_a_gp", "msg_u_gp", "antigravity", "claude-sonnet-4-6", "build", "")
	lines := []string{
		`{"conversation_id":"c1","event":"step_update","step_update":{"conversation_id":"c1","duration_seconds":0,"state":"ERROR","step_index":2,"step_type":"tool","tool_info":{"error":{"message":"permission check failed for read_file \"/Users/dev/.gemini/antigravity-cli/brain/other/.system_generated/logs/transcript_full.jsonl\": user denied permission","type":"PermissionDenied"},"name":"read_file","output":"","parameters":{"Path":"/Users/dev/.gemini/antigravity-cli/brain/other/.system_generated/logs/transcript_full.jsonl"}},"tool_name":"read_file"}}`,
		`{"conversation_id":"c1","event":"result","result":{"conversation_id":"c1","denied_actions":[{"action":"read_file","display_name":"ReadFile"}],"duration_seconds":1.2,"error":"","num_turns":1,"response":"","status":"DONE"}}`,
	}
	var evts []SSEEvent
	for _, l := range lines {
		evts = append(evts, tr.FeedLine(l)...)
	}
	var deniedText string
	for _, evt := range evts {
		if evt.Type == "message.part.updated" {
			if p, ok := evt.Properties["part"].(MessagePart); ok && p.ID == "prt_msg_a_gp_text_denied" {
				deniedText = p.Text
			}
		}
	}
	if !strings.Contains(deniedText, "built-in ~/.gemini path policy") || strings.Contains(deniedText, "Hoxi's approval hook did not handle it") {
		t.Fatalf("unexpected denied notice: %q", deniedText)
	}
}
