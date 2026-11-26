package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/sandbox"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func newPathGuardTestService(t *testing.T, repo string) *Service {
	t.Helper()
	return NewService(repo, worktree.NewManager(repo), nil, filepath.Join(t.TempDir(), "sessions.json"))
}

func initPathGuardTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, rel := range []string{
		"internal/auth/token.go",
		"internal/auth/deploy.sh",
		"web/src/pages/Home.tsx",
		"web/src/policy/guard.js",
		"docs/readme.md",
		"README.md",
	} {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		content := "package test\n"
		if strings.HasSuffix(rel, ".sh") {
			content = "#!/bin/sh\necho hi\n"
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	run("init", "-q")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestSessionPathScopeCreateAndInheritance(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	repo := initPathGuardTestRepo(t)
	svc := newPathGuardTestService(t, repo)

	// Rejects invalid path scope inputs rather than silently dropping them.
	if _, status, err := svc.CreateSession(CreateSessionRequest{
		Directory:     repo,
		Runtime:       string(RuntimeNative),
		SandboxMode:   "local",
		ReadOnlyPaths: []string{"../outside"},
	}, repo); err == nil || status != http.StatusBadRequest {
		t.Fatalf("expected 400 for traversal readOnlyPaths, got status=%d err=%v", status, err)
	}

	// Merges legacy DeniedPaths alias into ReadOnlyPaths and preserves WritablePaths.
	info, status, err := svc.CreateSession(CreateSessionRequest{
		Directory:     repo,
		Runtime:       string(RuntimeNative),
		SandboxMode:   "local",
		ReadOnlyPaths: []string{"internal/auth/"},
		DeniedPaths:   []string{"web/src/policy"},
		WritablePaths: []string{"./web/src", "docs"},
	}, repo)
	if err != nil || status != http.StatusOK {
		t.Fatalf("CreateSession failed: status=%d err=%v", status, err)
	}
	if strings.Join(info.ReadOnlyPaths, ",") != "internal/auth,web/src/policy" {
		t.Fatalf("unexpected ReadOnlyPaths: %v", info.ReadOnlyPaths)
	}
	if strings.Join(info.WritablePaths, ",") != "web/src,docs" {
		t.Fatalf("unexpected WritablePaths: %v", info.WritablePaths)
	}

	// Subagent inherits parent's ReadOnlyPaths and WritablePaths.
	parent, ok := svc.store.Meta(info.ID)
	if !ok {
		t.Fatal("parent session missing")
	}
	childInfo, created := svc.childSessionFactory(parent, "m", "build")(ChildSessionSpec{Title: "child"})
	if !created {
		t.Fatal("expected child session creation")
	}
	if strings.Join(childInfo.ReadOnlyPaths, ",") != "internal/auth,web/src/policy" ||
		strings.Join(childInfo.WritablePaths, ",") != "web/src,docs" {
		t.Fatalf("child did not inherit scope: ro=%v rw=%v", childInfo.ReadOnlyPaths, childInfo.WritablePaths)
	}

	// Even if parent scope is updated after child creation, effectiveSessionRecord inherits it.
	if err := svc.store.Put(SessionRecord{
		ID:           "ses_child_late",
		ParentID:     info.ID,
		Runtime:      string(RuntimeNative),
		ProjectDir:   repo,
		WorktreePath: repo,
	}); err != nil {
		t.Fatal(err)
	}
	eff, ok := svc.effectiveSessionRecord("ses_child_late")
	if !ok || strings.Join(eff.ReadOnlyPaths, ",") != "internal/auth,web/src/policy" {
		t.Fatalf("expected ancestor scope inheritance, got %+v", eff)
	}
}

func TestNativePermissionHookEnforcesReadOnlyAndWritableScope(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	repo := initPathGuardTestRepo(t)
	// Create a symlink inside web/src pointing into internal/auth to test symlink bypass!
	if err := os.Symlink(filepath.Join(repo, "internal", "auth", "token.go"), filepath.Join(repo, "web", "src", "sneaky.go")); err != nil {
		t.Fatal(err)
	}

	svc := newPathGuardTestService(t, repo)
	if _, err := svc.policy.set(ApprovalPolicy{Mode: ApprovalModeTrusted}); err != nil {
		t.Fatal(err)
	}
	rec := SessionRecord{
		ID:            "ses_scope_native",
		Runtime:       string(RuntimeNative),
		ProjectDir:    repo,
		WorktreePath:  repo,
		SandboxMode:   "local",
		ReadOnlyPaths: []string{"internal/auth", "web/src/policy"},
		WritablePaths: []string{"web/src", "docs"},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	hook := svc.nativePermissionHook(rec, "msg_1")
	mustJSON := func(v any) json.RawMessage {
		raw, _ := json.Marshal(v)
		return raw
	}

	// 1. Reading read-only core files and outside-writable files is ALLOWED.
	for _, target := range []string{"internal/auth/token.go", "web/src/policy/guard.js", "README.md"} {
		if err := hook(context.Background(), NativePermissionRequest{
			SessionID: rec.ID, CallID: "c_read_" + target, ToolName: "read", IsRead: true,
			Input: mustJSON(map[string]any{"filePath": target}),
		}); err != nil {
			t.Fatalf("expected reading %q to be allowed, got: %v", target, err)
		}
	}

	// 2. Writing inside WritablePaths (not excluded by ReadOnlyPaths) is ALLOWED.
	if err := hook(context.Background(), NativePermissionRequest{
		SessionID: rec.ID, CallID: "c_write_ok", ToolName: "edit", IsRead: false,
		Input: mustJSON(map[string]any{"filePath": "web/src/pages/Home.tsx"}),
	}); err != nil {
		t.Fatalf("expected edit inside writable path to succeed, got: %v", err)
	}

	// 3. Writing to ReadOnlyPaths, ReadOnly inside Writable, outside WritablePaths,
	//    symlink escape, apply_patch, and hoxi_image outputPath are ALL hard-denied even in Trusted mode!
	denyCases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"edit read-only", "edit", map[string]any{"filePath": "internal/auth/token.go"}},
		{"write read-only inside writable", "write", map[string]any{"filePath": "web/src/policy/guard.js", "content": "x"}},
		{"edit outside writable allowlist", "edit", map[string]any{"filePath": "README.md"}},
		{"symlink escape from writable to read-only", "write", map[string]any{"filePath": "web/src/sneaky.go", "content": "pwn"}},
		{"apply_patch read-only", "apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Update File: internal/auth/token.go\n@@\n-package test\n+package pwn\n*** End Patch"}},
		{"apply_patch move into read-only", "apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Update File: web/src/pages/Home.tsx\n*** Move to: internal/auth/stolen.tsx\n@@\n*** End Patch"}},
		{"hoxi_image outputPath read-only", "hoxi_image", map[string]any{"prompt": "hi", "outputPath": "internal/auth/out.png"}},
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		denyCases = append(denyCases, struct {
			name string
			tool string
			args map[string]any
		}{"case variation read-only", "edit", map[string]any{"filePath": "Internal/Auth/token.go"}})
	}
	for _, tc := range denyCases {
		if err := hook(context.Background(), NativePermissionRequest{
			SessionID: rec.ID, CallID: "c_" + tc.name, ToolName: tc.tool, IsRead: false,
			Input: mustJSON(tc.args),
		}); err == nil {
			t.Errorf("case %q: expected hard deny, got nil", tc.name)
		}
	}
}

