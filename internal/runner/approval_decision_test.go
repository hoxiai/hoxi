package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/testutil/execstub"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestDestructiveRequestsMatchSharedFixture(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".opencode", "lib", "permission-request.cases.json"))
	if err != nil {
		t.Fatalf("read shared fixture: %v", err)
	}
	var cases []struct {
		Name        string                 `json:"name"`
		Request     map[string]interface{} `json:"request"`
		Destructive bool                   `json:"destructive"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse shared fixture: %v", err)
	}
	if len(cases) < 20 {
		t.Fatalf("fixture unexpectedly small: %d cases", len(cases))
	}
	for _, c := range cases {
		if got := isDestructivePermissionRequest(c.Request); got != c.Destructive {
			t.Errorf("%s: destructive = %v, want %v", c.Name, got, c.Destructive)
		}
	}
}

func req(perm string, patterns ...string) map[string]interface{} {
	list := make([]interface{}, len(patterns))
	for i, p := range patterns {
		list[i] = p
	}
	return map[string]interface{}{"permission": perm, "patterns": list}
}

func toolReq(tool string, meta map[string]interface{}) map[string]interface{} {
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta["tool"] = tool
	return map[string]interface{}{"permission": "tool", "patterns": []interface{}{tool}, "metadata": meta}
}

func TestApprovalModesDecideEveryCategory(t *testing.T) {
	t.Parallel()
	auto := ApprovalPolicy{Mode: ApprovalModeAuto}
	smart := ApprovalPolicy{Mode: ApprovalModeSmart}
	manual := ApprovalPolicy{Mode: ApprovalModeManual}
	cases := []struct {
		name                string
		item                map[string]interface{}
		auto, smart, manual bool
	}{
		{"file read", req("read", "src/App.tsx"), true, true, true},
		{"dotenv read asks in every mode", req("read", "/repo/.env"), false, false, false},
		{"edit", req("edit", "src/App.tsx"), true, true, false},
		{"mutating command", req("bash", "mkdir -p dist && npm install"), true, false, false},
		{"read-only command", req("bash", "git status && go test ./..."), true, true, false},
		{"destructive command", req("bash", "rm -rf dist"), false, false, false},
		{"force push", req("bash", "git push origin main --force"), false, false, false},
		{"branch deletion", req("bash", "git branch -D feature"), false, false, false},
		{"webfetch", req("webfetch", "https://example.com"), true, true, false},
		{"routine external directory", req("external_directory", "/Users/u/code/sibling/*"), true, true, false},
		{"credential external directory", req("external_directory", "/Users/u/.ssh/config"), false, false, false},
		{"todo control tool", req("todowrite"), true, true, false},
		{"claude edit tool", toolReq("Write", map[string]interface{}{"args": `{"file_path":"src/a.ts"}`}), true, true, false},
		{"claude edit without path", toolReq("Write", nil), false, false, false},
		{"agy web tool", toolReq("read_url_content", map[string]interface{}{"args": `{"Url":"https://example.com"}`}), true, true, false},
		{"outside read", toolReq("Read", map[string]interface{}{"externalDirectory": true, "args": `{"file_path":"/Users/u/code/sibling/a.ts"}`}), true, true, false},
		{"mcp tool", toolReq("call_mcp_tool", nil), true, false, false},
		{"unknown tool", toolReq("mystery_action", nil), true, false, false},
		{"protected edit", req("edit", ".git/config"), false, false, false},
	}
	for _, c := range cases {
		for _, m := range []struct {
			pol  ApprovalPolicy
			want bool
		}{{auto, c.auto}, {smart, c.smart}, {manual, c.manual}} {
			if got := approvalAutoApproves(c.item, m.pol, nil, ""); got != m.want {
				t.Errorf("%s in %s mode: got %v, want %v", c.name, m.pol.Mode, got, m.want)
			}
		}
	}
}

func TestApprovalOverridesReplaceTheModePreset(t *testing.T) {
	t.Parallel()
	pol := ApprovalPolicy{Mode: ApprovalModeManual, Edit: "allow"}
	if !approvalAutoApproves(req("edit", "src/a.ts"), pol, nil, "") {
		t.Error("edit=allow must apply in manual mode")
	}
	if approvalAutoApproves(req("bash", "ls"), pol, nil, "") {
		t.Error("manual mode keeps shell at ask when only edit is overridden")
	}
	pol = ApprovalPolicy{Mode: ApprovalModeAuto, Bash: "ask", Webfetch: "ask"}
	if approvalAutoApproves(req("bash", "ls"), pol, nil, "") || approvalAutoApproves(req("webfetch", "https://x.dev"), pol, nil, "") {
		t.Error("ask overrides must apply in auto mode")
	}
	if !approvalAutoApproves(req("edit", "src/a.ts"), pol, nil, "") {
		t.Error("auto mode edit must stay allowed")
	}
	pol = ApprovalPolicy{Mode: ApprovalModeAuto, Bash: "smart"}
	if approvalAutoApproves(req("bash", "npm install lodash"), pol, nil, "") || !approvalAutoApproves(req("bash", "git diff"), pol, nil, "") {
		t.Error("bash=smart must only allow read-only commands")
	}
}

func TestPermissionRuleObjectsOnlyDecideTheirOwnTargets(t *testing.T) {
	t.Parallel()
	auto := ApprovalPolicy{Mode: ApprovalModeAuto}
	smart := ApprovalPolicy{Mode: ApprovalModeSmart}
	manual := ApprovalPolicy{Mode: ApprovalModeManual}
	rules := map[string]interface{}{
		"bash":     map[string]interface{}{"npm install *": "allow", "docker *": "ask", "*": "ask"},
		"webfetch": map[string]interface{}{"https://docs.hoxi.ai/*": "allow", "*": "ask"},
		"edit":     "deny",
	}
	// One "always allow" pattern (with normalizeApprovalPermissions' "*":"ask"
	// fallback) must not turn every other command into a prompt.
	if !approvalAutoApproves(req("bash", "npm test"), auto, rules, "") {
		t.Error("an unmatched command must follow auto mode even when * is ask")
	}
	if !approvalAutoApproves(req("bash", "npm install lodash"), smart, rules, "") {
		t.Error("an allow pattern must let its command run in smart mode")
	}
	if !approvalAutoApproves(req("bash", "npm install lodash\necho done"), smart, rules, "") {
		t.Error("an allow pattern with * must also match multi-line commands")
	}
	if approvalAutoApproves(req("bash", "docker build ."), auto, rules, "") {
		t.Error("an ask pattern must ask in auto mode")
	}
	if approvalAutoApproves(req("bash", "npm install x && rm -rf /"), auto, rules, "") {
		t.Error("an allow pattern never covers a destructive command")
	}
	if !approvalAutoApproves(req("webfetch", "https://docs.hoxi.ai/guide"), manual, rules, "") {
		t.Error("allowed domain must run even in manual mode")
	}
	if approvalAutoApproves(req("webfetch", "https://untrusted.example.com/page"), manual, rules, "") {
		t.Error("unmatched domain must follow manual mode and ask")
	}
	if approvalAutoApproves(req("webfetch", "https://untrusted.example.com/page"), auto, map[string]interface{}{
		"webfetch": map[string]interface{}{"https://docs.hoxi.ai/*": "allow", "*": "deny"},
	}, "") {
		t.Error("a * deny fallback must block other domains in auto mode")
	}
	if approvalAutoApproves(req("edit", "src/a.ts"), auto, rules, "") {
		t.Error("edit=deny must never auto-approve")
	}
	// Hoxi's own "ask" enforcement strings are not user choices.
	if !approvalAutoApproves(req("edit", "src/a.ts"), auto, map[string]interface{}{"edit": "ask"}, "") {
		t.Error(`edit:"ask" in the OpenCode config must not override auto mode`)
	}
}

func TestSmartModeDropsLeadingWorktreeCD(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	smart := ApprovalPolicy{Mode: ApprovalModeSmart}
	if !approvalAutoApproves(req("bash", "cd "+worktree+" && git status"), smart, nil, worktree) {
		t.Error("cd into the worktree followed by a read-only command is routine")
	}
	if approvalAutoApproves(req("bash", "cd /tmp && git status"), smart, nil, worktree) {
		t.Error("cd outside the worktree must ask in smart mode")
	}
	taggedWrapped := "# hoxi-stop:ses_1:call_1\ntrap : EXIT\n'/usr/bin/sandbox-exec' -f '/tmp/p.sb' -- /bin/sh -c 'git status'"
	if !approvalAutoApproves(req("bash", taggedWrapped), smart, nil, worktree) {
		t.Error("sandbox-wrapped safe command with stop marker must auto-approve in smart mode")
	}
	item := map[string]interface{}{
		"permission": "bash",
		"patterns":   []interface{}{"# hoxi-stop:ses_1:call_1\ntrap : EXIT\nnpm install lodash"},
		"metadata":   map[string]interface{}{"command": "# hoxi-stop:ses_1:call_1\ntrap : EXIT\nnpm install lodash"},
	}
	sanitizeOpenCodePermissionItem(item)
	if got := stringList(item["patterns"]); len(got) != 1 || got[0] != "npm install lodash" {
		t.Errorf("sanitized patterns = %v, want [npm install lodash]", got)
	}
	if meta, _ := item["metadata"].(map[string]interface{}); meta["command"] != "npm install lodash" {
		t.Errorf("sanitized metadata.command = %v, want npm install lodash", meta["command"])
	}
}

func TestOpaqueShellCommandsRequireApprovalInAutoAndTrusted(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
		for _, command := range []string{
			`P=.env; cat $P`,
			`cat "$HOXI_AUDIT_PATH"`,
			`printf 'echo harmless' | sh`,
			`bash -c 'echo harmless'`,
			`python3 -c 'import pathlib; print(pathlib.Path(".env").read_text())'`,
		} {
			item := req("bash", command)
			if approvalAutoApproves(item, ApprovalPolicy{Mode: mode}, nil, worktree) {
				t.Errorf("opaque shell %q was auto-approved in %s mode", command, mode)
			}
		}
	}
	// Inline interpreter scripts ask in auto mode; full trust lets them run,
	// as it already did `python3 script.py`.
	for _, command := range []string{`node -e 'console.log(1)'`, "python3 - <<'EOF'\nprint(1)\nEOF"} {
		if approvalAutoApproves(req("bash", command), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, worktree) {
			t.Errorf("inline script %q was auto-approved in auto mode", command)
		}
		if !approvalAutoApproves(req("bash", command), ApprovalPolicy{Mode: ApprovalModeTrusted}, nil, worktree) {
			t.Errorf("inline script %q asked in trusted mode", command)
		}
	}
}

func TestUngatedRuntimesRefuseNonReadOnlyTurnsWhenApprovalGateRequired(t *testing.T) {
	binDir := t.TempDir()
	markerFile := filepath.Join(t.TempDir(), "ran.txt")
	execstub.Write(t, binDir, "qodercli", "#!/bin/sh\necho ran > "+markerFile+"\nexit 0\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	repo := initTestGitRepo(t)
	storeDir := filepath.Join(t.TempDir(), "sessions")
	svc := NewService(repo, worktree.NewManager(repo), nil, storeDir)
	defer svc.CloseAll()

	if _, err := svc.policy.set(ApprovalPolicy{Mode: ApprovalModeManual}); err != nil {
		t.Fatal(err)
	}
	rec := SessionRecord{
		ID:           "ses_qoder_manual",
		Runtime:      string(RuntimeQoder),
		ProjectDir:   repo,
		WorktreePath: repo,
		SandboxMode:  "local",
		ModelID:      "auto",
		Mode:         "build",
	}
	if err := svc.Store().Put(rec); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	httpReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/ses_qoder_manual/prompt", strings.NewReader(`{"text":"edit main.go"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httpReq)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for ungated Qoder turn under manual policy, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Fatalf("ungated Qoder binary must not be executed under manual approval policy")
	}
}

