package worktree

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxNestedGitRepoDepth = 3
	maxNestedScanDirs     = 200
	maxConcurrentGitRepos = 6
	discoveredReposTTL    = 15 * time.Second
)

type cachedDiscoveredRepos struct {
	modTime   int64
	expiresAt time.Time
	repos     []gitRepoTarget
}

var (
	discoveredReposMu    sync.RWMutex
	discoveredReposCache = make(map[string]cachedDiscoveredRepos)
)

type gitRepoTarget struct {
	Dir       string
	RelPrefix string
}

type worktreeFileDiff struct {
	File          string `json:"file"`
	Patch         string `json:"patch,omitempty"`
	Additions     int    `json:"additions"`
	Deletions     int    `json:"deletions"`
	Status        string `json:"status,omitempty"`
	Repo          string `json:"repo,omitempty"`
	Branch        string `json:"branch,omitempty"`
	GeneratedLike bool   `json:"generatedLike,omitempty"`
	Binary        bool   `json:"binary,omitempty"`
	Large         bool   `json:"large,omitempty"`
	Stamp         string `json:"stamp,omitempty"`
}

func hasGitMarker(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir() || info.Mode().IsRegular()
}

func shouldSkipNestedScanDir(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "dist", "build", "target", "out", "__pycache__", "Pods", "DerivedData", "venv", "env":
		return true
	default:
		return false
	}
}

func scanNestedGitRepos(rootDir, currentDir string, depth int, visited *int, out *[]gitRepoTarget) {
	if *visited >= maxNestedScanDirs {
		return
	}
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if shouldSkipNestedScanDir(name) {
			continue
		}
		*visited++
		if *visited > maxNestedScanDirs {
			return
		}
		childDir := filepath.Join(currentDir, name)
		if hasGitMarker(childDir) {
			if rel, relErr := filepath.Rel(rootDir, childDir); relErr == nil && rel != "." {
				*out = append(*out, gitRepoTarget{
					Dir:       childDir,
					RelPrefix: filepath.ToSlash(rel),
				})
			}
			continue
		}
		if depth < maxNestedGitRepoDepth {
			scanNestedGitRepos(rootDir, childDir, depth+1, visited, out)
		}
	}
}

func discoverGitRepos(rawDir string) []gitRepoTarget {
	trimmed := strings.TrimSpace(rawDir)
	if trimmed == "" {
		return nil
	}
	baseDir := filepath.Clean(trimmed)
	info, err := os.Stat(baseDir)
	if err != nil || !info.IsDir() {
		return nil
	}
	modNano := info.ModTime().UnixNano()
	now := time.Now()

	discoveredReposMu.RLock()
	cached, ok := discoveredReposCache[baseDir]
	discoveredReposMu.RUnlock()
	if ok && cached.modTime == modNano && now.Before(cached.expiresAt) {
		out := make([]gitRepoTarget, len(cached.repos))
		copy(out, cached.repos)
		return out
	}

	var nested []gitRepoTarget
	visited := 0
	scanNestedGitRepos(baseDir, baseDir, 1, &visited, &nested)

	var result []gitRepoTarget
	if hasGitMarker(baseDir) {
		result = append([]gitRepoTarget{{Dir: baseDir, RelPrefix: ""}}, nested...)
	} else if len(nested) > 0 {
		result = nested
	} else if top, topErr := runGit(baseDir, "rev-parse", "--show-toplevel"); topErr == nil && strings.TrimSpace(top) != "" {
		result = []gitRepoTarget{{Dir: baseDir, RelPrefix: ""}}
	}

	if len(result) > 0 {
		cloned := make([]gitRepoTarget, len(result))
		copy(cloned, result)
		discoveredReposMu.Lock()
		discoveredReposCache[baseDir] = cachedDiscoveredRepos{
			modTime:   modNano,
			expiresAt: now.Add(discoveredReposTTL),
			repos:     cloned,
		}
		discoveredReposMu.Unlock()
	}
	return result
}

func nestedRepoPrefixSet(repos []gitRepoTarget) map[string]struct{} {
	set := make(map[string]struct{})
	for _, r := range repos {
		if r.RelPrefix != "" {
			set[r.RelPrefix] = struct{}{}
		}
	}
	return set
}

func isWithinNestedRepo(relPath string, nestedPrefixes map[string]struct{}) bool {
	if len(nestedPrefixes) == 0 {
		return false
	}
	norm := strings.Trim(filepath.ToSlash(strings.TrimSpace(relPath)), "\"/")
	if norm == "" {
		return false
	}
	if _, ok := nestedPrefixes[norm]; ok {
		return true
	}
	for prefix := range nestedPrefixes {
		if strings.HasPrefix(norm, prefix+"/") {
			return true
		}
	}
	return false
}

