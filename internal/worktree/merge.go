package worktree

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/subproc"
)

// ErrMergeConflict marks a MergeWorkingState call that could not combine the
// changes cleanly; the target directory is left as it was.
var ErrMergeConflict = errors.New("merge conflict")

type txFileEntry struct {
	rel        string
	existed    bool
	isSymlink  bool
	linkTarget string
	offset     int64
	size       int64
	mode       os.FileMode
}

type workingTreeTransaction struct {
	repoRoot        string
	tmpDir          string
	indexPath       string
	indexExisted    bool
	indexMode       os.FileMode
	indexBackupPath string
	packPath        string
	files           []txFileEntry
}

func resolveGitIndexFile(repoRoot string) (string, error) {
	out, err := runGit(repoRoot, "rev-parse", "--git-path", "index")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("resolve git index path: %w", err)
	}
	idxPath := strings.TrimSpace(out)
	if !filepath.IsAbs(idxPath) {
		idxPath = filepath.Join(repoRoot, idxPath)
	}
	return filepath.Clean(idxPath), nil
}

func writeReaderAtomic(r io.Reader, dst string, mode os.FileMode, syncFile bool) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	out, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".hoxi-tx-*")
	if err != nil {
		return err
	}
	tmpDst := out.Name()
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o600
	}
	if err := out.Chmod(perm); err != nil {
		_ = out.Close()
		_ = os.Remove(tmpDst)
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		_ = os.Remove(tmpDst)
		return err
	}
	if syncFile {
		if err := out.Sync(); err != nil {
			_ = out.Close()
			_ = os.Remove(tmpDst)
			return err
		}
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmpDst)
		return err
	}
	if err := os.Rename(tmpDst, dst); err != nil {
		_ = os.Remove(tmpDst)
		return err
	}
	return nil
}

func copyFileExact(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeReaderAtomic(in, dst, mode, false)
}

func copyFileDurable(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeReaderAtomic(in, dst, mode, true)
}

func hasDotGitSegment(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".git" {
			return true
		}
	}
	return false
}

func beginWorkingTreeTransaction(dir string, rawPaths []string) (*workingTreeTransaction, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("directory is required")
	}
	topLevel, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(topLevel) == "" {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot := filepath.Clean(strings.TrimSpace(topLevel))
	indexPath, err := resolveGitIndexFile(repoRoot)
	if err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "hoxi-merge-tx-*")
	if err != nil {
		return nil, err
	}
	tx := &workingTreeTransaction{
		repoRoot:  repoRoot,
		tmpDir:    tmpDir,
		indexPath: indexPath,
	}
	var packFile *os.File
	var packWriter *bufio.Writer
	var copyBuf []byte
	var packOffset int64
	cleanupOnErr := func(retErr error) (*workingTreeTransaction, error) {
		if packFile != nil {
			_ = packFile.Close()
		}
		_ = os.RemoveAll(tmpDir)
		return nil, retErr
	}

	if _, lockErr := os.Lstat(indexPath + ".lock"); lockErr == nil {
		return cleanupOnErr(fmt.Errorf("git index is locked by another process: %s.lock", indexPath))
	} else if !os.IsNotExist(lockErr) {
		return cleanupOnErr(fmt.Errorf("stat git index lock: %w", lockErr))
	}

	if idxInfo, err := os.Lstat(indexPath); err == nil {
		tx.indexExisted = true
		tx.indexMode = idxInfo.Mode().Perm()
		if tx.indexMode == 0 {
			tx.indexMode = 0o600
		}
		tx.indexBackupPath = filepath.Join(tmpDir, "index.bak")
		if err := copyFileExact(indexPath, tx.indexBackupPath, 0o600); err != nil {
			return cleanupOnErr(fmt.Errorf("backup git index: %w", err))
		}
	} else if !os.IsNotExist(err) {
		return cleanupOnErr(fmt.Errorf("stat git index: %w", err))
	}

	seen := make(map[string]struct{}, len(rawPaths))
	for _, raw := range rawPaths {
		if raw == "" {
			continue
		}
		rel := normalizeSnapshotRepoRelPath(repoRoot, raw)
		if rel == "" || hasDotGitSegment(rel) {
			return cleanupOnErr(fmt.Errorf("invalid or protected path in merge transaction: %q", raw))
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}

		absPath := filepath.Join(repoRoot, filepath.FromSlash(rel))
		info, err := os.Lstat(absPath)
		if os.IsNotExist(err) {
			tx.files = append(tx.files, txFileEntry{rel: rel, existed: false})
			continue
		}
		if err != nil {
			return cleanupOnErr(fmt.Errorf("inspect %s before merge: %w", rel, err))
		}
		if info.IsDir() {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, linkErr := os.Readlink(absPath)
			if linkErr != nil {
				return cleanupOnErr(fmt.Errorf("read symlink %s before merge: %w", rel, linkErr))
			}
			tx.files = append(tx.files, txFileEntry{
				rel:        rel,
				existed:    true,
				isSymlink:  true,
				linkTarget: linkTarget,
				mode:       info.Mode(),
			})
			continue
		}
		if !info.Mode().IsRegular() {
			return cleanupOnErr(fmt.Errorf("unsupported file mode on %s", rel))
		}
		if packFile == nil {
			tx.packPath = filepath.Join(tmpDir, "backup.pack")
			packFile, err = os.OpenFile(tx.packPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return cleanupOnErr(fmt.Errorf("create backup pack: %w", err))
			}
			packWriter = bufio.NewWriterSize(packFile, 256*1024)
			copyBuf = make([]byte, 32*1024)
		}
		in, openErr := os.Open(absPath)
		if openErr != nil {
			return cleanupOnErr(fmt.Errorf("open %s before merge: %w", rel, openErr))
		}
		written, copyErr := io.CopyBuffer(packWriter, in, copyBuf)
		_ = in.Close()
		if copyErr != nil {
			return cleanupOnErr(fmt.Errorf("backup %s before merge: %w", rel, copyErr))
		}
		perm := info.Mode().Perm()
		if perm == 0 {
			perm = 0o644
		}
		tx.files = append(tx.files, txFileEntry{
			rel:     rel,
			existed: true,
			offset:  packOffset,
			size:    written,
			mode:    perm,
		})
		packOffset += written
	}
	if packFile != nil {
		if err := packWriter.Flush(); err != nil {
			return cleanupOnErr(fmt.Errorf("flush backup pack: %w", err))
		}
		if err := packFile.Close(); err != nil {
			packFile = nil
			return cleanupOnErr(fmt.Errorf("close backup pack: %w", err))
		}
	}
	return tx, nil
}

