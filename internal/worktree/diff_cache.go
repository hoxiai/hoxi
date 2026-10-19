package worktree

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	maxSingleFilePatchBytes    = 256 * 1024
	maxGeneratedInlinePatchLen = 64 * 1024
	maxCachedRepoDiffs         = 512
	maxFingerprintFiles        = 128
)

var knownLockfileNames = map[string]struct{}{
	"package-lock.json": {},
	"pnpm-lock.yaml":    {},
	"yarn.lock":         {},
	"bun.lock":          {},
	"bun.lockb":         {},
	"go.sum":            {},
	"cargo.lock":        {},
	"gemfile.lock":      {},
	"poetry.lock":       {},
	"uv.lock":           {},
	"composer.lock":     {},
	"podfile.lock":      {},
	"pubspec.lock":      {},
	"flake.lock":        {},
	"shrinkwrap.yaml":   {},
	"npm-shrinkwrap.json": {},
}

var generatedFileSuffixes = []string{
	".min.js",
	".min.css",
	".bundle.js",
	".js.map",
	".css.map",
	".d.ts.map",
	".pb.go",
	"_pb2.py",
	"_pb2_grpc.py",
	".g.dart",
	".freezed.dart",
	".generated.ts",
	".generated.go",
	".gen.ts",
	".gen.go",
}

// classifyGeneratedFile reports whether relPath is a lockfile, minified bundle,
// or code-generated artifact that should be marked as generatedLike in diffs.
func classifyGeneratedFile(relPath string) bool {
	norm := strings.ToLower(strings.Trim(filepath.ToSlash(strings.TrimSpace(relPath)), "/"))
	if norm == "" {
		return false
	}
	base := norm
	if idx := strings.LastIndexByte(norm, '/'); idx >= 0 {
		base = norm[idx+1:]
	}
	if _, ok := knownLockfileNames[base]; ok {
		return true
	}
	for _, suffix := range generatedFileSuffixes {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	for _, seg := range strings.Split(norm, "/") {
		switch seg {
		case "dist", "build", ".next", "coverage", "__generated__":
			return true
		}
	}
	return false
}

// fileStatStamp returns a lightweight size+mtime+mode stamp for a working tree
// file without reading its contents.
func fileStatStamp(repoDir, relPath string) string {
	full := filepath.Join(repoDir, filepath.FromSlash(relPath))
	info, err := os.Lstat(full)
	if err != nil {
		return "missing"
	}
	return fmt.Sprintf("%d:%d:%d", info.Size(), info.ModTime().UnixNano(), info.Mode())
}

// resolveGitDirFast resolves the repository's .git directory (including linked
// git worktrees where .git is a gitdir pointer file) without spawning git.
func resolveGitDirFast(repoDir string) string {
	dotGit := filepath.Join(repoDir, ".git")
	info, err := os.Lstat(dotGit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return dotGit
	}
	if info.Mode().IsRegular() {
		data, readErr := os.ReadFile(dotGit)
		if readErr == nil {
			line := strings.TrimSpace(string(data))
			if strings.HasPrefix(line, "gitdir:") {
				target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
				if !filepath.IsAbs(target) {
					target = filepath.Clean(filepath.Join(repoDir, target))
				}
				return target
			}
		}
	}
	return ""
}

// resolveGitHeadFast reads the HEAD commit hash directly from the git directory
// when loose refs are available, falling back to git rev-parse HEAD only for packed refs.
func resolveGitHeadFast(repoDir, gitDir string) string {
	if gitDir != "" {
		if rawHead, err := os.ReadFile(filepath.Join(gitDir, "HEAD")); err == nil {
			headLine := strings.TrimSpace(string(rawHead))
			if !strings.HasPrefix(headLine, "ref:") && len(headLine) >= 40 {
				return headLine
			}
			if strings.HasPrefix(headLine, "ref:") {
				refRel := filepath.FromSlash(strings.TrimSpace(strings.TrimPrefix(headLine, "ref:")))
				if refBytes, refErr := os.ReadFile(filepath.Join(gitDir, refRel)); refErr == nil {
					if sha := strings.TrimSpace(string(refBytes)); len(sha) >= 40 {
						return sha
					}
				}
				if commonRaw, commonErr := os.ReadFile(filepath.Join(gitDir, "commondir")); commonErr == nil {
					commonRel := strings.TrimSpace(string(commonRaw))
					if commonRel != "" {
						commonDir := commonRel
						if !filepath.IsAbs(commonDir) {
							commonDir = filepath.Clean(filepath.Join(gitDir, commonDir))
						}
						if refBytes, refErr := os.ReadFile(filepath.Join(commonDir, refRel)); refErr == nil {
							if sha := strings.TrimSpace(string(refBytes)); len(sha) >= 40 {
								return sha
							}
						}
					}
				}
			}
		}
	}
	head, _ := runGit(repoDir, "rev-parse", "HEAD")
	return head
}

type repoFlightCall[T any] struct {
	wg  sync.WaitGroup
	val T
}

type repoFlightGroup[T any] struct {
	mu    sync.Mutex
	calls map[string]*repoFlightCall[T]
}

func (g *repoFlightGroup[T]) do(key string, fn func() T) T {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*repoFlightCall[T])
	}
	if c, ok := g.calls[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val
	}
	c := &repoFlightCall[T]{}
	c.wg.Add(1)
	g.calls[key] = c
	g.mu.Unlock()

	c.val = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()
	return c.val
}