func resolveRepoForRelPath(workDir, cleanedRelPath string) (string, string) {
	baseDir := filepath.Clean(workDir)
	absTarget := filepath.Join(baseDir, cleanedRelPath)
	curr := filepath.Dir(absTarget)
	for {
		if hasGitMarker(curr) {
			if subRel, err := filepath.Rel(curr, absTarget); err == nil && subRel != "." && !strings.HasPrefix(subRel, "..") {
				return curr, subRel
			}
		}
		if curr == baseDir || filepath.Dir(curr) == curr {
			break
		}
		curr = filepath.Dir(curr)
	}
	return baseDir, cleanedRelPath
}

func prefixPatchFilePaths(patch, file, relFile string) string {
	if patch == "" || file == "" || file == relFile {
		return patch
	}
	if nl := strings.IndexByte(patch, '\n'); nl > 0 && strings.HasPrefix(patch, "diff --git ") {
		patch = fmt.Sprintf("diff --git a/%s b/%s", relFile, relFile) + patch[nl:]
	}
	patch = strings.ReplaceAll(patch, "\n--- a/"+file+"\n", "\n--- a/"+relFile+"\n")
	patch = strings.ReplaceAll(patch, "\n+++ b/"+file+"\n", "\n+++ b/"+relFile+"\n")
	patch = strings.ReplaceAll(patch, "\nBinary files a/"+file+" and b/"+file, "\nBinary files a/"+relFile+" and b/"+relFile)
	return patch
}

func (m *worktreeManager) resolveSessionMeta(sessionID string) worktreeMeta {
	sessionID = sanitizeSessionID(sessionID)
	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if sessionID != "" && sessionID != "__root__" {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return worktreeMeta{
					SessionID: sessionID,
					Isolated:  true,
					Directory: candidate,
					Branch:    "hoxi/" + sessionID,
				}
			}
		}
		return worktreeMeta{
			SessionID: sessionID,
			Isolated:  false,
			Directory: m.rootDir,
		}
	}
	return meta
}

func parsePatchHeaderFile(chunk string) string {
	firstLine := chunk
	if nl := strings.IndexByte(chunk, '\n'); nl >= 0 {
		firstLine = chunk[:nl]
	}
	if idx := strings.Index(firstLine, " b/"); idx >= 0 {
		return filepath.ToSlash(strings.TrimSpace(firstLine[idx+3:]))
	}
	return ""
}

