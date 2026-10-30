package worktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	// KiloNonInteractiveSSHCommand prevents SSH from opening interactive passphrase
	// or host-key prompts during background Git operations when the user has not
	// configured a custom GIT_SSH_COMMAND.
	KiloNonInteractiveSSHCommand = "ssh -o BatchMode=yes"
	spotlightNoIndexMarker       = ".metadata_never_index"
	setupScriptTimeout           = 30 * time.Second
)

// nonInteractiveGitEnv builds a sanitized environment for non-interactive Git
// subprocesses, stripping GUI askpass/editor/pager hooks that can hang background
// polling or corrupt machine-parsed stdout.
func nonInteractiveGitEnv(extra ...string) []string {
	rawEnv := os.Environ()
	out := make([]string, 0, len(rawEnv)+len(extra)+2)
	hasSSHCommand := false

	for _, kv := range rawEnv {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		switch key {
		case "GIT_TERMINAL_PROMPT",
			"SSH_ASKPASS",
			"GIT_ASKPASS",
			"EDITOR",
			"VISUAL",
			"GIT_EDITOR",
			"GIT_SEQUENCE_EDITOR",
			"PAGER",
			"GIT_PAGER",
			"GIT_EXTERNAL_DIFF":
			continue
		case "GIT_SSH_COMMAND":
			if strings.TrimSpace(kv[len("GIT_SSH_COMMAND="):]) != "" {
				hasSSHCommand = true
			}
		}
		out = append(out, kv)
	}

	out = append(out, "GIT_TERMINAL_PROMPT=0")
	if !hasSSHCommand {
		out = append(out, "GIT_SSH_COMMAND="+KiloNonInteractiveSSHCommand)
	}
	out = append(out, extra...)
	return out
}

// markSpotlightNoIndex writes a .metadata_never_index marker inside the worktrees
// root on macOS so Spotlight (mds_stores/mdworker) does not index sandbox worktrees.
func markSpotlightNoIndex(worktreesRoot string) {
	if runtime.GOOS != "darwin" || strings.TrimSpace(worktreesRoot) == "" {
		return
	}
	markerPath := filepath.Join(worktreesRoot, spotlightNoIndexMarker)
	f, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		_ = f.Close()
	}
}

// isRootEnvFile matches ".env" or ".env.<qualifier>" (e.g. ".env.local",
// ".env.development") while excluding ".envrc" or ".environment".
func isRootEnvFile(name string) bool {
	return name == ".env" || strings.HasPrefix(name, ".env.")
}

// syncWorktreeEnvFiles atomically copies root-level .env / .env.* files from
// repoRoot into worktreeDir without overwriting existing files in the worktree.
// Any copied file that is not tracked in the worktree is added to .git/info/exclude
// so a freshly created worktree remains clean in `git status --porcelain`.
func syncWorktreeEnvFiles(repoRoot, worktreeDir string) ([]string, error) {
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return nil, err
	}

	var copied []string
	for _, entry := range entries {
		name := entry.Name()
		if !isRootEnvFile(name) || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		srcPath := filepath.Join(repoRoot, name)
		srcInfo, statErr := os.Lstat(srcPath)
		if statErr != nil || !srcInfo.Mode().IsRegular() {
			continue
		}

		dstPath := filepath.Join(worktreeDir, name)
		if err := copyFileExclusive(srcPath, dstPath, srcInfo.Mode().Perm()); err != nil {
			continue
		}
		ensureGitExclude(repoRoot, name)
		copied = append(copied, name)
	}
	return copied, nil
}

// cleanupSyncedEnvFiles removes untracked .env* files in worktreeDir that still
// match repoRoot or are untracked before a non-forced `git worktree remove`.
func cleanupSyncedEnvFiles(repoRoot, worktreeDir string) {
	entries, err := os.ReadDir(worktreeDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !isRootEnvFile(name) || entry.IsDir() {
			continue
		}
		// Only remove if it exists in repoRoot (i.e., was synced from root) and is not tracked by git.
		if _, err := os.Stat(filepath.Join(repoRoot, name)); err != nil {
			continue
		}
		if tracked, err := runGit(worktreeDir, "ls-files", "--", name); err == nil && strings.TrimSpace(tracked) == "" {
			_ = os.Remove(filepath.Join(worktreeDir, name))
		}
	}
}