type fingerprintResult struct {
	fingerprint string
	headSHA     string
	porcelainZ  string
	err         error
}

var (
	fingerprintFlightGroup repoFlightGroup[fingerprintResult]
	diffFlightGroup        repoFlightGroup[[]worktreeFileDiff]
	statusFlightGroup      repoFlightGroup[repoStatusSummary]
)

// computeRepoStateFingerprintWithPorcelain builds a deterministic fingerprint from git status,
// HEAD commit, git index stat, and the stat stamps of modified/untracked files.
// Concurrent in-flight calls for the same (repoDir, diffTarget) are coalesced.
func computeRepoStateFingerprintWithPorcelain(repoDir, diffTarget string) (string, string, string, error) {
	cleanDir := filepath.Clean(repoDir)
	res := fingerprintFlightGroup.do(cleanDir+"\x00"+diffTarget, func() fingerprintResult {
		porcelain, err := runGitRaw(cleanDir, "status", "--porcelain", "-b", "-z")
		if err != nil {
			return fingerprintResult{err: err}
		}

		gitDir := resolveGitDirFast(cleanDir)
		if gitDir == "" {
			if gd, gdErr := runGit(cleanDir, "rev-parse", "--git-dir"); gdErr == nil && gd != "" {
				if !filepath.IsAbs(gd) {
					gd = filepath.Join(cleanDir, gd)
				}
				gitDir = gd
			}
		}
		head := resolveGitHeadFast(cleanDir, gitDir)

		var b strings.Builder
		b.WriteString(diffTarget)
		b.WriteByte('|')
		b.WriteString(head)
		b.WriteByte('|')

		if gitDir != "" {
			if idxInfo, statErr := os.Stat(filepath.Join(gitDir, "index")); statErr == nil {
				fmt.Fprintf(&b, "idx:%d:%d|", idxInfo.Size(), idxInfo.ModTime().UnixNano())
			}
		}

		entries := strings.Split(porcelain, "\x00")
		if len(entries) > maxFingerprintFiles {
			return fingerprintResult{headSHA: head, porcelainZ: porcelain}
		}
		for _, entry := range entries {
			if entry == "" {
				continue
			}
			b.WriteString(entry)
			b.WriteByte(';')
			if len(entry) > 3 && !strings.HasPrefix(entry, "## ") {
				rel := strings.TrimSpace(entry[3:])
				if rel != "" && !strings.HasPrefix(rel, ".hoxi/") {
					b.WriteString(fileStatStamp(cleanDir, rel))
					b.WriteByte(';')
				}
			}
		}
		return fingerprintResult{
			fingerprint: b.String(),
			headSHA:     head,
			porcelainZ:  porcelain,
		}
	})
	return res.fingerprint, res.headSHA, res.porcelainZ, res.err
}

func computeRepoStateFingerprint(repoDir, diffTarget string) string {
	fp, _, _, _ := computeRepoStateFingerprintWithPorcelain(repoDir, diffTarget)
	return fp
}

type cachedRepoDiffEntry struct {
	fingerprint string
	diffs       []worktreeFileDiff
}

