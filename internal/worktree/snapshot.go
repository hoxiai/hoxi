package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func isIgnoredUntrackedWorktreePath(rel string) bool {
	slash := strings.Trim(filepath.ToSlash(strings.TrimSpace(rel)), "/")
	if slash == "" {
		return true
	}
	if strings.HasPrefix(slash, ".hoxi/") || slash == ".hoxi" ||
		strings.HasPrefix(slash, ".claude/") || slash == ".claude" ||
		strings.HasPrefix(slash, ".opencode/memory/sessions/") {
		return true
	}
	for _, seg := range strings.Split(slash, "/") {
		switch seg {
		case ".hoxi", ".claude", "node_modules", ".pnpm-store",
			"go-build", "go-build-cache", ".gocache", ".gomodcache",
			".dart_tool", ".pub-cache", "Pods", "DerivedData",
			".venv", "venv", "__pycache__":
			return true
		}
	}
	return false
}

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
	if _, err = runGit(dir, "commit", "-m", appendHoxiCoAuthorFor(dir, "chore(hoxi): snapshot before apply ("+sessionID+")")); err != nil {
		return fmt.Errorf("commit worktree snapshot: %w", err)
	}
	return nil
}

const maxSnapshotUntrackedFileSize = 4 * 1024 * 1024

func collectSnapshotCandidatePaths(repoRoot string) []string {
	seen := make(map[string]struct{})
	var paths []string
	var totalBytes int64
	addCandidate := func(rel string) {
		rel = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(rel)), "./")
		if rel == "" || strings.HasPrefix(rel, ".hoxi/") || strings.HasPrefix(rel, ".claude/") || strings.HasPrefix(rel, ".opencode/") || isIgnoredUntrackedWorktreePath(rel) {
			return
		}
		if _, exists := seen[rel]; exists {
			return
		}
		absPath := filepath.Join(repoRoot, filepath.FromSlash(rel))
		info, err := os.Lstat(absPath)
		if err != nil || info.IsDir() {
			return
		}
		if info.Mode()&os.ModeSymlink == 0 && !info.Mode().IsRegular() {
			return
		}
		if info.Size() > maxSnapshotUntrackedFileSize || totalBytes+info.Size() > maxPortableUntrackedBytes {
			return
		}
		totalBytes += info.Size()
		seen[rel] = struct{}{}
		paths = append(paths, rel)
	}

	if stagedNew, err := runGitRaw(repoRoot, "diff", "--cached", "--name-only", "--diff-filter=A", "-z"); err == nil && stagedNew != "" {
		for _, rel := range strings.Split(stagedNew, "\x00") {
			addCandidate(rel)
		}
	}
	for _, rel := range collectBoundedUntrackedFiles(repoRoot, "", nil, maxDiffUntrackedFiles) {
		addCandidate(rel)
	}
	return paths
}

// CreateSnapshot captures the working directory state (tracked + bounded untracked files)
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
	ensureGitExclude(repoRoot, ".hoxi/", ".claude/")

	tmpFile, err := os.CreateTemp("", "hoxi-snap-index-*")
	if err != nil {
		return "", err
	}
	tmpIndex := tmpFile.Name()
	_ = tmpFile.Close()
	_ = os.Remove(tmpIndex)
	defer os.Remove(tmpIndex)

	_, _ = runGitWithIndex(repoRoot, tmpIndex, "read-tree", "HEAD")
	if _, err := runGitWithIndex(repoRoot, tmpIndex, "add", "-u", "--", ".", ":(exclude).hoxi", ":(exclude).claude"); err != nil {
		return "", fmt.Errorf("stage snapshot: %w", err)
	}
	if untrackedPaths := collectSnapshotCandidatePaths(repoRoot); len(untrackedPaths) > 0 {
		addArgs := append([]string{"add", "-A", "--"}, untrackedPaths...)
		if _, err := runGitWithIndex(repoRoot, tmpIndex, addArgs...); err != nil {
			return "", fmt.Errorf("stage untracked snapshot: %w", err)
		}
	}
	if trackedClaude := trackedPathsUnder(repoRoot, ".claude"); len(trackedClaude) > 0 {
		addArgs := append([]string{"add", "-A", "--"}, trackedClaude...)
		if _, err := runGitWithIndex(repoRoot, tmpIndex, addArgs...); err != nil {
			return "", fmt.Errorf("stage tracked .claude snapshot: %w", err)
		}
	}
	tree, err := runGitWithIndex(repoRoot, tmpIndex, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write snapshot tree: %w", err)
	}
	return strings.TrimSpace(tree), nil
}