func copyFileExclusive(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if perm == 0 {
		perm = 0o600
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	return closeErr
}

// resolveWorktreeSetupScript checks .hoxi/setup.sh, .hoxi/setup-script.sh, and
// .hoxi/setup-script in repoRoot (plus .ps1/.cmd/.bat on Windows).
func resolveWorktreeSetupScript(repoRoot string) string {
	hoxiDir := filepath.Join(repoRoot, ".hoxi")
	names := []string{"setup.sh", "setup-script.sh", "setup-script"}
	if runtime.GOOS == "windows" {
		names = []string{"setup.ps1", "setup.cmd", "setup.bat", "setup.sh", "setup-script.sh", "setup-script"}
	}
	for _, name := range names {
		candidate := filepath.Join(hoxiDir, name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}

func resolveWindowsSh() string {
	if p, err := exec.LookPath("sh"); err == nil && p != "" {
		return p
	}
	for _, k := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		root := strings.TrimSpace(os.Getenv(k))
		if root == "" {
			continue
		}
		for _, sub := range []string{
			filepath.Join(root, "Git", "bin", "sh.exe"),
			filepath.Join(root, "Git", "usr", "bin", "sh.exe"),
			filepath.Join(root, "Programs", "Git", "bin", "sh.exe"),
		} {
			if st, err := os.Stat(sub); err == nil && !st.IsDir() {
				return sub
			}
		}
	}
	return ""
}

func newSetupScriptCommand(ctx context.Context, scriptPath string) *exec.Cmd {
	lower := strings.ToLower(scriptPath)
	if strings.HasSuffix(lower, ".ps1") {
		return subproc.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	}
	if runtime.GOOS == "windows" {
		if strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat") {
			return subproc.CommandContext(ctx, "cmd", "/c", scriptPath)
		}
		if sh := resolveWindowsSh(); sh != "" {
			return subproc.CommandContext(ctx, sh, scriptPath)
		}
		return subproc.CommandContext(ctx, "cmd", "/c", scriptPath)
	}
	return subproc.CommandContext(ctx, "sh", scriptPath)
}

func newVerifyShellCommand(ctx context.Context, cmdStr string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		if sh := resolveWindowsSh(); sh != "" {
			return subproc.CommandContext(ctx, sh, "-c", cmdStr)
		}
		return subproc.CommandContext(ctx, "cmd", "/c", cmdStr)
	}
	return subproc.CommandContext(ctx, "sh", "-c", cmdStr)
}

type worktreeSetupResult struct {
	Script string
	Status string // "none" | "succeeded" | "failed"
	Output string
	Error  string
}

// executeWorktreeSetupScript runs the optional .hoxi/setup.sh script inside a
// worktree directory and captures its status, bounded output, and error details.
func executeWorktreeSetupScript(repoRoot, worktreeDir, sessionID, branch string, port int) worktreeSetupResult {
	scriptPath := resolveWorktreeSetupScript(repoRoot)
	if scriptPath == "" {
		return worktreeSetupResult{Status: "none"}
	}
	scriptBase := filepath.Base(scriptPath)
	ctx, cancel := context.WithTimeout(context.Background(), setupScriptTimeout)
	defer cancel()

	if port <= 0 {
		port = allocateWorktreePort(sessionID)
	}
	cmd := newSetupScriptCommand(ctx, scriptPath)
	cmd.Dir = worktreeDir
	cmd.Env = nonInteractiveGitEnv(
		"HOXI_WORKTREE_DIR="+worktreeDir,
		"HOXI_REPO_ROOT="+repoRoot,
		"HOXI_SESSION_ID="+sessionID,
		"HOXI_BRANCH="+branch,
		"PORT="+strconv.Itoa(port),
		"HOXI_PORT="+strconv.Itoa(port),
	)
	out, err := cmd.CombinedOutput()
	if len(out) > maxSetupOutputBytes {
		out = out[len(out)-maxSetupOutputBytes:]
	}
	outputStr := strings.TrimSpace(string(out))
	if err != nil {
		errMsg := fmt.Sprintf("worktree setup script (%s) failed: %v", scriptBase, err)
		if outputStr != "" {
			errMsg = fmt.Sprintf("worktree setup script (%s) failed: %s", scriptBase, outputStr)
		}
		return worktreeSetupResult{
			Script: scriptBase,
			Status: "failed",
			Output: outputStr,
			Error:  errMsg,
		}
	}
	return worktreeSetupResult{
		Script: scriptBase,
		Status: "succeeded",
		Output: outputStr,
	}
}

// runWorktreeSetupScript executes the optional .hoxi/setup.sh script inside a
// newly created worktree directory.
func runWorktreeSetupScript(repoRoot, worktreeDir, sessionID, branch string) error {
	res := executeWorktreeSetupScript(repoRoot, worktreeDir, sessionID, branch, 0)
	if res.Status == "failed" {
		return errors.New(res.Error)
	}
	return nil
}
