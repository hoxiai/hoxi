package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const claudeResearchReferenceDir = "/Users/hugh/.claude/skills/synced/5367d089-917b-42e9-863e-145e292ec06f_5f1be84d-c28f-401e9113-c96b91384f53/deep-research/references"

func TestClaudeSkillDocumentsReadWithoutApproval(t *testing.T) {
	for _, item := range []map[string]interface{}{
		req("external_directory", claudeResearchReferenceDir+"/*"),
		req("external_directory", claudeResearchReferenceDir+"/**"),
		req("read", claudeResearchReferenceDir+"/guide.md"),
		req("read", "/Users/u/.claude/skills/deep-research/SKILL.md"),
		req("read", "/Users/u/.claude/skills/demo/scripts/run.sh"),
		req("read", "/Users/u/.claude/skills/deep-research/references/.env.example"),
		{"permission": "read", "patterns": []interface{}{claudeResearchReferenceDir + "/guide.md"}, "metadata": map[string]interface{}{"externalDirectory": true}},
		{"permission": "tool", "patterns": []interface{}{"read_file"}, "metadata": map[string]interface{}{"tool": "read_file", "filepath": claudeResearchReferenceDir + "/guide.md", "externalDirectory": true}},
		{"permission": "tool", "patterns": []interface{}{"list_dir"}, "metadata": map[string]interface{}{"tool": "list_dir", "filepath": claudeResearchReferenceDir, "externalDirectory": true}},
		{"permission": "tool", "patterns": []interface{}{"grep_search"}, "metadata": map[string]interface{}{"tool": "grep_search", "filepath": claudeResearchReferenceDir, "externalDirectory": true}},
	} {
		if isDestructivePermissionRequest(item) {
			t.Errorf("skill document read was marked sensitive: %#v", item)
		}
		for _, mode := range []string{ApprovalModeAuto, ApprovalModeSmart, ApprovalModeTrusted} {
			if !approvalAutoApproves(item, ApprovalPolicy{Mode: mode}, nil, t.TempDir()) {
				t.Errorf("skill document read asked in %s: %#v", mode, item)
			}
		}
		if cat := unattendedHardDeny(item, t.TempDir()); cat != "" {
			t.Errorf("read-only skill request was hard-denied: %s, %#v", cat, item)
		}
	}
}

func TestClaudeSkillReadExemptionRejectsSensitiveAndNonReadRequests(t *testing.T) {
	for _, p := range []string{
		"/Users/u/.claude/.credentials.json", "/Users/u/.claude/settings.json", "/Users/u/.claude/hooks/pre-tool.sh",
		"/Users/u/.claude/skills-other/references/guide.md", "/Users/u/.claude/skills/../.credentials.json",
		"/Users/u/.claude/skills/demo/references/../../../../.credentials.json",
		"/Users/u/.claude/skills/demo/references/.env", "/Users/u/.claude/skills/demo/references/private.pem",
		"/Users/u/.claude/skills/demo/references/credentials.json", "/Users/u/.claude/skills/demo/references/oauth_creds.json",
		"/Users/u/.claude/skills/demo/references/.ssh/key.txt", "/Users/u/.claude/skills/demo/references/.git/config",
		"/Users/u/.claude/skills/demo/references/*", "/Users/u/.claude/skills/demo/references/*.md",
		"/Users/u/.ssh/.claude/skills/demo/references/guide.md", "/etc/.claude/skills/demo/references/guide.md",
		"/Users/u/.ssh/.hoxi/worktrees/ses_1/.claude/skills/demo/references/guide.md",
	} {
		item := req("read", p)
		if !isDestructivePermissionRequest(item) || approvalAutoApproves(item, ApprovalPolicy{Mode: ApprovalModeAuto}, nil, t.TempDir()) {
			t.Errorf("sensitive or unresolved skill read passed: %s", p)
		}
	}
	for _, item := range []map[string]interface{}{
		req("edit", claudeResearchReferenceDir+"/guide.md"),
		req("write", claudeResearchReferenceDir+"/guide.md"),
		req("bash", "/Users/u/.claude/skills/demo/scripts/run.sh"),
		req("bash", "node /Users/u/.claude/skills/demo/scripts/run.js"),
		req("bash", "node --require=/Users/u/.claude/skills/demo/scripts/run.js app.js"),
		req("bash", "/tmp/cat /Users/u/.claude/skills/demo/references/guide.md"),
		{"permission": "external_directory", "patterns": []interface{}{claudeResearchReferenceDir + "/*"}, "metadata": map[string]interface{}{"tool": "Write"}},
		{"permission": "external_directory", "patterns": []interface{}{claudeResearchReferenceDir + "/*"}, "metadata": map[string]interface{}{"tool": "unknown_tool"}},
		{"permission": "external_directory", "patterns": []interface{}{claudeResearchReferenceDir + "/*"}, "metadata": map[string]interface{}{"command": "cat guide.md"}},
		{"permission": "external_directory", "patterns": []interface{}{claudeResearchReferenceDir + "/*"}, "metadata": map[string]interface{}{"destructive": true}},
		{"permission": "external_directory", "patterns": []interface{}{claudeResearchReferenceDir + "/*"}, "always": []interface{}{"/Users/hugh/.claude/*"}},
		{"permission": "external_directory", "patterns": []interface{}{claudeResearchReferenceDir + "/*", "/Users/hugh/.claude/.credentials.json"}},
	} {
		if !isDestructivePermissionRequest(item) {
			t.Errorf("non-read/mixed request used skill exemption: %#v", item)
		}
		for _, mode := range []string{ApprovalModeAuto, ApprovalModeSmart, ApprovalModeTrusted} {
			if approvalAutoApproves(item, ApprovalPolicy{Mode: mode}, map[string]interface{}{"edit": "allow", "bash": "allow"}, t.TempDir()) {
				t.Errorf("non-read/mixed request passed in %s: %#v", mode, item)
			}
		}
	}
}

