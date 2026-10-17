package worktree

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTempGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	run("init", "-b", "main")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@hoxi.local")

	if err := os.MkdirAll(filepath.Join(dir, ".opencode"), 0o755); err != nil {
		t.Fatalf("mkdir .opencode: %v", err)
	}
	excludePath := filepath.Join(dir, ".git", "info", "exclude")
	_ = os.WriteFile(excludePath, []byte(".opencode\n"), 0o644)
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# Hoxi Test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "chore: initial commit")
	return dir
}

func configGitUser(dir string) {
	for _, kv := range [][2]string{{"user.name", "Test User"}, {"user.email", "test@hoxi.local"}} {
		_ = exec.Command("git", "-C", dir, "config", kv[0], kv[1]).Run()
	}
}

func TestSanitizeSessionIDAndPorcelainHeader(t *testing.T) {
	cases := map[string]string{
		"ses_123-abc":          "ses_123-abc",
		"../evil/../../passwd": "evilpasswd",
		"   ":                  "",
		"ses!@#$%^&*()99":      "ses99",
	}
	for input, want := range cases {
		if got := sanitizeSessionID(input); got != want {
			t.Errorf("sanitizeSessionID(%q) = %q, want %q", input, got, want)
		}
	}

	headerCases := map[string]string{
		"## ":                                    "",
		"## No commits yet on main":              "main",
		"## Initial commit on dev":               "dev",
		"## HEAD (no branch)":                    "HEAD",
		"## feat/ui...origin/feat/ui [ahead 1]":  "feat/ui",
		"## fix-bug [behind 2]":                  "fix-bug",
		"## main":                                "main",
	}
	for input, want := range headerCases {
		if got := ParseBranchFromPorcelainHeader(input); got != want {
			t.Errorf("ParseBranchFromPorcelainHeader(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRootDirAndNonGitCreate(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	mgr := NewManager(dir1)

	if mgr.RootDir() != dir1 {
		t.Fatalf("RootDir() = %q, want %q", mgr.RootDir(), dir1)
	}

	// Ignore empty, non-existent, or file paths
	mgr.SetRootDir("   ")
	mgr.SetRootDir(filepath.Join(dir1, "non-existent"))
	filePath := filepath.Join(dir1, "file.txt")
	_ = os.WriteFile(filePath, []byte("x"), 0o644)
	mgr.SetRootDir(filePath)
	if mgr.RootDir() != dir1 {
		t.Fatalf("RootDir() changed unexpectedly to %q", mgr.RootDir())
	}

	mgr.SetRootDir(dir2)
	if mgr.RootDir() != dir2 {
		t.Fatalf("SetRootDir failed: got %q, want %q", mgr.RootDir(), dir2)
	}

	if _, err := mgr.Create(""); err == nil {
		t.Fatalf("expected error for empty sessionID")
	}

	// Non-git directory should fallback to Isolated=false
	meta, err := mgr.Create("ses_nongit")
	if err != nil {
		t.Fatalf("Create in non-git dir failed: %v", err)
	}
	if meta.Isolated || meta.Directory != dir2 {
		t.Fatalf("expected non-isolated fallback meta, got %+v", meta)
	}
	if err := mgr.Remove("ses_nongit", false); err != nil {
		t.Fatalf("Remove non-isolated session failed: %v", err)
	}
}

func TestWorktreeLifecycleCreateDiffRevertApplyRemove(t *testing.T) {
	repoDir := initTempGitRepo(t)
	mgr := NewManager(repoDir)

	// 1. Create worktree for session (and verify idempotent second call)
	info, err := mgr.Create("ses_unit_01")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if !info.Isolated || info.Branch != "hoxi/ses_unit_01" {
		t.Fatalf("unexpected worktree info: %+v", info)
	}
	infoCached, err := mgr.Create("ses_unit_01")
	if err != nil || infoCached.Directory != info.Directory {
		t.Fatalf("expected cached worktree info, got %+v, err=%v", infoCached, err)
	}
	configGitUser(info.Directory)

	// Verify .opencode symlink was created
	if _, err := os.Lstat(filepath.Join(info.Directory, ".opencode")); err != nil {
		t.Fatalf("expected .opencode symlink in worktree: %v", err)
	}

	// Verify RunGit and RunGitRaw helpers
	if out, err := RunGit(repoDir, "rev-parse", "--abbrev-ref", "HEAD"); err != nil || out != "main" {
		t.Fatalf("RunGit failed: out=%q err=%v", out, err)
	}
	if out, err := RunGitRaw(repoDir, "status", "--porcelain"); err != nil {
		t.Fatalf("RunGitRaw failed: out=%q err=%v", out, err)
	}

	// 2. Modify tracked file and create untracked file in worktree
	trackedPath := filepath.Join(info.Directory, "README.md")
	if err := os.WriteFile(trackedPath, []byte("# Hoxi Test\nAdded line\n"), 0o644); err != nil {
		t.Fatalf("modify README: %v", err)
	}
	untrackedPath := filepath.Join(info.Directory, "notes.txt")
	if err := os.WriteFile(untrackedPath, []byte("temp notes\nline 2\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	status, err := mgr.Status("ses_unit_01")
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !status.Dirty || status.ChangedFiles != 2 {
		t.Fatalf("expected 2 changed files (tracked + untracked), got %+v", status)
	}

	diffs, err := mgr.Diff("ses_unit_01")
	if err != nil || len(diffs) != 2 {
		t.Fatalf("expected 2 file diffs, got %d (err=%v)", len(diffs), err)
	}

	// Removing dirty worktree with force=false should fail
	if err := mgr.Remove("ses_unit_01", false); err == nil {
		t.Fatalf("expected Remove(force=false) on dirty worktree to return error")
	}

	// 3. Revert validation & reverting untracked file
	if err := mgr.RevertFile("ses_unit_01", "../outside"); err == nil {
		t.Fatalf("expected error for invalid relative path")
	}
	if err := mgr.RevertFile("ses_unit_01", "notes.txt"); err != nil {
		t.Fatalf("RevertFile failed: %v", err)
	}
	if _, err := os.Stat(untrackedPath); !os.IsNotExist(err) {
		t.Fatalf("expected untracked file to be removed after RevertFile")
	}

	// 4. Commit inside worktree and apply change back to main workspace
	if _, err := mgr.CommitWorktree("ses_unit_01", "   "); err == nil {
		t.Fatalf("expected error for empty commit message")
	}
	hash, err := mgr.CommitWorktree("ses_unit_01", "feat: update readme")
	if err != nil || hash == "" {
		t.Fatalf("CommitWorktree failed: err=%v, hash=%q", err, hash)
	}
	if err := mgr.Apply("ses_unit_01", "patch"); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	mainReadme, err := os.ReadFile(filepath.Join(repoDir, "README.md"))
	if err != nil {
		t.Fatalf("read main README: %v", err)
	}
	if string(mainReadme) != "# Hoxi Test\nAdded line\n" {
		t.Fatalf("expected main README to have applied change, got %q", string(mainReadme))
	}

	// 5. Remove worktree
	if err := mgr.Remove("ses_unit_01", true); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	afterRemove, err := mgr.Status("ses_unit_01")
	if err != nil {
		t.Fatalf("Status after remove failed: %v", err)
	}
	if afterRemove.Isolated {
		t.Fatalf("expected worktree Isolated=false after Remove")
	}
}

func TestBranchesCheckoutRenameApplyMergeAndPrune(t *testing.T) {
	repoDir := initTempGitRepo(t)
	mgr := NewManager(repoDir)

	// Create two sandbox sessions
	s1, err := mgr.Create("ses_b1")
	if err != nil {
		t.Fatalf("Create ses_b1: %v", err)
	}
	s2, err := mgr.Create("ses_b2")
	if err != nil {
		t.Fatalf("Create ses_b2: %v", err)
	}
	configGitUser(s1.Directory)

	// ListBranches should report main + 2 sandboxes
	brList, err := mgr.ListBranches()
	if err != nil {
		t.Fatalf("ListBranches failed: %v", err)
	}
	if brList.CurrentBranch != "main" || brList.SandboxCount != 2 || len(brList.Branches) < 3 {
		t.Fatalf("unexpected branch list: %+v", brList)
	}

	// RenameBranch on isolated session
	renamed, err := mgr.RenameBranch("ses_b1", "hoxi/ses_b1_custom")
	if err != nil || renamed != "hoxi/ses_b1_custom" {
		t.Fatalf("RenameBranch failed: renamed=%q err=%v", renamed, err)
	}
	if _, err := mgr.RenameBranch("ses_b1", "-invalid"); err == nil {
		t.Fatalf("expected error for invalid branch name")
	}

	// Uncommitted change in s1 applied via "merge" mode
	_ = os.WriteFile(filepath.Join(s1.Directory, "merged.txt"), []byte("from merge\n"), 0o644)
	if err := mgr.Apply("ses_b1", "merge"); err != nil {
		t.Fatalf("Apply(merge) failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "merged.txt")); err != nil {
		t.Fatalf("expected merged.txt in main repo after merge apply: %v", err)
	}

	// CheckoutBranch create=true and existing
	if _, err := mgr.CheckoutBranch("feat..bad", true); err == nil {
		t.Fatalf("expected error for invalid branch name in CheckoutBranch")
	}
	st, err := mgr.CheckoutBranch("feat/new-branch", true)
	if err != nil || st.Branch != "feat/new-branch" {
		t.Fatalf("CheckoutBranch(create=true) failed: st=%+v err=%v", st, err)
	}
	st, err = mgr.CheckoutBranch("main", false)
	if err != nil || st.Branch != "main" {
		t.Fatalf("CheckoutBranch(main) failed: st=%+v err=%v", st, err)
	}

	// PruneSandboxes excluding ses_b2
	removed, err := mgr.PruneSandboxes("ses_b2")
	if err != nil || removed == 0 {
		t.Fatalf("PruneSandboxes failed: removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(s2.Directory); err != nil {
		t.Fatalf("expected excluded session ses_b2 directory to remain: %v", err)
	}

	// Fresh Manager should discover existing ses_b2 worktree from disk
	freshMgr := NewManager(repoDir)
	if _, err := freshMgr.Diff("ses_b2"); err != nil {
		t.Fatalf("freshMgr.Diff(ses_b2) failed: %v", err)
	}
	if _, err := freshMgr.RenameBranch("ses_b2", "hoxi/ses_b2_renamed"); err != nil {
		t.Fatalf("freshMgr.RenameBranch(ses_b2) failed: %v", err)
	}
	if err := freshMgr.RemoveWithOptions("ses_b2", true, true); err != nil {
		t.Fatalf("freshMgr.RemoveWithOptions(ses_b2) failed: %v", err)
	}
}

func TestWorktreeHTTPRoutes(t *testing.T) {
	repoDir := initTempGitRepo(t)
	mgr := NewManager(repoDir)
	mux := http.NewServeMux()
	mgr.RegisterRoutes(mux)

	doJSON := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			if s, ok := body.(string); ok {
				buf.WriteString(s)
			} else {
				_ = json.NewEncoder(&buf).Encode(body)
			}
		}
		req := httptest.NewRequest(method, path, &buf)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// Method checks
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/hoxi/worktree/create"},
		{http.MethodGet, "/api/hoxi/worktree/apply"},
		{http.MethodGet, "/api/hoxi/worktree/revert-file"},
		{http.MethodGet, "/api/hoxi/worktree/commit"},
		{http.MethodPost, "/api/hoxi/worktree/branches"},
		{http.MethodGet, "/api/hoxi/worktree/checkout"},
		{http.MethodGet, "/api/hoxi/worktree/rename-branch"},
		{http.MethodGet, "/api/hoxi/worktree/prune-sandboxes"},
		{http.MethodGet, "/api/hoxi/worktree/ses_http"},
	} {
		if rec := doJSON(tc.method, tc.path, nil); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s status = %d, want 405", tc.method, tc.path, rec.Code)
		}
	}

	// 1. POST /api/hoxi/worktree/create
	if rec := doJSON(http.MethodPost, "/api/hoxi/worktree/create", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("create bad json status = %d, want 400", rec.Code)
	}
	rec := doJSON(http.MethodPost, "/api/hoxi/worktree/create", map[string]any{
		"sessionID": "ses_http",
		"directory": repoDir,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	var created worktreeMeta
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	configGitUser(created.Directory)

	// Write a change inside the created worktree
	_ = os.WriteFile(filepath.Join(created.Directory, "http.txt"), []byte("hello http\n"), 0o644)

	// 2. GET /api/hoxi/worktree/status & /diff
	rec = doJSON(http.MethodGet, "/api/hoxi/worktree/status?sessionID=ses_http", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"dirty":true`) {
		t.Fatalf("status unexpected response: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(http.MethodGet, "/api/hoxi/worktree/diff?sessionID=ses_http", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "http.txt") {
		t.Fatalf("diff unexpected response: %d %s", rec.Code, rec.Body.String())
	}

	// 3. POST /api/hoxi/worktree/revert-file
	_ = os.WriteFile(filepath.Join(created.Directory, "temp.txt"), []byte("discard me\n"), 0o644)
	rec = doJSON(http.MethodPost, "/api/hoxi/worktree/revert-file", map[string]any{
		"sessionID": "ses_http",
		"file":      "temp.txt",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("revert-file status = %d: %s", rec.Code, rec.Body.String())
	}

	// 4. POST /api/hoxi/worktree/commit
	rec = doJSON(http.MethodPost, "/api/hoxi/worktree/commit", map[string]any{
		"sessionID": "ses_http",
		"message":   "feat: add http.txt",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"commit"`) {
		t.Fatalf("commit failed: %d %s", rec.Code, rec.Body.String())
	}

	// 5. POST /api/hoxi/worktree/apply
	rec = doJSON(http.MethodPost, "/api/hoxi/worktree/apply", map[string]any{
		"sessionID": "ses_http",
		"mode":      "patch",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply failed: %d %s", rec.Code, rec.Body.String())
	}

	// 6. GET /api/hoxi/worktree/branches
	rec = doJSON(http.MethodGet, "/api/hoxi/worktree/branches?directory="+repoDir, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hoxi/ses_http") {
		t.Fatalf("branches failed: %d %s", rec.Code, rec.Body.String())
	}

	// 7. POST /api/hoxi/worktree/rename-branch
	rec = doJSON(http.MethodPost, "/api/hoxi/worktree/rename-branch", map[string]any{
		"sessionID": "ses_http",
		"newBranch": "hoxi/ses_http_renamed",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename-branch failed: %d %s", rec.Code, rec.Body.String())
	}

	// 8. POST /api/hoxi/worktree/checkout
	rec = doJSON(http.MethodPost, "/api/hoxi/worktree/checkout", map[string]any{
		"branch":    "feat/http-branch",
		"create":    true,
		"directory": repoDir,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("checkout create failed: %d %s", rec.Code, rec.Body.String())
	}
	_ = doJSON(http.MethodPost, "/api/hoxi/worktree/checkout", map[string]any{
		"branch": "main",
		"create": false,
	})

	// 9. DELETE /api/hoxi/worktree/{sessionID}
	rec = doJSON(http.MethodDelete, "/api/hoxi/worktree/ses_http?force=true", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete worktree failed: %d %s", rec.Code, rec.Body.String())
	}

	// 10. POST /api/hoxi/worktree/prune-sandboxes
	_ = doJSON(http.MethodPost, "/api/hoxi/worktree/create", map[string]any{"sessionID": "ses_prune"})
	rec = doJSON(http.MethodPost, "/api/hoxi/worktree/prune-sandboxes", map[string]any{
		"directory": repoDir,
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"removedCount"`) {
		t.Fatalf("prune-sandboxes failed: %d %s", rec.Code, rec.Body.String())
	}
}