func (tx *workingTreeTransaction) Close() {
	if tx != nil && tx.tmpDir != "" {
		_ = os.RemoveAll(tx.tmpDir)
	}
}

func (tx *workingTreeTransaction) Rollback() error {
	if tx == nil {
		return nil
	}
	var errs []error
	var packIn *os.File
	if tx.packPath != "" {
		f, err := os.Open(tx.packPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("open backup pack: %w", err))
		} else {
			packIn = f
			defer packIn.Close()
		}
	}
	for _, entry := range tx.files {
		absPath := filepath.Join(tx.repoRoot, filepath.FromSlash(entry.rel))
		if !entry.existed {
			if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("remove %s: %w", entry.rel, err))
			}
			pruneEmptyParents(tx.repoRoot, filepath.Dir(absPath))
			continue
		}
		if entry.isSymlink {
			if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
				errs = append(errs, fmt.Errorf("mkdir %s: %w", entry.rel, err))
				continue
			}
			_ = os.RemoveAll(absPath)
			if err := os.Symlink(entry.linkTarget, absPath); err != nil {
				errs = append(errs, fmt.Errorf("restore symlink %s: %w", entry.rel, err))
			}
			continue
		}
		if packIn == nil {
			errs = append(errs, fmt.Errorf("restore %s: backup pack unavailable", entry.rel))
			continue
		}
		if info, statErr := os.Lstat(absPath); statErr == nil && info.IsDir() {
			_ = os.RemoveAll(absPath)
		}
		section := io.NewSectionReader(packIn, entry.offset, entry.size)
		if err := writeReaderAtomic(section, absPath, entry.mode, false); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", entry.rel, err))
		}
	}

	if _, lockErr := os.Lstat(tx.indexPath + ".lock"); os.IsNotExist(lockErr) {
		if !tx.indexExisted {
			if err := os.Remove(tx.indexPath); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("remove index: %w", err))
			}
		} else {
			if err := copyFileDurable(tx.indexBackupPath, tx.indexPath, tx.indexMode); err != nil {
				errs = append(errs, fmt.Errorf("restore git index: %w", err))
			}
		}
	}
	globalRepoDiffCache.invalidate(tx.repoRoot)
	return errors.Join(errs...)
}

func pruneEmptyParents(repoRoot, dir string) {
	cleanRoot := filepath.Clean(repoRoot)
	cur := filepath.Clean(dir)
	for cur != cleanRoot && strings.HasPrefix(cur, cleanRoot+string(filepath.Separator)) {
		if err := os.Remove(cur); err != nil {
			return
		}
		cur = filepath.Dir(cur)
	}
}

