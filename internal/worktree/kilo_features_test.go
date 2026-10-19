package worktree

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	// 1. Spotlight exclusion marker exists in .hoxi/worktrees/
	markerPath := filepath.Join(repoDir, ".hoxi", "worktrees", spotlightNoIndexMarker)
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("expected Spotlight marker %s: %v", markerPath, err)
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