func trackedPathsUnder(repoRoot, relDir string) []string {
	seen := make(map[string]struct{})
	var paths []string
	collect := func(out string) {
		for _, entry := range strings.Split(out, "\x00") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if _, ok := seen[entry]; ok {
				continue
			}
			seen[entry] = struct{}{}
			paths = append(paths, entry)
		}
	}
	if out, err := runGitRaw(repoRoot, "ls-files", "-z", "--", relDir); err == nil && out != "" {
		collect(out)
	}
	if out, err := runGitRaw(repoRoot, "ls-tree", "-r", "--name-only", "-z", "HEAD", "--", relDir); err == nil && out != "" {
		collect(out)
	}
	return paths
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

	var currentCandidates []string
	if trackedOut, err := runGitRaw(repoRoot, "ls-files", "-c", "-z"); err == nil && trackedOut != "" {
		currentCandidates = append(currentCandidates, strings.Split(trackedOut, "\x00")...)
	}
	currentCandidates = append(currentCandidates, collectSnapshotCandidatePaths(repoRoot)...)
	for _, rel := range currentCandidates {
		if rel == "" || keep[rel] || strings.HasPrefix(rel, ".hoxi/") || strings.HasPrefix(rel, ".opencode/") || isIgnoredUntrackedWorktreePath(rel) {
			continue
		}
		_ = os.Remove(filepath.Join(repoRoot, filepath.FromSlash(rel)))
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
	if unmerged, _ := runGit(repoRoot, "ls-files", "-u"); strings.TrimSpace(unmerged) != "" {
		_, _ = runGit(repoRoot, "read-tree", "HEAD")
	}
	globalRepoDiffCache.invalidate(repoRoot)
	return nil
}

// SnapshotChangedPaths returns the repo-relative slash paths that differ
// between two snapshot tree hashes in dir.
func SnapshotChangedPaths(dir, fromTree, toTree string) ([]string, error) {
	dir = strings.TrimSpace(dir)
	fromTree = strings.TrimSpace(fromTree)
	toTree = strings.TrimSpace(toTree)
	if dir == "" || fromTree == "" || toTree == "" {
		return nil, fmt.Errorf("directory and snapshot trees are required")
	}
	topLevel, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(topLevel) == "" {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot := filepath.Clean(strings.TrimSpace(topLevel))
	if fromTree == toTree {
		if _, err := runGitRaw(repoRoot, "ls-tree", "-d", fromTree); err != nil {
			return nil, fmt.Errorf("list snapshot files: %w", err)
		}
		return nil, nil
	}
	out, err := runGitRaw(repoRoot, "diff-tree", "-r", "--name-only", "-z", fromTree, toTree)
	if err != nil {
		return nil, fmt.Errorf("diff snapshot trees: %w", err)
	}
	seen := make(map[string]struct{})
	var paths []string
	for _, entry := range strings.Split(out, "\x00") {
		rel := strings.Trim(filepath.ToSlash(strings.TrimSpace(entry)), "/")
		if rel == "" || strings.HasPrefix(rel, ".hoxi/") || strings.HasPrefix(rel, ".opencode/") || isIgnoredUntrackedWorktreePath(rel) {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		paths = append(paths, rel)
	}
	return paths, nil
}

// RestoreSnapshotPaths restores only the specified paths in dir to match
// treeHash, removing any target path that did not exist in treeHash and leaving
// all other tracked and untracked files in the shared repository untouched.
func RestoreSnapshotPaths(dir, treeHash string, paths []string) error {
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
	inTree := make(map[string]bool)
	for _, entry := range strings.Split(treeFilesOut, "\x00") {
		if entry != "" {
			inTree[entry] = true
		}
	}

	seen := make(map[string]bool)
	var toCheckout, toRemove []string
	for _, raw := range paths {
		rel := normalizeSnapshotRepoRelPath(repoRoot, raw)
		if rel == "" || seen[rel] || strings.HasPrefix(rel, ".git/") || rel == ".git" ||
			strings.HasPrefix(rel, ".hoxi/") || strings.HasPrefix(rel, ".opencode/") || isIgnoredUntrackedWorktreePath(rel) {
			continue
		}
		seen[rel] = true
		if inTree[rel] {
			toCheckout = append(toCheckout, rel)
		} else {
			toRemove = append(toRemove, rel)
		}
	}
	for _, rel := range toRemove {
		_ = os.Remove(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	}
	if len(toCheckout) > 0 {
		tmpFile, err := os.CreateTemp("", "hoxi-restore-paths-index-*")
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
		checkoutArgs := append([]string{"checkout-index", "-f", "--"}, toCheckout...)
		if _, err := runGitWithIndex(repoRoot, tmpIndex, checkoutArgs...); err != nil {
			return fmt.Errorf("checkout snapshot files: %w", err)
		}
	}
	globalRepoDiffCache.invalidate(repoRoot)
	return nil
}

func normalizeSnapshotRepoRelPath(repoRoot, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		rel, err := filepath.Rel(repoRoot, filepath.Clean(trimmed))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
			return ""
		}
		return filepath.ToSlash(rel)
	}
	cleaned := filepath.ToSlash(filepath.Clean(trimmed))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return ""
	}
	return cleaned
}

