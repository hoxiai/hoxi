package devpulse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

type uncommittedItem struct {
	targetIdx   int
	dir         string
	repoRoot    string
	file        string
	oldPath     string
	add         int
	del         int
	untracked   bool
	contentHash string
}

func extractUncommittedItems(targetIdx int, workDir, repoRoot string, scan gitUncommittedScan, budget *untrackedBudget) ([]uncommittedItem, string) {
	if repoRoot == "" {
		repoRoot = workDir
	}
	seenInWorktree := make(map[string]struct{})
	var items []uncommittedItem
	if scan.diffOut != "" {
		for _, st := range parseGitNumstatOutput(scan.diffOut) {
			if _, seen := seenInWorktree[st.path]; seen {
				continue
			}
			seenInWorktree[st.path] = struct{}{}
			items = append(items, uncommittedItem{
				targetIdx: targetIdx,
				dir:       workDir,
				repoRoot:  repoRoot,
				file:      st.path,
				oldPath:   st.oldPath,
				add:       st.add,
				del:       st.del,
			})
		}
	}
	for _, rel := range scan.untrackedFiles {
		rel = unquoteGitPath(rel)
		if rel == "" || strings.HasSuffix(rel, "/") || isGeneratedOrLockFile(rel) {
			continue
		}
		if _, seen := seenInWorktree[rel]; seen {
			continue
		}
		seenInWorktree[rel] = struct{}{}
		item := uncommittedItem{
			targetIdx: targetIdx,
			dir:       workDir,
			repoRoot:  repoRoot,
			file:      rel,
			untracked: true,
		}
		if budget.truncated || budget.readCount >= maxUntrackedFilesToCount {
			budget.truncated = true
			items = append(items, item)
			continue
		}
		full := filepath.Join(workDir, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if err != nil || info.IsDir() {
			items = append(items, item)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			budget.readCount++
			item.add = 1
			if linkTarget, linkErr := os.Readlink(full); linkErr == nil {
				item.contentHash = hashAddedFileLines([]byte(linkTarget))
			}
			items = append(items, item)
			continue
		}
		if !info.Mode().IsRegular() {
			items = append(items, item)
			continue
		}
		if info.Size() > maxUntrackedFileSize {
			budget.truncated = true
			items = append(items, item)
			continue
		}
		if budget.readBytes+info.Size() > maxUntrackedTotalBytes {
			budget.truncated = true
			items = append(items, item)
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil || len(data) == 0 || bytes.IndexByte(data, 0) != -1 {
			items = append(items, item)
			continue
		}
		budget.readCount++
		budget.readBytes += int64(len(data))
		lines := bytes.Count(data, []byte{'\n'})
		if data[len(data)-1] != '\n' {
			lines++
		}
		item.add = lines
		item.contentHash = hashAddedFileLines(data)
		items = append(items, item)
	}
	return items, scan.branch
}

func (s *Service) collectDeduplicatedUncommitted(
	ctx context.Context,
	targets []GitTarget,
	results []gitTargetRunResult,
	sideBranchesByRepo map[string][]sideBranchRef,
	committedFilesByRepo map[string]map[string]struct{},
) (additions, deletions int, uncommittedFileSet map[string]struct{}, branch string, truncated bool) {
	uncommittedFileSet = make(map[string]struct{})
	var budget untrackedBudget
	var allItems []uncommittedItem

	for i, t := range targets {
		items, br := extractUncommittedItems(i, t.Dir, t.RepoRoot, results[i].uncommitted, &budget)
		allItems = append(allItems, items...)
		if branch == "" && br != "" {
			branch = br
		}
	}
	truncated = budget.truncated
	if len(allItems) == 0 {
		return 0, 0, uncommittedFileSet, branch, truncated
	}

	// Find files touched in 2+ targets of the same repoRoot (e.g. TransferWorkingState /
	// MergeWorkingState) or overlapping with committed changes on a side branch
	// (e.g. Apply Patch before committing on main).
	targetCountByKey := make(map[string]int, len(allItems))
	needTargetPatch := make(map[int][]string)
	needTargetPatchSet := make(map[int]map[string]struct{})
	addTargetPath := func(idx int, p string) {
		if p == "" {
			return
		}
		if needTargetPatchSet[idx] == nil {
			needTargetPatchSet[idx] = make(map[string]struct{})
		}
		if _, ok := needTargetPatchSet[idx][p]; ok {
			return
		}
		needTargetPatchSet[idx][p] = struct{}{}
		needTargetPatch[idx] = append(needTargetPatch[idx], p)
	}

	for _, it := range allItems {
		targetCountByKey[it.repoRoot+"\x00"+it.file]++
	}

	needSideFiles := make(map[string]map[string]struct{})
	for _, it := range allItems {
		key := it.repoRoot + "\x00" + it.file
		overlapsWorktree := targetCountByKey[key] >= 2
		overlapsSideBranch := false
		if len(sideBranchesByRepo[it.repoRoot]) > 0 {
			if cf := committedFilesByRepo[it.repoRoot]; cf != nil {
				_, overlapsSideBranch = cf[it.file]
			}
		}
		if !overlapsWorktree && !overlapsSideBranch {
			continue
		}
		if !it.untracked {
			addTargetPath(it.targetIdx, it.file)
			addTargetPath(it.targetIdx, it.oldPath)
		}
		if overlapsSideBranch {
			if needSideFiles[it.repoRoot] == nil {
				needSideFiles[it.repoRoot] = make(map[string]struct{})
			}
			needSideFiles[it.repoRoot][it.file] = struct{}{}
			if it.oldPath != "" {
				needSideFiles[it.repoRoot][it.oldPath] = struct{}{}
			}
		}
	}

	// Lazily compute normalized patch hashes only for colliding targets/files.
	targetPatchHashes := make(map[int]map[string]string, len(needTargetPatch))
	for idx, paths := range needTargetPatch {
		if ctx.Err() != nil {
			break
		}
		diffOut := s.runTrackedDiffPatch(ctx, targets[idx].Dir, paths)
		if diffOut != "" {
			targetPatchHashes[idx] = normalizedUnifiedDiffHashes(diffOut)
		}
	}

	// Compute merge-base..sideBranch normalized patch hashes for any target that
	// has uncommitted changes overlapping a side branch's committed files.
	sideHashesByTarget := make(map[int]map[string]map[string]struct{})
	if len(needSideFiles) > 0 && ctx.Err() == nil {
		for idx, t := range targets {
			repoRoot := t.RepoRoot
			if repoRoot == "" {
				repoRoot = t.Dir
			}
			filesMap := needSideFiles[repoRoot]
			branches := sideBranchesByRepo[repoRoot]
			if len(filesMap) == 0 || len(branches) == 0 {
				continue
			}
			var targetPaths []string
			seenTP := make(map[string]struct{})
			for _, it := range allItems {
				if it.targetIdx != idx {
					continue
				}
				if _, ok := filesMap[it.file]; !ok {
					continue
				}
				if _, dup := seenTP[it.file]; !dup {
					seenTP[it.file] = struct{}{}
					targetPaths = append(targetPaths, it.file)
				}
				if it.oldPath != "" {
					if _, dup := seenTP[it.oldPath]; !dup {
						seenTP[it.oldPath] = struct{}{}
						targetPaths = append(targetPaths, it.oldPath)
					}
				}
			}
			if len(targetPaths) == 0 {
				continue
			}
			tHead, headErr := s.runGit(ctx, t.Dir, "rev-parse", "--verify", "HEAD")
			if headErr != nil || tHead == "" {
				continue
			}
			for _, br := range branches {
				if br.tipSHA == tHead {
					continue
				}
				diffRef := br.lastCountedSHA
				if diffRef == "" {
					diffRef = br.name
				}
				mb, mbErr := s.runGit(ctx, t.Dir, "merge-base", "HEAD", diffRef)
				if mbErr != nil || mb == "" {
					continue
				}
				args := append([]string{"-c", "core.quotePath=false", "-c", "diff.noprefix=false", "-c", "diff.mnemonicPrefix=false", "diff", "-M", mb, diffRef, "--"}, targetPaths...)
				diffOut, dErr := s.runGitRaw(ctx, t.Dir, args...)
				if dErr != nil || diffOut == "" {
					continue
				}
				hashes := normalizedUnifiedDiffHashes(diffOut)
				if len(hashes) == 0 {
					continue
				}
				if sideHashesByTarget[idx] == nil {
					sideHashesByTarget[idx] = make(map[string]map[string]struct{})
				}
				for f, h := range hashes {
					if sideHashesByTarget[idx][f] == nil {
						sideHashesByTarget[idx][f] = make(map[string]struct{})
					}
					sideHashesByTarget[idx][f][h] = struct{}{}
				}
			}
		}
	}

	seenUncommittedPatch := make(map[string]struct{})
	for _, it := range allItems {
		patchHash := it.contentHash
		if !it.untracked {
			patchHash = targetPatchHashes[it.targetIdx][it.file]
		}
		if patchHash != "" {
			if brHashes := sideHashesByTarget[it.targetIdx][it.file]; brHashes != nil {
				if _, alreadyCommitted := brHashes[patchHash]; alreadyCommitted {
					continue
				}
			}
			dedupKey := it.repoRoot + "\x00" + it.file + "\x00" + patchHash
			if _, seen := seenUncommittedPatch[dedupKey]; seen {
				continue
			}
			seenUncommittedPatch[dedupKey] = struct{}{}
		}
		additions += it.add
		deletions += it.del
		uncommittedFileSet[it.repoRoot+"\x00"+it.file] = struct{}{}
	}
	return additions, deletions, uncommittedFileSet, branch, truncated
}

func (s *Service) runTrackedDiffPatch(ctx context.Context, workDir string, paths []string) string {
	if len(paths) == 0 || ctx.Err() != nil {
		return ""
	}
	args := append([]string{"-c", "core.quotePath=false", "-c", "diff.noprefix=false", "-c", "diff.mnemonicPrefix=false", "diff", "-M", "HEAD", "--"}, paths...)
	if out, err := s.runGitRaw(ctx, workDir, args...); err == nil {
		return out
	}
	argsEmpty := append([]string{"-c", "core.quotePath=false", "-c", "diff.noprefix=false", "-c", "diff.mnemonicPrefix=false", "diff", "-M", gitEmptyTreeSHA, "--"}, paths...)
	if out, err := s.runGitRaw(ctx, workDir, argsEmpty...); err == nil {
		return out
	}
	return ""
}

func hashAddedFileLines(data []byte) string {
	h := sha256.New()
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		h.Write([]byte{'+'})
		h.Write(line)
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// normalizedUnifiedDiffHashes hashes each file's semantic added/deleted lines
// from a unified diff (ignoring index hashes and hunk line numbers), matching
// hashAddedFileLines for newly added files.
func normalizedUnifiedDiffHashes(rawDiff string) map[string]string {
	out := make(map[string]string)
	if rawDiff == "" {
		return out
	}
	lines := strings.Split(rawDiff, "\n")
	var (
		inFile     bool
		inHunk     bool
		minusPath  string
		plusPath   string
		renameFrom string
		renameTo   string
		hasContent bool
		hasher     = sha256.New()
	)
	flush := func() {
		if !inFile {
			return
		}
		path := plusPath
		if path == "" || path == "/dev/null" {
			path = renameTo
		}
		if path == "" || path == "/dev/null" {
			path = minusPath
		}
		if path != "" && path != "/dev/null" && hasContent {
			out[path] = hex.EncodeToString(hasher.Sum(nil))
		}
	}
	reset := func() {
		inFile = true
		inHunk = false
		minusPath = ""
		plusPath = ""
		renameFrom = ""
		renameTo = ""
		hasContent = false
		hasher.Reset()
	}

	for _, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			reset()
			continue
		}
		if !inFile {
			continue
		}
		if !inHunk {
			switch {
			case strings.HasPrefix(line, "rename from "):
				renameFrom = unquoteGitPath(strings.TrimPrefix(line, "rename from "))
			case strings.HasPrefix(line, "rename to "):
				renameTo = unquoteGitPath(strings.TrimPrefix(line, "rename to "))
				hasher.Write([]byte("rename:" + renameFrom + "->" + renameTo + "\n"))
				hasContent = true
			case strings.HasPrefix(line, "--- a/"):
				minusPath = unquoteGitPath(strings.TrimPrefix(line, "--- a/"))
			case strings.HasPrefix(line, "+++ b/"):
				plusPath = unquoteGitPath(strings.TrimPrefix(line, "+++ b/"))
			case strings.HasPrefix(line, "Binary files "):
				hasher.Write([]byte(line + "\n"))
				hasContent = true
			case strings.HasPrefix(line, "@@ "):
				inHunk = true
			}
			continue
		}
		if strings.HasPrefix(line, "@@ ") || strings.HasPrefix(line, `\ `) {
			continue
		}
		if len(line) > 0 && (line[0] == '+' || line[0] == '-') {
			hasher.Write([]byte(line))
			hasher.Write([]byte{'\n'})
			hasContent = true
		}
	}
	flush()
	return out
}