func diffSingleRepo(repo gitRepoTarget, diffTarget string, nestedPrefixes map[string]struct{}) []worktreeFileDiff {
	fingerprint := computeRepoStateFingerprint(repo.Dir, diffTarget)
	if cached, hit := globalRepoDiffCache.get(repo.Dir, diffTarget, fingerprint); hit {
		return cached
	}

	var repoBranch string
	if repo.RelPrefix != "" {
		repoBranch, _ = runGit(repo.Dir, "rev-parse", "--abbrev-ref", "HEAD")
	}

	useCached := false
	numstat, err := runGitRaw(repo.Dir, "diff", "--no-renames", "--numstat", "-z", diffTarget)
	if err != nil && diffTarget != "HEAD" {
		diffTarget = "HEAD"
		numstat, err = runGitRaw(repo.Dir, "diff", "--no-renames", "--numstat", "-z", "HEAD")
	}
	if err != nil {
		// Unborn branch fallback (newly git init'd repo without HEAD commit yet)
		if cached, cachedErr := runGitRaw(repo.Dir, "diff", "--cached", "--no-renames", "--numstat", "-z"); cachedErr == nil {
			numstat = cached
			useCached = true
		}
	}

	var patches []string
	patchByFile := make(map[string]string)
	if strings.TrimSpace(numstat) != "" {
		var patchesRaw string
		if useCached {
			patchesRaw, _ = runGitRaw(repo.Dir, "diff", "--cached", "--no-renames", "--no-ext-diff", "--no-textconv", "--no-color", "-U3")
		} else {
			patchesRaw, _ = runGitRaw(repo.Dir, "diff", "--no-renames", "--no-ext-diff", "--no-textconv", "--no-color", "-U3", diffTarget)
		}
		patches = strings.Split("\n"+patchesRaw, "\ndiff --git ")
		if len(patches) > 0 {
			patches = patches[1:]
		}
		for _, chunk := range patches {
			if f := parsePatchHeaderFile(chunk); f != "" {
				patchByFile[f] = "diff --git " + chunk
			}
		}
	}

	results := make([]worktreeFileDiff, 0)
	seen := make(map[string]struct{})

	for i, line := range strings.Split(strings.TrimSuffix(numstat, "\x00"), "\x00") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		file := filepath.ToSlash(fields[2])
		if file == "" || strings.HasPrefix(file, ".hoxi/") {
			continue
		}
		if repo.RelPrefix == "" && isWithinNestedRepo(file, nestedPrefixes) {
			continue
		}
		binary := fields[0] == "-" || fields[1] == "-"
		add, _ := strconv.Atoi(fields[0])
		del, _ := strconv.Atoi(fields[1])
		patch := patchByFile[file]
		if patch == "" && i < len(patches) {
			patch = "diff --git " + patches[i]
		}
		if strings.Contains(patch, "\nSubproject commit ") && isWithinNestedRepo(file, nestedPrefixes) {
			continue
		}
		relFile := file
		if repo.RelPrefix != "" {
			relFile = repo.RelPrefix + "/" + strings.TrimPrefix(file, "/")
			patch = prefixPatchFilePaths(patch, file, relFile)
		}
		st := "modified"
		if strings.Contains(patch, "\nnew file mode ") {
			st = "added"
		} else if strings.Contains(patch, "\ndeleted file mode ") {
			st = "deleted"
		}
		generated := classifyGeneratedFile(relFile)
		large := len(patch) > maxSingleFilePatchBytes || (generated && (len(patch) > maxGeneratedInlinePatchLen || add+del > 300))
		if large {
			patch = ""
		}
		seen[relFile] = struct{}{}
		results = append(results, worktreeFileDiff{
			File:          relFile,
			Patch:         strings.TrimSpace(patch),
			Additions:     add,
			Deletions:     del,
			Status:        st,
			Repo:          repo.RelPrefix,
			Branch:        repoBranch,
			GeneratedLike: generated,
			Binary:        binary,
			Large:         large,
			Stamp:         fileStatStamp(repo.Dir, file),
		})
	}

	untracked, _ := runGitRaw(repo.Dir, "ls-files", "--others", "--exclude-standard", "-z")
	for _, rel := range strings.Split(untracked, "\x00") {
		if rel == "" || strings.HasPrefix(rel, ".hoxi/") {
			continue
		}
		slashRel := filepath.ToSlash(rel)
		if repo.RelPrefix == "" && isWithinNestedRepo(slashRel, nestedPrefixes) {
			continue
		}
		relFile := slashRel
		if repo.RelPrefix != "" {
			relFile = repo.RelPrefix + "/" + strings.TrimPrefix(slashRel, "/")
		}
		if _, exists := seen[relFile]; exists {
			continue
		}
		full := filepath.Join(repo.Dir, rel)
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			continue
		}
		generated := classifyGeneratedFile(relFile)
		stamp := fileStatStamp(repo.Dir, slashRel)
		if info.Size() > maxSingleFilePatchBytes {
			seen[relFile] = struct{}{}
			results = append(results, worktreeFileDiff{
				File:          relFile,
				Status:        "added",
				Repo:          repo.RelPrefix,
				Branch:        repoBranch,
				GeneratedLike: generated,
				Large:         true,
				Stamp:         stamp,
			})
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil || bytes.IndexByte(data, 0) != -1 {
			continue
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		text = strings.TrimSuffix(text, "\n")
		lines := []string{}
		if len(data) > 0 {
			lines = strings.Split(text, "\n")
		}
		large := generated && (len(data) > maxGeneratedInlinePatchLen || len(lines) > 300)
		var patch string
		if !large {
			var patchBuf strings.Builder
			patchBuf.WriteString(fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n", relFile, relFile, relFile))
			if len(lines) > 0 {
				patchBuf.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(lines)))
				for _, l := range lines {
					patchBuf.WriteString("+" + l + "\n")
				}
			}
			patch = strings.TrimSpace(patchBuf.String())
		}
		seen[relFile] = struct{}{}
		results = append(results, worktreeFileDiff{
			File:          relFile,
			Patch:         patch,
			Additions:     len(lines),
			Deletions:     0,
			Status:        "added",
			Repo:          repo.RelPrefix,
			Branch:        repoBranch,
			GeneratedLike: generated,
			Large:         large,
			Stamp:         stamp,
		})
	}

	globalRepoDiffCache.put(repo.Dir, diffTarget, fingerprint, results)
	return results
}

func (m *worktreeManager) Diff(sessionID string) ([]worktreeFileDiff, error) {
	meta := m.resolveSessionMeta(sessionID)
	if meta.Directory == "" {
		return []worktreeFileDiff{}, nil
	}

	repos := discoverGitRepos(meta.Directory)
	if len(repos) == 0 {
		return []worktreeFileDiff{}, nil
	}

	nestedPrefixes := nestedRepoPrefixSet(repos)
	rootDiffTarget := "HEAD"
	if meta.Isolated && strings.TrimSpace(meta.BaseCommit) != "" {
		rootDiffTarget = strings.TrimSpace(meta.BaseCommit)
	}

	if len(repos) == 1 {
		target := "HEAD"
		if repos[0].RelPrefix == "" {
			target = rootDiffTarget
		}
		return diffSingleRepo(repos[0], target, nestedPrefixes), nil
	}

	perRepo := make([][]worktreeFileDiff, len(repos))
	sem := make(chan struct{}, maxConcurrentGitRepos)
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		go func(idx int, r gitRepoTarget) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			target := "HEAD"
			if r.RelPrefix == "" {
				target = rootDiffTarget
			}
			perRepo[idx] = diffSingleRepo(r, target, nestedPrefixes)
		}(i, repo)
	}
	wg.Wait()

	results := make([]worktreeFileDiff, 0)
	seen := make(map[string]struct{})
	for _, list := range perRepo {
		for _, item := range list {
			if _, exists := seen[item.File]; exists {
				continue
			}
			seen[item.File] = struct{}{}
			results = append(results, item)
		}
	}
	return results, nil
}

