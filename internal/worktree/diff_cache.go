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
	maxCachedRepoDiffs         = 32
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

// computeRepoStateFingerprint builds a deterministic fingerprint from git status,
// HEAD commit, git index stat, and the stat stamps of modified/untracked files.
func computeRepoStateFingerprint(repoDir, diffTarget string) string {
	porcelain, err := runGitRaw(repoDir, "status", "--porcelain", "-b", "-z")
	if err != nil {
		return ""
	}
	head, _ := runGit(repoDir, "rev-parse", "HEAD")

	var b strings.Builder
	b.WriteString(diffTarget)
	b.WriteByte('|')
	b.WriteString(head)
	b.WriteByte('|')

	if gitDir, gdErr := runGit(repoDir, "rev-parse", "--git-dir"); gdErr == nil && gitDir != "" {
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(repoDir, gitDir)
		}
		if idxInfo, statErr := os.Stat(filepath.Join(gitDir, "index")); statErr == nil {
			fmt.Fprintf(&b, "idx:%d:%d|", idxInfo.Size(), idxInfo.ModTime().UnixNano())
		}
	}

	entries := strings.Split(porcelain, "\x00")
	if len(entries) > maxFingerprintFiles {
		return ""
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
				b.WriteString(fileStatStamp(repoDir, rel))
				b.WriteByte(';')
			}
		}
	}
	return b.String()
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
		repoBranch, _ = runGit(repoDir, "rev-parse", "--abbrev-ref", "HEAD")
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