func TestSessionShellPolicyCheckBlocksMutationsExecutionsAndUnsandboxedWrites(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	repo := initPathGuardTestRepo(t)
	recNoSandbox := SessionRecord{
		ID:             "ses_shell_notsb",
		Runtime:        string(RuntimeNative),
		ProjectDir:     repo,
		WorktreePath:   repo,
		CommandSandbox: false,
		ReadOnlyPaths:  []string{"internal/auth"},
		WritablePaths:  []string{"web/src", "docs"},
	}
	recSandboxed := recNoSandbox
	recSandboxed.CommandSandbox = true

	// Pure read-only commands are allowed even without Seatbelt.
	for _, cmd := range []string{
		"cat internal/auth/token.go",
		"rg package internal/auth",
		"ls internal/auth",
		"git status",
		"git diff internal/auth/token.go",
		"git log -n 3 internal/auth",
		"go test ./...",
	} {
		if act, reason := sessionShellPolicyCheck(recNoSandbox, repo, cmd); act != "" {
			t.Errorf("expected read-only cmd %q allowed, got action=%q reason=%q", cmd, act, reason)
		}
	}

	// Explicit mutations, ancestor removals, script executions, obfuscated commands, and broad git ops -> HARD DENY.
	for _, cmd := range []string{
		"echo pwn > internal/auth/token.go",
		"echo pwn >> internal/auth/token.go",
		"rm internal/auth/token.go",
		"rm -rf internal",
		"rm -rf internal/*",
		"mv internal internal_bak",
		"mv web/src/pages/Home.tsx internal/auth/Home.tsx",
		"cp web/src/pages/Home.tsx internal/auth/Home.tsx",
		"chmod 777 internal/auth/token.go",
		"touch internal/auth/new.go",
		"sed -i '' 's/test/pwn/' internal/auth/token.go",
		"cd internal && echo bad > auth/token.go",
		`r\m internal/auth/token.go`,
		"bash -c 'rm internal/auth/token.go'",
		"./internal/auth/deploy.sh",
		"bash internal/auth/deploy.sh",
		"sh internal/auth/deploy.sh",
		"source internal/auth/deploy.sh",
		"python3 internal/auth/deploy.sh",
		"go run ./internal/auth",
		"git reset --hard",
		"git clean -fd",
		"git checkout -- .",
		"git checkout -- internal/auth/token.go",
		"git restore internal/auth/token.go",
		"git rm internal/auth/token.go",
		"git diff --output=internal/auth/out.patch",
		"gofmt -w .",
		"prettier --write .",
		"echo bad > README.md",
		"touch README.md",
	} {
		if act, reason := sessionShellPolicyCheck(recNoSandbox, repo, cmd); act != "deny" {
			t.Errorf("expected HARD DENY for %q, got action=%q reason=%q", cmd, act, reason)
		}
	}

	// Valid in-place edit strictly inside WritablePaths is NOT falsely hard-denied on sed expression.
	if act, reason := sessionShellPolicyCheck(recSandboxed, repo, "sed -i '' 's/a/b/' web/src/pages/Home.tsx"); act == "deny" {
		t.Errorf("unexpected deny for writable sed target: reason=%q", reason)
	}

	// Opaque/inline script without Seatbelt -> require_approval (never silently auto-approved!).
	if act, _ := sessionShellPolicyCheck(recNoSandbox, repo, `python3 -c "open('internal/auth/token.go','w').write('x')"`); act != "require_approval" {
		t.Errorf("expected require_approval without Seatbelt for python3 -c, got %q", act)
	}

	// In unattended mode without Seatbelt, gateCommand rejects require_approval immediately.
	svc := newPathGuardTestService(t, repo)
	if _, err := svc.policy.set(ApprovalPolicy{Mode: ApprovalModeUnattended}); err != nil {
		t.Fatal(err)
	}
	_ = svc.store.Put(recNoSandbox)
	cred := hookCredential{runtime: string(RuntimeNative), sessionID: recNoSandbox.ID, messageID: "m1"}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if ok, _ := svc.gateCommand(req, cred, recNoSandbox, approvalAsk{command: `python3 -c "print(1)"`, cwd: repo}); ok {
		t.Fatal("expected unattended mode without Seatbelt to reject non-readonly command in scoped session")
	}
}