type repoDiffCache struct {
	mu      sync.RWMutex
	entries map[string]cachedRepoDiffEntry
	order   []string
}

var globalRepoDiffCache = &repoDiffCache{
	entries: make(map[string]cachedRepoDiffEntry),
}

type cachedRepoStatusEntry struct {
	fingerprint string
	summary     repoStatusSummary
}

type repoStatusCache struct {
	mu      sync.RWMutex
	entries map[string]cachedRepoStatusEntry
	order   []string
}

var globalRepoStatusCache = &repoStatusCache{
	entries: make(map[string]cachedRepoStatusEntry),
}

func (c *repoStatusCache) get(repoDir, diffTarget, fingerprint string) (repoStatusSummary, bool) {
	if fingerprint == "" {
		return repoStatusSummary{}, false
	}
	key := repoDir + "\x00" + diffTarget
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || entry.fingerprint != fingerprint {
		return repoStatusSummary{}, false
	}
	return entry.summary, true
}

func (c *repoStatusCache) put(repoDir, diffTarget, fingerprint string, summary repoStatusSummary) {
	if fingerprint == "" {
		return
	}
	key := repoDir + "\x00" + diffTarget
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		if len(c.order) >= maxCachedRepoDiffs {
			evict := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, evict)
		}
		c.order = append(c.order, key)
	}
	c.entries[key] = cachedRepoStatusEntry{
		fingerprint: fingerprint,
		summary:     summary,
	}
}

func (c *repoStatusCache) invalidate(repoDir string) {
	if strings.TrimSpace(repoDir) == "" {
		return
	}
	prefix := filepath.Clean(repoDir) + "\x00"
	c.mu.Lock()
	defer c.mu.Unlock()
	nextOrder := c.order[:0]
	for _, k := range c.order {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		} else {
			nextOrder = append(nextOrder, k)
		}
	}
	c.order = nextOrder
}

func (c *repoDiffCache) get(repoDir, diffTarget, fingerprint string) ([]worktreeFileDiff, bool) {
	if fingerprint == "" {
		return nil, false
	}
	key := repoDir + "\x00" + diffTarget
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || entry.fingerprint != fingerprint {
		return nil, false
	}
	out := make([]worktreeFileDiff, len(entry.diffs))
	copy(out, entry.diffs)
	return out, true
}

func (c *repoDiffCache) put(repoDir, diffTarget, fingerprint string, diffs []worktreeFileDiff) {
	if fingerprint == "" {
		return
	}
	key := repoDir + "\x00" + diffTarget
	cloned := make([]worktreeFileDiff, len(diffs))
	copy(cloned, diffs)

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		if len(c.order) >= maxCachedRepoDiffs {
			evict := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, evict)
		}
		c.order = append(c.order, key)
	}
	c.entries[key] = cachedRepoDiffEntry{
		fingerprint: fingerprint,
		diffs:       cloned,
	}
}

func (c *repoDiffCache) invalidate(repoDir string) {
	globalRepoStatusCache.invalidate(repoDir)
	if strings.TrimSpace(repoDir) == "" {
		return
	}
	prefix := filepath.Clean(repoDir) + "\x00"
	c.mu.Lock()
	defer c.mu.Unlock()
	nextOrder := c.order[:0]
	for _, k := range c.order {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		} else {
			nextOrder = append(nextOrder, k)
		}
	}
	c.order = nextOrder
}

