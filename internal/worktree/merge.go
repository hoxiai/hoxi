package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrMergeConflict marks a MergeWorkingState call that could not combine the
// changes cleanly; the target directory is left as it was.
var ErrMergeConflict = errors.New("merge conflict")

// MergeWorkingState folds the uncommitted changes of each source worktree into
// dstDir, in order. Sources and target must share history (sibling worktrees of
// one repository). Changes already present in the target, such as those a
// source inherited from an earlier source, merge cleanly through a three-way
// apply. On any conflict the target is restored and ErrMergeConflict returned,
// unlike TransferWorkingState, which skips hunks that do not apply.
func MergeWorkingState(dstDir string, srcDirs ...string) error {
	dstDir = strings.TrimSpace(dstDir)
	if dstDir == "" {
		return errors.New("target directory is required")
	}
	before, err := CreateSnapshot(dstDir)
	if err != nil {
		return err
	}
	for _, src := range srcDirs {
		src = strings.TrimSpace(src)
		if src == "" || filepath.Clean(src) == filepath.Clean(dstDir) {
			continue
		}
		if err := mergeOneWorkingState(src, dstDir); err != nil {
			if restoreErr := RestoreSnapshot(dstDir, before); restoreErr != nil {
				return fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
			}
			return err
		}
	}
	globalRepoDiffCache.invalidate(dstDir)
	return nil
}

func mergeOneWorkingState(srcDir, dstDir string) error {
	patch, err := runGitRaw(srcDir, "diff", "--binary", "--full-index", "HEAD")
	if err != nil {
		return fmt.Errorf("read changes in %s: %w", srcDir, err)
	}
	if strings.TrimSpace(patch) != "" {
		if err := applyThreeWay(dstDir, patch); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrMergeConflict, filepath.Base(srcDir), err)
		}
	}
	snap, err := CapturePortableSnapshot(srcDir)
	if err != nil {
		return err
	}
	dstRoot, err := runGit(dstDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	for _, file := range snap.UntrackedFiles {
		rel := filepath.Clean(filepath.FromSlash(file.RelPath))
		if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		target := filepath.Join(dstRoot, rel)
		if existing, readErr := os.ReadFile(target); readErr == nil {
			if !bytes.Equal(existing, file.Data) {
				return fmt.Errorf("%w: %s: both sides created %s", ErrMergeConflict, filepath.Base(srcDir), file.RelPath)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, file.Data, file.Mode.Perm()|0o600); err != nil {
			return err
		}
	}
	return nil
}

// applyThreeWay applies patch with --3way, which needs the index to match the
// working tree for the touched paths, so the index is synced first.
func applyThreeWay(dir, patch string) error {
	if _, err := runGit(dir, "add", "-u"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "core.autocrlf=false", "-C", dir, "apply", "--3way", "--whitespace=nowarn", "-")
	cmd.Env = nonInteractiveGitEnv()
	cmd.Stdin = strings.NewReader(patch)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// HasCommittedHead reports whether dir is itself the root of a git repository
// with at least one commit, the precondition for creating isolated worktrees.
// A subdirectory of a repository does not qualify: git worktree would check
// out the whole parent repository, so the session would silently work on a
// different project than the folder the user opened.
func HasCommittedHead(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	topLevel, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(topLevel) == "" {
		return false
	}
	if !sameRootPath(topLevel, dir) {
		return false
	}
	_, err = runGit(dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	return err == nil
}

// sameRootPath compares two directories after resolving symlinks.
func sameRootPath(left, right string) bool {
	leftCanonical, leftErr := filepath.EvalSymlinks(left)
	rightCanonical, rightErr := filepath.EvalSymlinks(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return filepath.Clean(leftCanonical) == filepath.Clean(rightCanonical)
}
