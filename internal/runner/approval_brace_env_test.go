package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End to end through the Claude Code PreToolUse hook a session is launched
// with: the brace grep runs without a prompt in auto mode, a brace trick
// still asks, and an unattended sandboxed worktree refuses a nested .env.
func TestClaudeHookBraceGrepAndNestedDotEnv(t *testing.T) {
	f := newClaudeGateFixture(t)
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	grep := `grep -rn --exclude-dir={node_modules,dist,.git} "Notification" admin/ scripts/`
	if d := f.decide(t, f.payload("Bash", grep, "", "cc-uuid-1")); d != "allow|" {
		t.Fatalf("auto mode, brace grep: %q", d)
	}

	events, unsub := f.svc.Subscribe("ses_cc_gate")
	defer unsub()
	result := f.asyncDecide(t, f.payload("Bash", "cat {.e,}nv", "", "cc-uuid-1"))
	asked := waitForEvent(t, events, "permission.asked")
	id, _ := asked.Properties["id"].(string)
	if code := f.reply(t, id, "reject", ""); code != 200 {
		t.Fatalf("reject status %d", code)
	}
	if d := <-result; !strings.HasPrefix(d, "deny|") {
		t.Fatalf("auto mode, cat {.e,}nv after reject: %q", d)
	}

	worktree := filepath.Join(f.dir, ".hoxi", "worktrees", "ses")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.store.Put(SessionRecord{ID: "ses_cc_gate", Runtime: "claude-code", ProjectDir: f.dir,
		WorktreePath: worktree, ExternalID: "cc-uuid-1", CommandSandbox: true}); err != nil {
		t.Fatal(err)
	}
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeUnattended})
	if d := f.decide(t, f.payload("Bash", "cat apay/.env", worktree, "cc-uuid-1")); !strings.HasPrefix(d, "deny|") {
		t.Fatalf("unattended, cat apay/.env: %q", d)
	}
	if d := f.decide(t, f.payload("Bash", "cat apay/.env.example", worktree, "cc-uuid-1")); d != "allow|" {
		t.Fatalf("unattended, cat apay/.env.example: %q", d)
	}
}

func bashRequest(command string) map[string]interface{} {
	return map[string]interface{}{
		"permission": "bash",
		"patterns":   []interface{}{command},
		"metadata":   map[string]interface{}{"command": command},
	}
}

// Agents write `--exclude-dir={a,b}` all the time; it used to ask in every
// mode. The brace list is now judged like the repeated flag it expands to,
// while brace tricks that hide a command or a secret still ask.
func TestFilterFlagBraceListRunsInAutoMode(t *testing.T) {
	worktree := "/work/shopark"
	for _, cmd := range []string{
		`grep -rn --exclude-dir={node_modules,dist,.git} "Notification" admin/ scripts/`,
		`grep -rn --exclude="stats.html" --exclude-dir={node_modules,dist,.git,vendor} "notification/list\|notificationListAPI" admin/ scripts/ go-fast/ go-shop/`,
		`grep -rn --include={*.go,*.ts} TODO src`,
	} {
		for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
			if !approvalAutoApproves(bashRequest(cmd), ApprovalPolicy{Mode: mode}, nil, worktree) {
				t.Errorf("%s mode asked for %q", mode, cmd)
			}
		}
	}
	for _, cmd := range []string{
		`{rm,-rf,.}`,
		`cat {.e,}nv`,
		`grep -r KEY --include={.env,a.go} .`,
		`grep -r KEY --include={.envrc,a.go} .`,
		`grep -r x --exclude-dir={a,b}c .`,
		`grep -r x --exclude-dir={../..,b} .`,
		`grep -r x --file={a,b} .`,
		`grep -r x --exclude-dir={a,b} . ; {rm,-rf,.}`,
		`grep -r x --exclude-dir={a,b} . && rm -rf build`,
	} {
		if approvalAutoApproves(bashRequest(cmd), ApprovalPolicy{Mode: ApprovalModeTrusted}, nil, worktree) {
			t.Errorf("trusted mode let %q through", cmd)
		}
	}
}

// Unattended mode skips the generic destructive scan, so its own secret scan
// must see a .env under any directory, as the generic one does.
func TestUnattendedRefusesNestedDotEnvReads(t *testing.T) {
	worktree := filepath.Join(t.TempDir(), ".hoxi", "worktrees", "ses")
	for _, cmd := range []string{
		`cat apay/.env`,
		`head -n 3 config/.env.local`,
		`cat ./plugins/app/one/.env.production`,
		`cat .envrc`,
		`node scripts/seed.js --env-file=apps/web/.env`,
	} {
		req := bashRequest(cmd)
		if approvalAutoApprovesIsolated(req, ApprovalPolicy{Mode: ApprovalModeUnattended}, nil, worktree, true) {
			t.Errorf("unattended mode let %q through", cmd)
		}
		if allow, category := unattendedDecision(req, worktree); allow || category != unattendedCategorySecret {
			t.Errorf("unattendedDecision(%q) = %v/%q, want deny/%q", cmd, allow, category, unattendedCategorySecret)
		}
	}
	if !approvalAutoApprovesIsolated(bashRequest(`cat apay/.env.example`), ApprovalPolicy{Mode: ApprovalModeUnattended}, nil, worktree, true) {
		t.Error("unattended mode refused cat apay/.env.example")
	}
}