func collectMergeWorkingStatePaths(dstDir string, srcDirs ...string) ([]string, error) {
	var paths []string
	dstHead, _ := runGit(dstDir, "rev-parse", "--verify", "HEAD")
	dstHead = strings.TrimSpace(dstHead)
	for _, src := range srcDirs {
		src = strings.TrimSpace(src)
		if src == "" || filepath.Clean(src) == filepath.Clean(dstDir) {
			continue
		}
		diffBase := "HEAD"
		if dstHead != "" {
			if mb, err := runGit(src, "merge-base", "HEAD", dstHead); err == nil && strings.TrimSpace(mb) != "" {
				diffBase = strings.TrimSpace(mb)
			}
		}
		diffNames, err := runGitRaw(src, "diff", "--name-only", "--no-renames", "-z", diffBase)
		if err != nil {
			return nil, fmt.Errorf("read changes in %s: %w", src, err)
		}
		if diffNames != "" {
			paths = append(paths, strings.Split(diffNames, "\x00")...)
		}
		srcRoot, err := runGit(src, "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, err
		}
		paths = append(paths, collectBoundedUntrackedFiles(strings.TrimSpace(srcRoot), "", nil, maxDiffUntrackedFiles)...)
	}
	return paths, nil
}

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
	touchedPaths, err := collectMergeWorkingStatePaths(dstDir, srcDirs...)
	if err != nil {
		return err
	}
	tx, err := beginWorkingTreeTransaction(dstDir, touchedPaths)
	if err != nil {
		return err
	}
	defer tx.Close()
	for _, src := range srcDirs {
		src = strings.TrimSpace(src)
		if src == "" || filepath.Clean(src) == filepath.Clean(dstDir) {
			continue
		}
		if err := mergeOneWorkingState(src, dstDir); err != nil {
			if restoreErr := tx.Rollback(); restoreErr != nil {
				return fmt.Errorf("%w (restore failed: %v)", err, restoreErr)
			}
			return err
		}
	}
	globalRepoDiffCache.invalidate(dstDir)
	return nil
}

func mergeOneWorkingState(srcDir, dstDir string) error {
	diffBase := "HEAD"
	if dstHead, headErr := runGit(dstDir, "rev-parse", "--verify", "HEAD"); headErr == nil && strings.TrimSpace(dstHead) != "" {
		if mb, mbErr := runGit(srcDir, "merge-base", "HEAD", strings.TrimSpace(dstHead)); mbErr == nil && strings.TrimSpace(mb) != "" {
			diffBase = strings.TrimSpace(mb)
		}
	}
	patch, err := runGitRaw(srcDir, "diff", "--binary", "--full-index", diffBase)
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
		if _, err := runGit(m.rootDir, "merge", "--no-ff", "-m", appendHoxiCoAuthorFor(meta.Directory, mergeMsg), meta.Branch); err != nil {
			_, _ = runGit(m.rootDir, "merge", "--abort")
			return fmt.Errorf("%w: %v", ErrMergeConflict, err)
		}
		return nil
	}

	// Default mode: "patch" — applies changes cleanly onto main workspace without polluting commit history
	base, err := runGit(m.rootDir, "merge-base", "HEAD", meta.Branch)
	if err != nil || strings.TrimSpace(base) == "" {
		return fmt.Errorf("resolve apply base: %v", err)
	}
	diffPatch, err := runGitRaw(meta.Directory, "diff", "--binary", "--no-renames", strings.TrimSpace(base)+"..HEAD")
	if err != nil {
		return fmt.Errorf("generate worktree patch: %w", err)
	}
	if strings.TrimSpace(diffPatch) == "" {
		return nil
	}

	changedNames, err := runGitRaw(meta.Directory, "diff", "--name-only", "--no-renames", "-z", strings.TrimSpace(base)+"..HEAD")
	if err != nil {
		return fmt.Errorf("list worktree patch paths: %w", err)
	}
	touchedPaths := strings.Split(changedNames, "\x00")
	tx, err := beginWorkingTreeTransaction(m.rootDir, touchedPaths)
	if err != nil {
		return fmt.Errorf("prepare patch rollback: %w", err)
	}
	defer tx.Close()

	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := subproc.CommandContext(ctx, "git", "-C", m.rootDir, "apply", "--3way", "-")
	cmd.Env = nonInteractiveGitEnv()
	cmd.Stdin = strings.NewReader(diffPatch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if restoreErr := tx.Rollback(); restoreErr != nil {
			return fmt.Errorf("%w: git apply --3way failed: %s (restore failed: %v)", ErrMergeConflict, strings.TrimSpace(string(out)), restoreErr)
		}
		return fmt.Errorf("%w: git apply --3way failed: %s", ErrMergeConflict, strings.TrimSpace(string(out)))
	}
	return nil
}
