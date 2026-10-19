package worktree

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNonInteractiveGitEnvSanitization(t *testing.T) {
	t.Setenv("SSH_ASKPASS", "/usr/bin/ssh-askpass")
	t.Setenv("GIT_ASKPASS", "/usr/bin/git-askpass")
	t.Setenv("EDITOR", "vim")
	t.Setenv("VISUAL", "code")
	t.Setenv("GIT_EDITOR", "nano")
	t.Setenv("PAGER", "less")
	t.Setenv("GIT_EXTERNAL_DIFF", "difft")

	env := nonInteractiveGitEnv("CUSTOM_VAR=1")
	joined := "\n" + strings.Join(env, "\n") + "\n"

	for _, forbidden := range []string{
		"\nSSH_ASKPASS=",
		"\nGIT_ASKPASS=",
		"\nEDITOR=",
		"\nVISUAL=",
		"\nGIT_EDITOR=",
		"\nPAGER=",
		"\nGIT_EXTERNAL_DIFF=",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("expected %s to be stripped from nonInteractiveGitEnv", strings.TrimSpace(forbidden))
		}
	}

	for _, required := range []string{
		"\nGIT_TERMINAL_PROMPT=0\n",
		"\nGIT_SSH_COMMAND=ssh -o BatchMode=yes\n",
		"\nCUSTOM_VAR=1\n",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("expected %s in nonInteractiveGitEnv", strings.TrimSpace(required))
		}
	}
}

func TestCreateWorktreeSyncsEnvMarksSpotlightAndRunsSetupScript(t *testing.T) {
	repoDir := initTempGitRepo(t)

	if err := os.WriteFile(filepath.Join(repoDir, ".env"), []byte("API_KEY=secret123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".env.local"), []byte("DEBUG=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, ".hoxi"), 0o755); err != nil {
		t.Fatal(err)
	}
	setupScript := "#!/bin/sh\nprintf '%s|%s' \"$HOXI_SESSION_ID\" \"$HOXI_BRANCH\" > .hoxi-setup-ran\n"
	if err := os.WriteFile(filepath.Join(repoDir, ".hoxi", "setup.sh"), []byte(setupScript), 0o755); err != nil {
		t.Fatal(err)
	}
	ensureGitExclude(repoDir, ".hoxi-setup-ran")

	mgr := NewManager(repoDir)
	meta, err := mgr.Create("ses_env_test")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if !meta.Isolated {
		t.Fatalf("expected isolated worktree")
	}

	// 1. Spotlight exclusion marker exists in .hoxi/worktrees/ on macOS only
	markerPath := filepath.Join(repoDir, ".hoxi", "worktrees", spotlightNoIndexMarker)
	if _, err := os.Stat(markerPath); runtime.GOOS == "darwin" && err != nil {
		t.Fatalf("expected Spotlight marker %s: %v", markerPath, err)
	} else if runtime.GOOS != "darwin" && !os.IsNotExist(err) {
		t.Fatalf("Spotlight marker must not be written on %s: %v", runtime.GOOS, err)
	}

	// 2. .env and .env.local were copied into the worktree
	for name, want := range map[string]string{
		".env":       "API_KEY=secret123\n",
		".env.local": "DEBUG=true\n",
	} {
		got, readErr := os.ReadFile(filepath.Join(meta.Directory, name))
		if readErr != nil || string(got) != want {
			t.Fatalf("expected synced %s=%q, got %q (err=%v)", name, want, string(got), readErr)
		}
	}

	// 3. .hoxi/setup.sh executed with HOXI_SESSION_ID and HOXI_BRANCH
	hookOut, err := os.ReadFile(filepath.Join(meta.Directory, ".hoxi-setup-ran"))
	if err != nil || string(hookOut) != "ses_env_test|hoxi/ses_env_test" {
		t.Fatalf("expected setup hook output, got %q (err=%v)", string(hookOut), err)
	}
	_ = os.Remove(filepath.Join(meta.Directory, ".hoxi-setup-ran"))

	// 4. Fresh worktree with synced .env files remains clean and healthy
	st, err := mgr.Status("ses_env_test")
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if st.Dirty {
		t.Fatalf("expected synced .env files not to dirty worktree status: %+v", st)
	}
	if st.Health != HealthOK {
		t.Fatalf("expected Health=%q, got %q", HealthOK, st.Health)
	}

	// 5. Non-forced Remove succeeds cleanly because synced .env files are cleaned up before git worktree remove
	if err := mgr.Remove("ses_env_test", false); err != nil {
		t.Fatalf("expected non-forced Remove to succeed with synced .env files: %v", err)
	}
}