func TestClaudeSkillReadChecksResolvedTargets(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, ".claude", "skills")
	refs := filepath.Join(root, "demo", "references")
	secretDir := filepath.Join(t.TempDir(), ".ssh")
	for _, dir := range []string{refs, secretDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	guide := filepath.Join(refs, "guide.md")
	credential := filepath.Join(refs, "private.key")
	for _, file := range []string{guide, credential} {
		if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, target string
		allow        bool
	}{
		{"ordinary.md", guide, true},
		{"disguised.md", credential, false},
		{"spaced.md ", credential, false},
		{`references\alias.md`, credential, false},
		{"escape", t.TempDir(), false},
		{"secret", secretDir, false},
	} {
		if filepath.Separator == '\\' && strings.Contains(tc.name, "\\") {
			continue // this case is a literal POSIX filename, not a Windows separator
		}
		link := filepath.Join(refs, tc.name)
		if err := os.Symlink(tc.target, link); err != nil {
			t.Fatal(err)
		}
		for _, permission := range []string{"read", "external_directory"} {
			path := link
			if permission == "external_directory" && (tc.name == "escape" || tc.name == "secret") {
				path += "/*"
			}
			got := approvalAutoApproves(req(permission, path), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, base)
			if got != tc.allow {
				t.Errorf("%s %s: allowed=%v, want %v", permission, path, got, tc.allow)
			}
		}
		command := "cat " + shellQuote(link)
		if isDestructiveCommand(command) == tc.allow {
			t.Errorf("shell read did not check resolved skill target: %s", command)
		}
	}
}

func TestClaudeSkillReadChecksInstalledRootLinks(t *testing.T) {
	for _, secret := range []bool{false, true} {
		home := t.TempDir()
		installed := filepath.Join(home, ".claude", "skills")
		actual := t.TempDir()
		if secret {
			actual = filepath.Join(actual, ".ssh")
		}
		if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(actual, "demo", "references"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(actual, installed); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{filepath.Join(installed, "demo", "references", "guide.md")} {
			if got := isClaudeSkillReadPath(p, false); got == secret {
				t.Errorf("root link secret=%v: %s, eligible=%v", secret, p, got)
			}
		}
	}
	if isClaudeSkillReadPath("~/.claude/skills/demo/references/guide.md", false) {
		t.Fatal("home shorthand must not be interpreted identically by all tools/interpreters")
	}
}

func TestClaudeHookReadsSkillDocumentsButStillAsksForSkillWrites(t *testing.T) {
	f := newClaudeGateFixture(t)
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	payload := func(tool string) []byte {
		raw, err := json.Marshal(map[string]interface{}{
			"session_id": "cc-uuid-1", "cwd": f.dir, "permission_mode": "acceptEdits",
			"hook_event_name": "PreToolUse", "tool_name": tool, "tool_use_id": "toolu_skill_read",
			"tool_input": map[string]interface{}{"file_path": claudeResearchReferenceDir + "/guide.md"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if got := f.decide(t, payload("Read")); got != "allow|" {
		t.Fatalf("skill Read raised an approval: %q", got)
	}
	events, unsubscribe := f.svc.Subscribe("ses_cc_gate")
	defer unsubscribe()
	result := f.asyncDecide(t, payload("Write"))
	asked := waitForEvent(t, events, "permission.asked")
	id, _ := asked.Properties["id"].(string)
	if code := f.reply(t, id, "reject", ""); code != 200 {
		t.Fatalf("reject status: %d", code)
	}
	if got := <-result; !strings.HasPrefix(got, "deny|") {
		t.Fatalf("skill Write bypassed approval: %q", got)
	}
}
