package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ReviewScope selects which changes a workspace review covers.
type ReviewScope string

const (
	// ReviewUncommitted covers staged, unstaged, and untracked changes against HEAD.
	ReviewUncommitted ReviewScope = "uncommitted"
	// ReviewBranch covers commits on HEAD that are not yet in the base branch.
	ReviewBranch ReviewScope = "branch"
)

const (
	reviewMaxFiles         = 40
	reviewMaxFileBytes     = 200 << 10
	reviewMaxGitOutput     = 8 << 20
	reviewMaxGuidanceRunes = 10000
	reviewGuidanceFile     = "REVIEW.md"
)

// ReviewFileDiff is one changed file in a workspace review.
type ReviewFileDiff struct {
	File      string
	Before    string
	After     string
	Additions int
	Deletions int
}

// ReviewSnapshot is everything a workspace review sees. Guidance is always read
// from the base revision, never from the code under review, so a change cannot
// rewrite the rules that judge it.
type ReviewSnapshot struct {
	Scope             ReviewScope
	BaseRef           string
	Files             []ReviewFileDiff
	TotalFiles        int
	Guidance          string
	GuidanceTruncated bool
}

type reviewEntry struct {
	path      string
	additions int
	deletions int
	untracked bool
}

// CollectReviewSnapshot gathers the changed files and REVIEW.md guidance for the
// repository (or worktree) that contains dir.
func CollectReviewSnapshot(dir string, scope ReviewScope, baseRef string) (ReviewSnapshot, error) {
	if !filepath.IsAbs(dir) {
		return ReviewSnapshot{}, errors.New("review directory must be an absolute path")
	}
	root, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return ReviewSnapshot{}, fmt.Errorf("not a git repository: %s", dir)
	}
	if _, err := runGit(root, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
		return ReviewSnapshot{}, errors.New("repository has no commits to review against")
	}

	snap := ReviewSnapshot{Scope: scope}
	var baseCommit, guidanceRef string
	switch scope {
	case ReviewUncommitted:
		snap.BaseRef = "HEAD"
		baseCommit, guidanceRef = "HEAD", "HEAD"
	case ReviewBranch:
		base, err := resolveReviewBase(root, baseRef)
		if err != nil {
			return snap, err
		}
		mergeBase, err := runGit(root, "merge-base", base, "HEAD")
		if err != nil || mergeBase == "" {
			return snap, fmt.Errorf("no common ancestor between %s and HEAD", base)
		}
		snap.BaseRef = base
		baseCommit, guidanceRef = mergeBase, base
	default:
		return snap, fmt.Errorf("unknown review scope %q", scope)
	}

	snap.Guidance, snap.GuidanceTruncated = readReviewGuidance(root, guidanceRef)

	entries, err := listReviewEntries(root, scope, baseCommit)
	if err != nil {
		return snap, err
	}
	snap.TotalFiles = len(entries)
	for _, entry := range entries {
		if len(snap.Files) >= reviewMaxFiles {
			break
		}
		file := ReviewFileDiff{
			File:      entry.path,
			Before:    gitShowText(root, baseCommit, entry.path),
			Additions: entry.additions,
			Deletions: entry.deletions,
		}
		if scope == ReviewUncommitted {
			file.After = readWorkingText(root, entry.path)
		} else {
			file.After = gitShowText(root, "HEAD", entry.path)
		}
		snap.Files = append(snap.Files, file)
	}
	return snap, nil
}

// resolveReviewBase returns the requested base branch, or main/master when none
// is given. Names are checked as commits so a bad value never reaches a path.
func resolveReviewBase(root, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	candidates := []string{requested}
	if requested == "" {
		candidates = []string{"main", "master"}
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if strings.HasPrefix(candidate, "-") || strings.ContainsAny(candidate, " \t\n\\") || strings.Contains(candidate, "..") {
			return "", fmt.Errorf("invalid base branch %q", candidate)
		}
		if _, err := runGit(root, "rev-parse", "--verify", "--quiet", candidate+"^{commit}"); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("base branch not found; choose a base branch to review against")
}

func listReviewEntries(root string, scope ReviewScope, baseCommit string) ([]reviewEntry, error) {
	args := []string{"diff", "--numstat", "--no-renames", "-z", baseCommit}
	if scope == ReviewBranch {
		args = append(args, "HEAD")
	}
	args = append(args, "--")
	out, err := runGitBounded(root, reviewMaxGitOutput, args...)
	if err != nil {
		return nil, fmt.Errorf("list changed files: %w", err)
	}
	var entries []reviewEntry
	for _, record := range strings.Split(string(out), "\x00") {
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		entry := reviewEntry{path: parts[2], additions: parseNumstat(parts[0]), deletions: parseNumstat(parts[1])}
		if !safeReviewPath(entry.path) {
			continue
		}
		entries = append(entries, entry)
	}
	if scope != ReviewUncommitted {
		return entries, nil
	}
	untracked, err := runGitBounded(root, reviewMaxGitOutput, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("list untracked files: %w", err)
	}
	for _, rel := range strings.Split(string(untracked), "\x00") {
		if rel == "" || !safeReviewPath(rel) {
			continue
		}
		entries = append(entries, reviewEntry{path: rel, additions: countWorkingLines(root, rel), untracked: true})
	}
	return entries, nil
}

// parseNumstat treats binary markers ("-") and malformed counts as zero.
func parseNumstat(field string) int {
	n := 0
	for _, r := range field {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// safeReviewPath rejects absolute paths and anything that could leave the repo.
func safeReviewPath(rel string) bool {
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return false
	}
	clean := filepath.Clean(rel)
	return clean != "." && !filepath.IsAbs(clean) && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func gitShowText(root, commit, rel string) string {
	data, err := runGitBounded(root, reviewMaxFileBytes, "show", commit+":"+rel)
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return ""
	}
	return string(data)
}

// readWorkingText reads a file from the working tree, skipping symlinks,
// binaries, and oversized files so the review stays bounded.
func readWorkingText(root, rel string) string {
	full := filepath.Join(root, rel)
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() || info.Size() > reviewMaxFileBytes {
		return ""
	}
	data, err := os.ReadFile(full)
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return ""
	}
	return string(data)
}

// countWorkingLines counts text lines; a trailing newline does not add a line.
func countWorkingLines(root, rel string) int {
	text := readWorkingText(root, rel)
	if text == "" {
		return 0
	}
	lines := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		lines++
	}
	return lines
}

func readReviewGuidance(root, ref string) (string, bool) {
	data, err := runGitBounded(root, reviewMaxGitOutput, "show", ref+":"+reviewGuidanceFile)
	if err != nil {
		return "", false
	}
	runes := []rune(strings.TrimSpace(string(data)))
	if len(runes) > reviewMaxGuidanceRunes {
		return string(runes[:reviewMaxGuidanceRunes]), true
	}
	return string(runes), false
}

// runGitBounded runs git under the worktree timeout and non-interactive env,
// returning at most limit bytes of stdout. Exceeding the limit kills git.
func runGitBounded(dir string, limit int64, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = nonInteractiveGitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, limit+1))
	oversized := int64(len(data)) > limit
	if oversized {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if oversized {
		return nil, fmt.Errorf("git %s: output exceeds %d bytes", args[0], limit)
	}
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(stderr.String()), waitErr)
	}
	return data, nil
}