func TestPortableSnapshotCaptureApplyAndTransfer(t *testing.T) {
	repoDir := initTempGitRepo(t)
	if err := os.WriteFile(filepath.Join(repoDir, "staged.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repoDir, "add", "staged.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repoDir, "commit", "-m", "chore: add staged.txt"); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(repoDir)
	meta, err := mgr.Create("ses_transfer")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer func() { _ = mgr.Remove("ses_transfer", true) }()

	// Create staged change, unstaged change, and untracked file in repoDir
	if err := os.WriteFile(filepath.Join(repoDir, "staged.txt"), []byte("base\nstaged-line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repoDir, "add", "staged.txt"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Hoxi Test\nunstaged-edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "untracked.txt"), []byte("untracked-payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := TransferWorkingState(repoDir, meta.Directory); err != nil {
		t.Fatalf("TransferWorkingState failed: %v", err)
	}

	// Verify staged change in target worktree index
	cachedDiff, err := runGitRaw(meta.Directory, "diff", "--cached")
	if err != nil || !strings.Contains(cachedDiff, "+staged-line") {
		t.Fatalf("expected staged diff in worktree, got %q (err=%v)", cachedDiff, err)
	}
	// Verify unstaged change in target worktree
	unstagedDiff, err := runGitRaw(meta.Directory, "diff")
	if err != nil || !strings.Contains(unstagedDiff, "+unstaged-edit") {
		t.Fatalf("expected unstaged diff in worktree, got %q (err=%v)", unstagedDiff, err)
	}
	// Verify untracked file in target worktree
	untrackedData, err := os.ReadFile(filepath.Join(meta.Directory, "untracked.txt"))
	if err != nil || string(untrackedData) != "untracked-payload\n" {
		t.Fatalf("expected untracked file in worktree, got %q (err=%v)", string(untrackedData), err)
	}
}

func TestDiffGeneratedClassificationLargeSuppressionAndDiffFileOnDemand(t *testing.T) {
	repoDir := initTempGitRepo(t)
	mgr := NewManager(repoDir)
	meta, err := mgr.Create("ses_large_diff")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer func() { _ = mgr.Remove("ses_large_diff", true) }()

	// Write a generated lockfile with >400 lines so it triggers both GeneratedLike and Large suppression
	var lockLines strings.Builder
	for i := 0; i < 450; i++ {
		fmt.Fprintf(&lockLines, "pkg-%d: version-1.0.%d\n", i, i)
	}
	if err := os.WriteFile(filepath.Join(meta.Directory, "pnpm-lock.yaml"), []byte(lockLines.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meta.Directory, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diffs, err := mgr.Diff("ses_large_diff")
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}

	var lockDiff, mainDiff *worktreeFileDiff
	for i := range diffs {
		switch diffs[i].File {
		case "pnpm-lock.yaml":
			lockDiff = &diffs[i]
		case "main.go":
			mainDiff = &diffs[i]
		}
	}
	if lockDiff == nil || mainDiff == nil {
		t.Fatalf("expected both pnpm-lock.yaml and main.go in diffs, got %+v", diffs)
	}
	if !lockDiff.GeneratedLike || !lockDiff.Large || lockDiff.Patch != "" {
		t.Fatalf("expected pnpm-lock.yaml to be GeneratedLike+Large with omitted Patch in summary diff, got %+v", *lockDiff)
	}
	if mainDiff.GeneratedLike || mainDiff.Large || !strings.Contains(mainDiff.Patch, "package main") {
		t.Fatalf("expected main.go to include inline patch, got %+v", *mainDiff)
	}

	// Now fetch pnpm-lock.yaml on demand via HTTP route ?file=pnpm-lock.yaml
	mux := http.NewServeMux()
	mgr.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/hoxi/worktree/diff?sessionID=ses_large_diff&file=pnpm-lock.yaml", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from single-file diff route, got %d: %s", w.Code, w.Body.String())
	}
	var detail worktreeFileDiff
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal single-file diff: %v", err)
	}
	if detail.File != "pnpm-lock.yaml" || !detail.GeneratedLike || !strings.Contains(detail.Patch, "+pkg-0: version-1.0.0") || detail.Stamp == "" {
		t.Fatalf("expected full patch and stamp from single-file diff route, got %+v", detail)
	}
}

func TestQuarantineTrackerAndWorktreeHealthClassification(t *testing.T) {
	q := newQuarantineTracker()
	now := time.Now()
	q.now = func() time.Time { return now }

	key := "/tmp/wt-broken"
	if q.RecordFailure(key) {
		t.Fatal("1st failure should not quarantine")
	}
	if q.RecordFailure(key) {
		t.Fatal("2nd failure should not quarantine")
	}
	if !q.RecordFailure(key) {
		t.Fatal("3rd consecutive failure should trigger quarantine")
	}
	if !q.Blocked(key) {
		t.Fatal("expected key to be blocked during quarantine window")
	}

	// Advance clock past baseQuarantineWindow: half-open probe allowed once
	now = now.Add(baseQuarantineWindow + time.Second)
	if q.Blocked(key) {
		t.Fatal("expected quarantine window expiry to allow half-open probe")
	}

	// Clear resets failure state
	q.Clear(key)
	if q.RecordFailure(key) {
		t.Fatal("after Clear, 1st failure should not quarantine")
	}

	// Verify HealthAbsentRestorable when worktree directory is missing but branch exists
	repoDir := initTempGitRepo(t)
	mgr := NewManager(repoDir)
	meta, err := mgr.Create("ses_restorable")
	if err != nil {
		t.Fatal(err)
	}
	detachAndRemoveDir(meta.Directory)

	st, err := mgr.Status("ses_restorable")
	if err != nil {
		t.Fatal(err)
	}
	if st.Health != HealthAbsentRestorable {
		t.Fatalf("expected Health=%q when directory removed but branch exists, got %q", HealthAbsentRestorable, st.Health)
	}
}

func TestBaseBranchDriftSyncAndAbsentRestorableRecovery(t *testing.T) {
	repoDir := initTempGitRepo(t)
	mgr := NewManager(repoDir)

	meta, err := mgr.Create("ses_drift")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer func() { _ = mgr.Remove("ses_drift", true) }()

	// Commit a change on the primary branch (main) after the isolated worktree was created
	if err := os.WriteFile(filepath.Join(repoDir, "upstream.txt"), []byte("upstream-change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repoDir, "add", "upstream.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repoDir, "commit", "-m", "feat: upstream commit on main"); err != nil {
		t.Fatal(err)
	}

	// Also make an uncommitted local edit inside the isolated worktree to verify autostash rebase
	if err := os.WriteFile(filepath.Join(meta.Directory, "local-wip.txt"), []byte("wip-content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := mgr.Status("ses_drift")
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if st.BaseBranch != "main" || st.BehindCount != 1 {
		t.Fatalf("expected BaseBranch=main and BehindCount=1, got BaseBranch=%q BehindCount=%d", st.BaseBranch, st.BehindCount)
	}

	// Sync worktree onto base branch via HTTP route POST /api/hoxi/worktree/sync
	mux := http.NewServeMux()
	mgr.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/worktree/sync", strings.NewReader(`{"sessionID":"ses_drift"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/hoxi/worktree/sync, got %d: %s", w.Code, w.Body.String())
	}

	stAfter, err := mgr.Status("ses_drift")
	if err != nil {
		t.Fatalf("Status after sync failed: %v", err)
	}
	if stAfter.BehindCount != 0 {
		t.Fatalf("expected BehindCount=0 after SyncWithBase, got %d", stAfter.BehindCount)
	}
	// Verify upstream file is now in the worktree and local uncommitted file survived autostash
	if _, err := os.Stat(filepath.Join(meta.Directory, "upstream.txt")); err != nil {
		t.Fatalf("expected upstream.txt in synced worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(meta.Directory, "local-wip.txt")); err != nil {
		t.Fatalf("expected local-wip.txt preserved after rebase: %v", err)
	}

	// Delete the worktree directory directly on disk (without git worktree remove) -> absent-restorable
	if err := os.RemoveAll(meta.Directory); err != nil {
		t.Fatalf("RemoveAll worktree dir: %v", err)
	}
	stMissing, err := mgr.Status("ses_drift")
	if err != nil {
		t.Fatalf("Status when missing failed: %v", err)
	}
	if stMissing.Health != HealthAbsentRestorable {
		t.Fatalf("expected Health=%q when directory deleted on disk, got %q", HealthAbsentRestorable, stMissing.Health)
	}

	// Restore the absent-restorable worktree directory by calling Create("ses_drift")
	restored, err := mgr.Create("ses_drift")
	if err != nil {
		t.Fatalf("Restore via Create failed: %v", err)
	}
	stRestored, err := mgr.Status("ses_drift")
	if err != nil {
		t.Fatalf("Status after restore failed: %v", err)
	}
	if !restored.Isolated || stRestored.Health != HealthOK {
		t.Fatalf("expected restored worktree to be isolated and healthy, got meta=%+v status=%+v", restored, stRestored)
	}
	if _, err := os.Stat(filepath.Join(restored.Directory, "upstream.txt")); err != nil {
		t.Fatalf("expected upstream.txt in restored worktree directory: %v", err)
	}
}

func TestBuildRemoteCompareURLAndStatus(t *testing.T) {
	if got := buildRemoteCompareURL("git@github.com:hoxiai/hoxi.git", "main", "hoxi/ses_123"); got != "https://github.com/hoxiai/hoxi/compare/main...hoxi/ses_123?expand=1" {
		t.Fatalf("unexpected GitHub SSH compareURL: %q", got)
	}
	if got := buildRemoteCompareURL("https://token@github.com/hoxiai/hoxi.git", "", "feat/pr-flow"); got != "https://github.com/hoxiai/hoxi/compare/feat/pr-flow?expand=1" {
		t.Fatalf("unexpected GitHub HTTPS compareURL: %q", got)
	}
	if got := buildRemoteCompareURL("git@gitlab.com:group/sub/repo.git", "main", "hoxi/ses_1"); got != "https://gitlab.com/group/sub/repo/-/merge_requests/new?merge_request[source_branch]=hoxi%2Fses_1&merge_request[target_branch]=main" {
		t.Fatalf("unexpected GitLab compareURL: %q", got)
	}
	if got := buildRemoteCompareURL("https://github.com/hoxiai/hoxi.git", "main", "main"); got != "" {
		t.Fatalf("expected empty compareURL when branch equals baseBranch, got %q", got)
	}

	root := initTempGitRepo(t)
	if _, err := runGit(root, "remote", "add", "origin", "git@github.com:hoxiai/hoxi.git"); err != nil {
		t.Fatalf("remote add origin: %v", err)
	}

	mgr := NewManager(root)
	if _, err := mgr.Create("ses_pr"); err != nil {
		t.Fatalf("Create worktree: %v", err)
	}
	st, err := mgr.Status("ses_pr")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(st.CompareURL, "https://github.com/hoxiai/hoxi/compare/") || !strings.HasSuffix(st.CompareURL, "...hoxi/ses_pr?expand=1") {
		t.Fatalf("expected CompareURL for worktree branch, got %q", st.CompareURL)
	}
}

func TestMultiDirectoryAndNestedGitReposStatusAndDiff(t *testing.T) {
	primary := initTempGitRepo(t)
	extraRepo := initTempGitRepo(t)

	// Create a dirty edit in extraRepo and leave primary clean
	if err := os.WriteFile(filepath.Join(extraRepo, "extra-change.txt"), []byte("hello from extra repo\n"), 0o644); err != nil {
		t.Fatalf("write extra-change.txt: %v", err)
	}

	mgr := NewManager(primary)
	st, err := mgr.StatusWithExtraDirs("", []string{extraRepo})
	if err != nil {
		t.Fatalf("StatusWithExtraDirs failed: %v", err)
	}
	if len(st.Repos) != 2 {
		t.Fatalf("expected 2 repos in StatusWithExtraDirs, got %d (%+v)", len(st.Repos), st.Repos)
	}
	if !st.Dirty || st.ChangedFiles != 1 {
		t.Fatalf("expected aggregated Dirty=true and ChangedFiles=1, got Dirty=%v ChangedFiles=%d", st.Dirty, st.ChangedFiles)
	}

	diffs, err := mgr.DiffWithExtraDirs("", []string{extraRepo})
	if err != nil {
		t.Fatalf("DiffWithExtraDirs failed: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff from extraRepo, got %d (%+v)", len(diffs), diffs)
	}
	if diffs[0].RepoDir == "" || !strings.HasSuffix(diffs[0].File, "/extra-change.txt") {
		t.Fatalf("expected RepoDir and prefixed File on external repo diff, got %+v", diffs[0])
	}
}

func TestOnDemandNestedGitRepoLoadingWhenRootIsGitRepo(t *testing.T) {
	root := initTempGitRepo(t)
	subA := initSubGitRepo(t, root, filepath.Join("plugins", "app", "stripe"), "main")
	subB := initSubGitRepo(t, root, filepath.Join("plugins", "app", "paypal"), "main")

	// Dirty both nested repos
	if err := os.WriteFile(filepath.Join(subA, "main.go"), []byte("package main\n// stripe edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subB, "main.go"), []byte("package main\n// paypal edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(root)

	// 1. Before any file in plugins/app/* is touched by a session, root status only loads root repo
	stBefore, err := mgr.Status("__root__")
	if err != nil {
		t.Fatalf("Status before touch failed: %v", err)
	}
	if len(stBefore.Repos) != 1 {
		t.Fatalf("expected only 1 repo (root) loaded before sub-repos are touched, got %d: %+v", len(stBefore.Repos), stBefore.Repos)
	}

	// 2. Touching a file inside plugins/app/stripe via DiffForFiles activates only stripe (not paypal)
	edited := map[string]struct{}{
		"plugins/app/stripe/main.go": {},
	}
	diffs, err := mgr.DiffForFiles("__root__", edited)
	if err != nil {
		t.Fatalf("DiffForFiles failed: %v", err)
	}
	if len(diffs) != 1 || diffs[0].File != "plugins/app/stripe/main.go" {
		t.Fatalf("expected only stripe diff from DiffForFiles, got %+v", diffs)
	}

	stAfter, err := mgr.Status("__root__")
	if err != nil {
		t.Fatalf("Status after touch failed: %v", err)
	}
	if len(stAfter.Repos) != 2 {
		t.Fatalf("expected root + touched stripe repo (2 repos, not untouched paypal), got %d: %+v", len(stAfter.Repos), stAfter.Repos)
	}
}