// Qoder and CodeBuddy have no PreToolUse gate, so the mandatory approval floor
// (secrets, destructive commands, opaque execution) cannot be honored in any
// non-read-only mode, including auto and trusted.
func TestUngatedRuntimesRefusedInAutoAndTrusted(t *testing.T) {
	for _, runtime := range []string{string(RuntimeQoder), string(RuntimeCodeBuddy)} {
		for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
			t.Run(runtime+"/"+mode, func(t *testing.T) {
				repo := initTestGitRepo(t)
				svc := NewService(repo, worktree.NewManager(repo), nil, filepath.Join(t.TempDir(), "sessions"))
				defer svc.CloseAll()
				if _, err := svc.policy.set(ApprovalPolicy{Mode: mode}); err != nil {
					t.Fatal(err)
				}
				rec := SessionRecord{ID: "ses_ungated_" + mode, Runtime: runtime, ProjectDir: repo, WorktreePath: repo, SandboxMode: "local", Mode: "build", ModelID: "auto"}
				if err := svc.Store().Put(rec); err != nil {
					t.Fatal(err)
				}
				bin := execstub.Write(t, t.TempDir(), "auditcli", "#!/bin/sh\ncat >/dev/null\nexit 0\n")
				err := svc.startTurnProcess(rec, bin, "build", "auto", "", "audit", MessageInfo{ID: "msg_user", SessionID: rec.ID, Role: "user"}, nil, nil, "msg_asst")
				if err == nil {
					t.Fatalf("started ungated %s under %s policy despite the mandatory approval floor", runtime, mode)
				}
			})
		}
	}
}

// Interpreter checks must cover version-suffixed binaries and awk programs that
// call system() or pipe to a command.
func TestOpaqueInterpreterVariantsAsk(t *testing.T) {
	if !approvalAutoApproves(req("bash", `python3.12 -c 'print(1)'`), ApprovalPolicy{Mode: ApprovalModeTrusted}, nil, "") {
		t.Error("full trust lets version-suffixed inline interpreters run")
	}
	for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
		commands := []string{`awk 'BEGIN {system("id")}'`, `awk 'BEGIN{print "id" | "sh"}'`}
		if mode == ApprovalModeAuto {
			commands = append(commands, `python3.12 -c 'print(1)'`)
		}
		for _, command := range commands {
			t.Run(mode+"/"+command, func(t *testing.T) {
				item := map[string]interface{}{"permission": "bash", "patterns": []string{command}}
				if approvalAutoApproves(item, ApprovalPolicy{Mode: mode}, nil, "") {
					t.Fatalf("automatically approved inline interpreter command in %s", mode)
				}
			})
		}
	}
}