func TestExternalHooksAndOpenCodeEnforceSessionPathScope(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	repo := initPathGuardTestRepo(t)
	svc := newPathGuardTestService(t, repo)
	if _, err := svc.policy.set(ApprovalPolicy{Mode: ApprovalModeTrusted}); err != nil {
		t.Fatal(err)
	}
	rec := SessionRecord{
		ID:            "ses_ext_scope",
		Runtime:       string(RuntimeClaudeCode),
		ProjectDir:    repo,
		WorktreePath:  repo,
		ReadOnlyPaths: []string{"internal/auth"},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	// Claude hook denies edit to read-only file.
	dec, reason := svc.decideClaudeHookCall(req, hookCredential{runtime: string(RuntimeClaudeCode), sessionID: rec.ID}, claudeHookPayload{
		Cwd:      repo,
		ToolName: "Edit",
		rawInput: json.RawMessage(`{"file_path":"internal/auth/token.go"}`),
	})
	if dec != "deny" || !strings.Contains(reason, "read-only") {
		t.Fatalf("Claude hook failed to deny read-only edit: dec=%q reason=%q", dec, reason)
	}

	// Gemini hook denies write_file to read-only file.
	dec, reason = svc.decideGeminiHookCall(req, hookCredential{runtime: string(RuntimeGeminiCLI), sessionID: rec.ID}, geminiHookPayload{
		Cwd:       repo,
		ToolName:  "write_file",
		ToolInput: json.RawMessage(`{"file_path":"internal/auth/token.go","content":"bad"}`),
	})
	if dec != "deny" || !strings.Contains(reason, "read-only") {
		t.Fatalf("Gemini hook failed to deny read-only write: dec=%q reason=%q", dec, reason)
	}

	// OpenCode edit request targeting read-only path is rejected.
	ocReason, ocDenied := sessionOpenCodeItemViolation(rec, repo, map[string]interface{}{
		"permission": "edit",
		"patterns":   []string{"internal/auth/token.go"},
	})
	if !ocDenied || !strings.Contains(ocReason, "read-only") {
		t.Fatalf("OpenCode scope check failed: denied=%v reason=%q", ocDenied, ocReason)
	}
}

func TestVerifySessionPathScopeAndConfineRuntimeCommand(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	repo := initPathGuardTestRepo(t)
	svc := newPathGuardTestService(t, repo)
	rec := SessionRecord{
		ID:             "ses_verify_scope",
		Runtime:        string(RuntimeNative),
		ProjectDir:     repo,
		WorktreePath:   repo,
		CommandSandbox: true,
		ReadOnlyPaths:  []string{"internal/auth"},
		WritablePaths:  []string{"web/src"},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	// Confined Seatbelt profile embeds DenyWrite/DenyExec for ReadOnlyPaths and narrowed AllowWrite.
	if sandbox.Available() {
		_, args, err := svc.confineRuntimeCommand(rec, "/bin/echo", []string{"ok"})
		if err != nil {
			t.Fatalf("confineRuntimeCommand failed: %v", err)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "internal/auth") || !strings.Contains(joined, "(deny process-exec") {
			t.Fatalf("expected Seatbelt profile to include read-only path rules, got:\n%s", joined)
		}
	}

	// Editing only inside WritablePaths passes post-turn verification.
	if err := os.WriteFile(filepath.Join(repo, "web", "src", "pages", "Home.tsx"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	violations, err := svc.VerifySessionPathScope(rec.ID)
	if err != nil {
		t.Fatalf("VerifySessionPathScope clean failed: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations, got %v", violations)
	}

	// Mutating a read-only file and creating an out-of-scope file are both caught!
	if err := os.WriteFile(filepath.Join(repo, "internal", "auth", "token.go"), []byte("hacked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	violations, err = svc.VerifySessionPathScope(rec.ID)
	if err != nil {
		t.Fatalf("VerifySessionPathScope dirty failed: %v", err)
	}
	if strings.Join(violations, ",") != "README.md,internal/auth/token.go" {
		t.Fatalf("unexpected violations: %v", violations)
	}
}

func TestSessionPathScopeSymlinkedWorkspaceParentPrefixAndSanitizedGitEnv(t *testing.T) {
	realParent := t.TempDir()
	realRepo := filepath.Join(realParent, "repo")
	if err := os.MkdirAll(filepath.Join(realRepo, "web", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(realRepo, "internal", "auth"), 0o755); err != nil {
		t.Fatal(err)
	}
	aliasParent := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(realParent, aliasParent); err != nil {
		t.Fatal(err)
	}
	aliasRepo := filepath.Join(aliasParent, "repo")
	// Symlink inside read-only dir pointing into writable dir must still be denied.
	if err := os.Symlink(filepath.Join(realRepo, "web", "src", "ok.ts"), filepath.Join(realRepo, "internal", "auth", "tricky_link.ts")); err != nil {
		t.Fatal(err)
	}

	rec := SessionRecord{
		ID:            "ses_symlink_parent",
		ProjectDir:    realRepo,
		WorktreePath:  realRepo,
		ReadOnlyPaths: []string{"internal/auth"},
		WritablePaths: []string{"web/src"},
	}

	// Legitimate write addressed via symlinked workspace parent prefix is allowed.
	if reason, bad := sessionWriteTargetViolation(rec, realRepo, filepath.Join(aliasRepo, "web", "src", "ok.ts")); bad {
		t.Fatalf("expected symlinked workspace parent prefix write inside WritablePaths to be allowed, got: %s", reason)
	}
	// Attack addressed via symlinked workspace parent prefix into read-only dir (even if symlink points into web/src) is denied.
	if _, bad := sessionWriteTargetViolation(rec, realRepo, filepath.Join(aliasRepo, "internal", "auth", "tricky_link.ts")); !bad {
		t.Fatal("expected write through symlink in read-only path to be denied even with symlinked parent prefix")
	}

	// Verify git status env strips interactive askpass/editor variables.
	t.Setenv("GIT_ASKPASS", "/bin/evil-askpass")
	t.Setenv("SSH_ASKPASS", "/bin/evil-ssh")
	t.Setenv("GIT_EDITOR", "vim")
	env := sanitizedGitStatusEnv()
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "GIT_ASKPASS", "SSH_ASKPASS", "GIT_EDITOR":
			t.Fatalf("sanitizedGitStatusEnv leaked %s", kv)
		}
	}
}

func TestSessionPathScopeHardenedShellWorktreeAndReconcile(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	repo := initPathGuardTestRepo(t)
	wt := filepath.Join(repo, ".hoxi", "worktrees", "wt_scope_test")
	if err := os.MkdirAll(filepath.Join(wt, "web", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wt, "internal", "auth"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Isolated worktree session with WritablePaths=["web/src"] must ALLOW writes inside
	//    worktree/web/src, and DENY writes targeting main repo/web/src outside worktree!
	wtRec := SessionRecord{
		ID:            "ses_wt_isolated",
		Runtime:       string(RuntimeNative),
		ProjectDir:    repo,
		WorktreePath:  wt,
		ReadOnlyPaths: []string{"internal/auth"},
		WritablePaths: []string{"web/src"},
	}
	if reason, bad := sessionWriteTargetViolation(wtRec, wt, filepath.Join(wt, "web", "src", "Home.tsx")); bad {
		t.Fatalf("expected write inside worktree writable path allowed, got: %s", reason)
	}
	if _, bad := sessionWriteTargetViolation(wtRec, wt, filepath.Join(repo, "web", "src", "Home.tsx")); !bad {
		t.Fatal("expected write targeting main ProjectDir writable path from isolated worktree to be denied")
	}

	// 2. Hardened shell parser: combined flags (-lc/-ec), subshell grouping (cd ... && rm ...),
	//    abbreviated git flags, and broad git mutations.
	for _, cmd := range []string{
		"bash -lc 'echo pwn > internal/auth/token.go'",
		"sh -ec 'rm internal/auth/token.go'",
		"(cd internal/auth && rm token.go)",
		"{ cd internal && echo bad > auth/token.go; }",
		"git reset --har",
		"git diff --outpu=internal/auth/out.patch",
		"git switch -f main",
		"git switch --discard-changes main",
		"git stash pop",
		"git stash apply",
		"git cherry-pick HEAD",
		"git merge other",
		"git rebase main",
		"git revert HEAD",
		"git am patch.mbox",
	} {
		if act, reason := sessionShellPolicyCheck(wtRec, wt, cmd); act != "deny" {
			t.Errorf("expected HARD DENY for %q, got action=%q reason=%q", cmd, act, reason)
		}
	}
	// Subshell cd reverts after closing paren; git stash list is not hard-denied.
	if act, reason := sessionShellPolicyCheck(wtRec, wt, "git stash list"); act == "deny" {
		t.Errorf("expected git stash list not hard-denied, got action=%q reason=%q", act, reason)
	}
	if act, reason := sessionShellPolicyCheck(wtRec, wt, "(cd internal/auth && ls) && touch web/src/Home.tsx"); act == "deny" {
		t.Errorf("expected subshell cd to revert before writable touch, got action=%q reason=%q", act, reason)
	}

	// 3. OpenCode session with CommandSandbox=true must NOT treat unwrapped bash commands as Seatbelt-confined.
	ocRec := SessionRecord{
		ID:             "ses_oc_scope",
		Runtime:        string(RuntimeOpenCode),
		ProjectDir:     repo,
		WorktreePath:   repo,
		CommandSandbox: true,
		ReadOnlyPaths:  []string{"internal/auth"},
	}
	if act, _ := sessionShellPolicyCheck(ocRec, repo, `python3 -c "open('internal/auth/token.go','w').write('x')"`); act != "require_approval" {
		t.Fatalf("expected unwrapped OpenCode bash in scoped session to require_approval, got %q", act)
	}

	// 4. reconcilePendingApprovals must NOT auto-approve a path-scoped require_approval command
	//    even when switching approval policy to Trusted mode!
	svc := newPathGuardTestService(t, repo)
	noSBRec := SessionRecord{
		ID:             "ses_reconcile_scope",
		Runtime:        string(RuntimeNative),
		ProjectDir:     repo,
		WorktreePath:   repo,
		CommandSandbox: false,
		ReadOnlyPaths:  []string{"internal/auth"},
	}
	if err := svc.store.Put(noSBRec); err != nil {
		t.Fatal(err)
	}
	replyCh := make(chan approvalReply, 1)
	svc.gate.mu.Lock()
	svc.gate.pending["per_rnr_scope_1"] = &pendingApproval{
		sid: noSBRec.ID,
		req: map[string]interface{}{
			"id":         "per_rnr_scope_1",
			"permission": "bash",
			"patterns":   []string{`python3 -c "print(1)"`},
			"metadata":   map[string]interface{}{"command": `python3 -c "print(1)"`},
		},
		reply: replyCh,
	}
	svc.gate.mu.Unlock()

	if _, err := svc.policy.set(ApprovalPolicy{Mode: ApprovalModeTrusted}); err != nil {
		t.Fatal(err)
	}
	svc.reconcilePendingApprovals()
	select {
	case rep := <-replyCh:
		t.Fatalf("reconcilePendingApprovals unexpectedly resolved scoped require_approval request: %+v", rep)
	default:
	}

	// Explicit path-scope violation waiting in pending queue is rejected on reconcile.
	denyCh := make(chan approvalReply, 1)
	svc.gate.mu.Lock()
	svc.gate.pending["per_rnr_scope_2"] = &pendingApproval{
		sid: noSBRec.ID,
		req: map[string]interface{}{
			"id":         "per_rnr_scope_2",
			"permission": "bash",
			"patterns":   []string{"rm internal/auth/token.go"},
			"metadata":   map[string]interface{}{"command": "rm internal/auth/token.go"},
		},
		reply: denyCh,
	}
	svc.gate.mu.Unlock()
	svc.reconcilePendingApprovals()
	select {
	case rep := <-denyCh:
		if rep.Reply != "reject" || rep.Source != approvalSourcePathScope {
			t.Fatalf("expected reject/path_scope, got %+v", rep)
		}
	default:
		t.Fatal("expected pending path-scope violation to be rejected during reconcile")
	}

	// 5. Protecting root AI instruction files blocks creating NEW nested rule files in arbitrary
	//    subdirectories (e.g. sub/AGENTS.md, packages/x/CLAUDE.md, sub/.cursorrules) across tools,
	//    shell, and post-turn verification, without falsely blocking unrelated nested filenames like README.md.
	ruleRec := SessionRecord{
		ID:            "ses_nested_rules",
		Runtime:       string(RuntimeNative),
		ProjectDir:    repo,
		WorktreePath:  repo,
		ReadOnlyPaths: []string{"AGENTS.md", "CLAUDE.md", ".cursorrules", "README.md"},
	}
	for _, nestedBad := range []string{
		"sub/AGENTS.md",
		"packages/x/CLAUDE.md",
		"packages/x/agents.md",
		"deep/nested/.cursorrules",
	} {
		if _, bad := sessionWriteTargetViolation(ruleRec, repo, filepath.Join(repo, filepath.FromSlash(nestedBad))); !bad {
			t.Errorf("expected creating nested rule file %q to be hard-denied", nestedBad)
		}
		if act, _ := sessionShellPolicyCheck(ruleRec, repo, "echo evil > "+nestedBad); act != "deny" {
			t.Errorf("expected shell write to nested rule file %q to be hard-denied, got %q", nestedBad, act)
		}
	}
	// Protecting root README.md does NOT falsely block docs/README.md.
	if reason, bad := sessionWriteTargetViolation(ruleRec, repo, filepath.Join(repo, "docs", "README.md")); bad {
		t.Fatalf("unexpected deny for docs/README.md: %s", reason)
	}
}
