package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDestructiveRequestsMatchSharedFixture(t *testing.T) {
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
	worktree := t.TempDir()
	smart := ApprovalPolicy{Mode: ApprovalModeSmart}
	if !approvalAutoApproves(req("bash", "cd "+worktree+" && git status"), smart, nil, worktree) {
		t.Error("cd into the worktree followed by a read-only command is routine")
	}
	if approvalAutoApproves(req("bash", "cd /tmp && git status"), smart, nil, worktree) {
		t.Error("cd outside the worktree must ask in smart mode")
	}
}
