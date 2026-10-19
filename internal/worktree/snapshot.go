package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func snapshotWorktree(dir, sessionID string) error {
	statusOut, err := runGit(dir, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("read worktree status: %w", err)
	}
	if strings.TrimSpace(statusOut) == "" {
		return nil
	}
	if _, err = runGit(dir, "add", "-A"); err != nil {
		return fmt.Errorf("stage worktree snapshot: %w", err)
	}
	if _, err = runGit(dir, "commit", "-m", appendHoxiCoAuthor("chore(hoxi): snapshot before apply ("+sessionID+")")); err != nil {
		return fmt.Errorf("commit worktree snapshot: %w", err)
	}
	return nil
}

// CreateSnapshot captures the working directory state (tracked + untracked files)
// as a Git tree object without modifying HEAD or the repository's real index.
func CreateSnapshot(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", fmt.Errorf("directory is required")
	}
	topLevel, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(topLevel) == "" {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot := filepath.Clean(strings.TrimSpace(topLevel))
	ensureGitExclude(repoRoot, ".hoxi/")

	tmpFile, err := os.CreateTemp("", "hoxi-snap-index-*")
	if err != nil {
		return "", err
	}
	tmpIndex := tmpFile.Name()
	_ = tmpFile.Close()
	_ = os.Remove(tmpIndex)
	defer os.Remove(tmpIndex)

	_, _ = runGitWithIndex(repoRoot, tmpIndex, "read-tree", "HEAD")
	if _, err := runGitWithIndex(repoRoot, tmpIndex, "add", "-A"); err != nil {
		return "", fmt.Errorf("stage snapshot: %w", err)
	}
	tree, err := runGitWithIndex(repoRoot, tmpIndex, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write snapshot tree: %w", err)
	}
	return strings.TrimSpace(tree), nil
}

// HeadTree returns the Git tree SHA of HEAD in dir.
func HeadTree(dir string) (string, error) {
	out, err := runGit(dir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RestoreSnapshot restores the working directory in dir to match treeHash,
// removing files created after the snapshot while keeping HEAD and the real index at HEAD.
func RestoreSnapshot(dir, treeHash string) error {
	dir = strings.TrimSpace(dir)
	treeHash = strings.TrimSpace(treeHash)
	if dir == "" || treeHash == "" {
		return fmt.Errorf("directory and snapshot tree are required")
	}
	topLevel, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(topLevel) == "" {
		return fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot := filepath.Clean(strings.TrimSpace(topLevel))

	treeFilesOut, err := runGitRaw(repoRoot, "ls-tree", "-r", "--name-only", "-z", treeHash)
	if err != nil {
		return fmt.Errorf("list snapshot files: %w", err)
	}
	keep := make(map[string]bool)
	for _, entry := range strings.Split(treeFilesOut, "\x00") {
		if entry != "" {
			keep[entry] = true
		}
	}

	currentFilesOut, err := runGitRaw(repoRoot, "ls-files", "-c", "-o", "--exclude-standard", "-z")
	if err == nil {
		for _, rel := range strings.Split(currentFilesOut, "\x00") {
			if rel == "" || keep[rel] || strings.HasPrefix(rel, ".hoxi/") || strings.HasPrefix(rel, ".opencode/") {
				continue
			}
			_ = os.Remove(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		}
	}

	tmpFile, err := os.CreateTemp("", "hoxi-restore-index-*")
	if err != nil {
		return err
	}
	tmpIndex := tmpFile.Name()
	_ = tmpFile.Close()
	_ = os.Remove(tmpIndex)
	defer os.Remove(tmpIndex)

	if _, err := runGitWithIndex(repoRoot, tmpIndex, "read-tree", treeHash); err != nil {
		return fmt.Errorf("read snapshot tree: %w", err)
	}
	if _, err := runGitWithIndex(repoRoot, tmpIndex, "checkout-index", "-a", "-f"); err != nil {
		return fmt.Errorf("checkout snapshot files: %w", err)
	}
	_, _ = runGit(repoRoot, "read-tree", "HEAD")
	return nil
}

func runGitWithIndex(workDir, indexPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmdArgs := append([]string{"-C", workDir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_INDEX_FILE="+indexPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return strings.TrimSpace(string(output)), nil
}