// DiffFile returns the authoritative full-content diff for a single relative file
// path in the session's worktree or repository, including large or generated files
// whose patches were summarized in the bulk diff response.
func (m *worktreeManager) DiffFile(sessionID, relPath string) (worktreeFileDiff, error) {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(strings.TrimSpace(relPath), "./")))
	if cleaned == "" || cleaned == "." || strings.HasPrefix(cleaned, "..") || strings.HasPrefix(cleaned, "/") {
		return worktreeFileDiff{}, fmt.Errorf("invalid relative file path")
	}

	meta := m.resolveSessionMeta(sessionID)
	if meta.Directory == "" {
		return worktreeFileDiff{}, fmt.Errorf("session directory not found")
	}

	repoDir, repoRelPath := resolveRepoForRelPath(meta.Directory, cleaned)
	repoRelPath = filepath.ToSlash(repoRelPath)
	diffTarget := "HEAD"
	var relPrefix string
	if filepath.Clean(repoDir) == filepath.Clean(meta.Directory) {
		if meta.Isolated && strings.TrimSpace(meta.BaseCommit) != "" {
			diffTarget = strings.TrimSpace(meta.BaseCommit)
		}
	} else if rel, err := filepath.Rel(meta.Directory, repoDir); err == nil && rel != "." {
		relPrefix = filepath.ToSlash(rel)
	}

	var repoBranch string
	if relPrefix != "" {
		repoBranch = resolveGitBranchFast(repoDir)
	}

	numstat, err := runGitRaw(repoDir, "diff", "--no-renames", "--numstat", "-z", diffTarget, "--", repoRelPath)
	if err != nil && diffTarget != "HEAD" {
		diffTarget = "HEAD"
		numstat, err = runGitRaw(repoDir, "diff", "--no-renames", "--numstat", "-z", "HEAD", "--", repoRelPath)
	}

	generated := classifyGeneratedFile(cleaned)
	stamp := fileStatStamp(repoDir, repoRelPath)

	if err == nil && strings.TrimSpace(numstat) != "" {
		line := strings.Split(strings.TrimSuffix(numstat, "\x00"), "\x00")[0]
		fields := strings.SplitN(line, "\t", 3)
		add, del := 0, 0
		binary := false
		if len(fields) >= 2 {
			if fields[0] == "-" || fields[1] == "-" {
				binary = true
			} else {
				add, _ = strconv.Atoi(fields[0])
				del, _ = strconv.Atoi(fields[1])
			}
		}
		patchRaw, _ := runGitRaw(repoDir, "diff", "--no-renames", "--no-ext-diff", "--no-textconv", "--no-color", "-U3", diffTarget, "--", repoRelPath)
		if relPrefix != "" {
			patchRaw = prefixPatchFilePaths(patchRaw, repoRelPath, cleaned)
		}
		st := "modified"
		if strings.Contains(patchRaw, "\nnew file mode ") {
			st = "added"
		} else if strings.Contains(patchRaw, "\ndeleted file mode ") {
			st = "deleted"
		}
		large := len(patchRaw) > maxSingleFilePatchBytes
		if large {
			patchRaw = patchRaw[:maxSingleFilePatchBytes]
		}
		return worktreeFileDiff{
			File:          cleaned,
			Patch:         strings.TrimSpace(patchRaw),
			Additions:     add,
			Deletions:     del,
			Status:        st,
			Repo:          relPrefix,
			Branch:        repoBranch,
			GeneratedLike: generated,
			Binary:        binary,
			Large:         large,
			Stamp:         stamp,
		}, nil
	}

	// Fallback: check if it is an untracked file
	full := filepath.Join(repoDir, filepath.FromSlash(repoRelPath))
	info, statErr := os.Stat(full)
	if statErr != nil || info.IsDir() {
		return worktreeFileDiff{}, fmt.Errorf("file diff not found: %s", cleaned)
	}
	if info.Size() > maxSingleFilePatchBytes {
		return worktreeFileDiff{
			File:          cleaned,
			Status:        "added",
			Repo:          relPrefix,
			Branch:        repoBranch,
			GeneratedLike: generated,
			Large:         true,
			Stamp:         stamp,
		}, nil
	}
	data, readErr := os.ReadFile(full)
	if readErr != nil || bytes.IndexByte(data, 0) != -1 {
		return worktreeFileDiff{
			File:          cleaned,
			Status:        "added",
			Repo:          relPrefix,
			Branch:        repoBranch,
			GeneratedLike: generated,
			Binary:        readErr == nil,
			Stamp:         stamp,
		}, nil
	}
	text := strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(text, "\n")
	}
	var patchBuf strings.Builder
	patchBuf.WriteString(fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n", cleaned, cleaned, cleaned))
	if len(lines) > 0 {
		patchBuf.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(lines)))
		for _, l := range lines {
			patchBuf.WriteString("+" + l + "\n")
		}
	}
	return worktreeFileDiff{
		File:          cleaned,
		Patch:         strings.TrimSpace(patchBuf.String()),
		Additions:     len(lines),
		Deletions:     0,
		Status:        "added",
		Repo:          relPrefix,
		Branch:        repoBranch,
		GeneratedLike: generated,
		Stamp:         stamp,
	}, nil
}
