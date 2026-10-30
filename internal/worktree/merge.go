package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/subproc"
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
	cmd := subproc.CommandContext(ctx, "git", "-c", "core.autocrlf=false", "-C", dir, "apply", "--3way", "--whitespace=nowarn", "-")
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

func (m *worktreeManager) Apply(sessionID, mode string) error {
	return m.ApplyWithMessage(sessionID, mode, "")
}

func (m *worktreeManager) ApplyWithMessage(sessionID, mode, message string) error {
	sessionID = sanitizeSessionID(sessionID)
	m.roots.Lock()
	guard := m.roots.applyGuard
	m.roots.Unlock()
	apply := func() error { return m.apply(sessionID, mode, message) }
	if guard != nil {
		return guard(sessionID, apply)
	}
	return apply()
}

func (m *worktreeManager) apply(sessionID, mode, message string) error {
	m.ops.Lock()
	defer m.ops.Unlock()
	sessionID = sanitizeSessionID(sessionID)
	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			br, _ := runGit(candidate, "rev-parse", "--abbrev-ref", "HEAD")
			if br == "" {
				br = "hoxi/" + sessionID
			}
			meta = worktreeMeta{
				SessionID: sessionID,
				Isolated:  true,
				Directory: candidate,
				Branch:    br,
			}
			ok = true
		}
	}
	if !ok || !meta.Isolated {
		return errors.New("session does not have an isolated worktree")
	}
	if branch, err := runGit(meta.Directory, "symbolic-ref", "--short", "HEAD"); err == nil {
		meta.Branch = branch
	}

	if err := snapshotWorktreeWithMessage(meta.Directory, sessionID, message); err != nil {
		return err
	}
	defer func() {
		globalRepoDiffCache.invalidate(meta.Directory)
		globalRepoDiffCache.invalidate(m.rootDir)
	}()

	if mode == "merge" {
		// Resolve attribution against the session worktree that produced the
		// change, not the project repo the merge commit lands in.
		mergeMsg := strings.TrimSpace(message)
		if mergeMsg == "" {
			mergeMsg = "Merge hoxi session " + sessionID
		}
		_, err := runGit(m.rootDir, "merge", "--no-ff", "-m", appendHoxiCoAuthorFor(meta.Directory, mergeMsg), meta.Branch)
		return err
	}

	// Default mode: "patch" — applies changes cleanly onto main workspace without polluting commit history
	base, err := runGit(m.rootDir, "merge-base", "HEAD", meta.Branch)
	if err != nil || strings.TrimSpace(base) == "" {
		return fmt.Errorf("resolve apply base: %v", err)
	}
	diffPatch, err := runGitRaw(meta.Directory, "diff", "--binary", strings.TrimSpace(base)+"..HEAD")
	if err != nil {
		return fmt.Errorf("generate worktree patch: %w", err)
	}
	if strings.TrimSpace(diffPatch) == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := subproc.CommandContext(ctx, "git", "-C", m.rootDir, "apply", "--3way", "-")
	cmd.Env = nonInteractiveGitEnv()
	cmd.Stdin = strings.NewReader(diffPatch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply --3way failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