func runGitWithIndex(workDir, indexPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmdArgs := append([]string{"-c", "core.autocrlf=false", "-C", workDir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = nonInteractiveGitEnv("GIT_INDEX_FILE=" + indexPath)
	stdout := &cappedGitOutputBuffer{limit: maxGitCommandOutputBytes}
	stderr := &cappedGitOutputBuffer{limit: maxGitCommandOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("%s: %w", errText, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

const maxPortableUntrackedBytes = 20 * 1024 * 1024

// PortableUntrackedFile holds an untracked working-tree file for cross-worktree transfer.
type PortableUntrackedFile struct {
	RelPath string
	Mode    os.FileMode
	Data    []byte
}

// PortableGitSnapshot holds staged patches, unstaged patches, and untracked files
// so uncommitted local changes can be portably applied to a newly provisioned worktree.
type PortableGitSnapshot struct {
	StagedPatch    string
	UnstagedPatch  string
	UntrackedFiles []PortableUntrackedFile
}

// CapturePortableSnapshot captures staged diff, unstaged diff, and untracked files from dir.
func CapturePortableSnapshot(dir string) (*PortableGitSnapshot, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("directory is required")
	}
	topLevel, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(topLevel) == "" {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot := filepath.Clean(strings.TrimSpace(topLevel))

	staged, _ := runGitRaw(repoRoot, "diff", "--cached", "--binary")
	unstaged, _ := runGitRaw(repoRoot, "diff", "--binary")

	var untracked []PortableUntrackedFile
	var totalBytes int64
	for _, entry := range collectBoundedUntrackedFiles(repoRoot, "", nil, maxDiffUntrackedFiles) {
		if entry == "" || strings.HasPrefix(entry, ".hoxi/") || strings.HasPrefix(entry, ".opencode/") || isIgnoredUntrackedWorktreePath(entry) || isRootEnvFile(entry) {
			continue
		}
		absPath := filepath.Join(repoRoot, filepath.FromSlash(entry))
		info, statErr := os.Lstat(absPath)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > maxSnapshotUntrackedFileSize {
			continue
		}
		if totalBytes+info.Size() > maxPortableUntrackedBytes {
			break
		}
		data, readErr := os.ReadFile(absPath)
		if readErr != nil {
			continue
		}
		totalBytes += int64(len(data))
		untracked = append(untracked, PortableUntrackedFile{
			RelPath: entry,
			Mode:    info.Mode().Perm(),
			Data:    data,
		})
	}

	return &PortableGitSnapshot{
		StagedPatch:    staged,
		UnstagedPatch:  unstaged,
		UntrackedFiles: untracked,
	}, nil
}

// ApplyPortableSnapshot replays a portable snapshot onto targetDir.
func ApplyPortableSnapshot(targetDir string, snap *PortableGitSnapshot) error {
	if snap == nil {
		return nil
	}
	targetDir = strings.TrimSpace(targetDir)
	if targetDir == "" {
		return fmt.Errorf("target directory is required")
	}
	topLevel, err := runGit(targetDir, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(topLevel) == "" {
		return fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot := filepath.Clean(strings.TrimSpace(topLevel))

	if strings.TrimSpace(snap.StagedPatch) != "" {
		if err := applyGitPatch(repoRoot, snap.StagedPatch, "--index"); err != nil {
			_ = applyGitPatch(repoRoot, snap.StagedPatch)
		}
	}
	if strings.TrimSpace(snap.UnstagedPatch) != "" {
		_ = applyGitPatch(repoRoot, snap.UnstagedPatch)
	}
	for _, uf := range snap.UntrackedFiles {
		cleaned := filepath.Clean(filepath.FromSlash(uf.RelPath))
		if cleaned == "." || strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
			continue
		}
		dstPath := filepath.Join(repoRoot, cleaned)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			continue
		}
		mode := uf.Mode
		if mode == 0 {
			mode = 0o644
		}
		_ = os.WriteFile(dstPath, uf.Data, mode)
	}
	globalRepoDiffCache.invalidate(repoRoot)
	return nil
}

// TransferWorkingState captures uncommitted staged/unstaged/untracked changes from srcDir
// and applies them to dstDir without mutating srcDir.
func TransferWorkingState(srcDir, dstDir string) error {
	srcDir = strings.TrimSpace(srcDir)
	dstDir = strings.TrimSpace(dstDir)
	if srcDir == "" || dstDir == "" || filepath.Clean(srcDir) == filepath.Clean(dstDir) {
		return nil
	}
	snap, err := CapturePortableSnapshot(srcDir)
	if err != nil {
		return err
	}
	return ApplyPortableSnapshot(dstDir, snap)
}

func applyGitPatch(workDir, patch string, extraArgs ...string) error {
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	run := func(threeWay bool) error {
		ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
		defer cancel()
		args := []string{"-c", "core.autocrlf=false", "-C", workDir, "apply", "--whitespace=nowarn"}
		if threeWay {
			args = append(args, "--3way")
		}
		args = append(args, extraArgs...)
		args = append(args, "-")
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = nonInteractiveGitEnv()
		cmd.Stdin = strings.NewReader(patch)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
		}
		return nil
	}
	if err := run(false); err != nil {
		return run(true)
	}
	return nil
}
