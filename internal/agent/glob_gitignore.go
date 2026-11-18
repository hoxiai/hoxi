package agent

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxBraceExpansionDepth    = 3
	maxExpandedGlobCandidates = 64
	maxIgnoreFileSizeBytes    = 64 * 1024
)

// matchGlob matches a workspace-relative slash path against a glob pattern,
// supporting recursive `**` directory wildcards, standard `*`/`?`/`[...]`
// segment wildcards, and `{a,b,c}` brace expansion.
func matchGlob(pattern, relSlash string) bool {
	rawPat := normalizeGlobSlashes(strings.TrimSpace(pattern))
	cleanRel := strings.Trim(filepath.ToSlash(relSlash), "/")
	if rawPat == "" || cleanRel == "" || cleanRel == "." {
		return false
	}

	candidates := expandGlobBraces(rawPat, 0)
	baseName := path.Base(cleanRel)
	relSegs := strings.Split(cleanRel, "/")

	for _, cand := range candidates {
		p := normalizeGlobCandidate(cand)
		if p == "" {
			continue
		}
		if p == "**" || p == "**/*" {
			return true
		}
		// Basename shorthand (e.g. "*.go" or "*.{ts,tsx}") matches at any depth.
		if !strings.Contains(p, "/") && !strings.Contains(p, "**") {
			if matchSingleGlobSegment(p, baseName) {
				return true
			}
			continue
		}
		patSegs := strings.Split(p, "/")
		if matchGlobSegments(patSegs, relSegs) {
			return true
		}
	}
	return false
}

func normalizeGlobCandidate(p string) string {
	p = normalizeGlobSlashes(strings.TrimSpace(p))
	for strings.HasPrefix(p, "./") {
		p = strings.TrimPrefix(p, "./")
	}
	p = strings.TrimPrefix(p, "/")
	if strings.HasSuffix(p, "/") {
		p += "**"
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return p
}

// normalizeGlobSlashes converts Windows path backslashes into forward slashes
// without clobbering glob character escapes (`\{`, `\}`, `\[`, `\]`, `\*`, `\?`, `\\`).
func normalizeGlobSlashes(p string) string {
	if filepath.Separator != '\\' || !strings.ContainsRune(p, '\\') {
		return p
	}
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' && i+1 < len(p) {
			switch p[i+1] {
			case '{', '}', ',', '[', ']', '*', '?', '\\':
				b.WriteByte('\\')
				b.WriteByte(p[i+1])
				i++
				continue
			}
			b.WriteByte('/')
			continue
		}
		b.WriteByte(p[i])
	}
	return b.String()
}

// expandGlobBraces expands top-level `{a,b}` alternatives with bounded depth
// and candidate count so malformed or nested patterns remain linear-time safe.
func expandGlobBraces(pattern string, depth int) []string {
	if depth >= maxBraceExpansionDepth || !strings.Contains(pattern, "{") {
		return []string{pattern}
	}
	openIdx, closeIdx, parts, ok := findFirstBraceGroup(pattern)
	if !ok {
		return []string{pattern}
	}
	prefix := pattern[:openIdx]
	suffix := pattern[closeIdx+1:]
	var out []string
	for _, alt := range parts {
		sub := expandGlobBraces(prefix+alt+suffix, depth+1)
		for _, item := range sub {
			out = append(out, item)
			if len(out) >= maxExpandedGlobCandidates {
				return out
			}
		}
	}
	return out
}

func findFirstBraceGroup(s string) (openIdx, closeIdx int, parts []string, ok bool) {
	inClass := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '{':
			if inClass {
				continue
			}
			depth := 1
			curStart := i + 1
			var elems []string
			for j := i + 1; j < len(s); j++ {
				if s[j] == '\\' {
					j++
					continue
				}
				switch s[j] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						elems = append(elems, s[curStart:j])
						if len(elems) >= 2 {
							return i, j, elems, true
						}
						break
					}
				case ',':
					if depth == 1 {
						elems = append(elems, s[curStart:j])
						curStart = j + 1
					}
				}
				if depth == 0 {
					break
				}
			}
		}
	}
	return 0, 0, nil, false
}

func matchGlobSegments(patSegs, pathSegs []string) bool {
	nPat := len(patSegs)
	nPath := len(pathSegs)
	// dp[j] reports whether patSegs[:i] matches pathSegs[:j].
	dp := make([]bool, nPath+1)
	dp[0] = true

	for i := 0; i < nPat; i++ {
		seg := patSegs[i]
		if seg == "**" {
			for j := 1; j <= nPath; j++ {
				if dp[j-1] {
					dp[j] = true
				}
			}
			continue
		}
		next := make([]bool, nPath+1)
		for j := 1; j <= nPath; j++ {
			if dp[j-1] && matchSingleGlobSegment(seg, pathSegs[j-1]) {
				next[j] = true
			}
		}
		dp = next
	}
	return dp[nPath]
}

func matchSingleGlobSegment(segPat, name string) bool {
	for strings.Contains(segPat, "**") {
		segPat = strings.ReplaceAll(segPat, "**", "*")
	}
	ok, err := path.Match(segPat, name)
	if err != nil {
		return segPat == name
	}
	return ok
}

type ignoreRule struct {
	baseRel  string // slash-separated directory relative to walkBase ("" for root)
	pattern  string
	negate   bool
	dirOnly  bool
	anchored bool
}

type workspaceIgnorer struct {
	walkBase    string
	realBase    string
	loaded      map[string]bool
	rules       []ignoreRule
	hasNegation bool
}

func newWorkspaceIgnorer(worktreeDir, searchRoot string) *workspaceIgnorer {
	base := strings.TrimSpace(worktreeDir)
	if base == "" {
		base = strings.TrimSpace(searchRoot)
	}
	if base == "" {
		return nil
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return nil
	}
	realBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		realBase = absBase
	}
	ig := &workspaceIgnorer{
		walkBase: filepath.Clean(absBase),
		realBase: filepath.Clean(realBase),
		loaded:   make(map[string]bool),
	}
	ig.loadDirIgnoreFiles(ig.walkBase)

	if strings.TrimSpace(searchRoot) != "" {
		if absRoot, rErr := filepath.Abs(searchRoot); rErr == nil {
			cleanRoot := filepath.Clean(absRoot)
			if cleanRoot != ig.walkBase && isLexicalPathWithin(ig.walkBase, cleanRoot) {
				rel, relErr := filepath.Rel(ig.walkBase, cleanRoot)
				if relErr == nil && rel != "." {
					cur := ig.walkBase
					for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
						cur = filepath.Join(cur, part)
						ig.loadDirIgnoreFiles(cur)
					}
					if ig.IsIgnored(cleanRoot, true) {
						// Caller explicitly targeted an ignored subdirectory (like `rg pat ignored_dir`):
						// scope ignore rules strictly to files inside cleanRoot rather than parent rules.
						scoped := &workspaceIgnorer{
							walkBase: cleanRoot,
							realBase: ig.realBase,
							loaded:   make(map[string]bool),
						}
						scoped.loadDirIgnoreFiles(cleanRoot)
						return scoped
					}
				}
			}
		}
	}
	return ig
}

func (ig *workspaceIgnorer) EnterDir(dirPath string) {
	if ig == nil {
		return
	}
	ig.loadDirIgnoreFiles(dirPath)
}

func (ig *workspaceIgnorer) loadDirIgnoreFiles(dirPath string) {
	cleanDir := filepath.Clean(dirPath)
	if ig.loaded[cleanDir] || !isLexicalPathWithin(ig.walkBase, cleanDir) {
		return
	}
	ig.loaded[cleanDir] = true

	relDir, err := filepath.Rel(ig.walkBase, cleanDir)
	if err != nil || strings.HasPrefix(relDir, "..") {
		return
	}
	baseRel := ""
	if relDir != "." {
		baseRel = filepath.ToSlash(relDir)
	}

	for _, fname := range []string{".gitignore", ".ignore"} {
		cand := filepath.Join(cleanDir, fname)
		st, lErr := os.Lstat(cand)
		if lErr != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > maxIgnoreFileSizeBytes {
			continue
		}
		if realFile, rErr := filepath.EvalSymlinks(cand); rErr != nil || !isLexicalPathWithin(ig.realBase, realFile) {
			continue
		}
		raw, rErr := os.ReadFile(cand)
		if rErr != nil {
			continue
		}
		ig.appendRulesFromText(baseRel, string(raw))
	}
}

func (ig *workspaceIgnorer) appendRulesFromText(baseRel, content string) {
	for _, rawLine := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(rawLine, " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
			line = line[1:]
		}
		negate := false
		if strings.HasPrefix(line, "!") {
			negate = true
			line = strings.TrimSpace(line[1:])
		}
		if line == "" {
			continue
		}
		dirOnly := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		if line == "" {
			continue
		}
		anchored := strings.HasPrefix(line, "/")
		line = strings.TrimPrefix(line, "/")
		if !anchored && strings.Contains(line, "/") && !strings.HasPrefix(line, "**/") {
			anchored = true
		}
		if negate {
			ig.hasNegation = true
		}
		ig.rules = append(ig.rules, ignoreRule{
			baseRel:  baseRel,
			pattern:  line,
			negate:   negate,
			dirOnly:  dirOnly,
			anchored: anchored,
		})
	}
}

// CanReincludeUnderDir reports whether any anchored negation rule (`!dir/...`)
// explicitly targets a descendant inside absOrWalkPath. Unanchored file
// negations (like `!keep.log` or `!.env.example`) do not re-include files
// inside an excluded parent directory, allowing WalkDir to SkipDir in O(1).
func (ig *workspaceIgnorer) CanReincludeUnderDir(absOrWalkPath string) bool {
	if ig == nil || !ig.hasNegation {
		return false
	}
	cleanTarget := filepath.Clean(absOrWalkPath)
	if abs, err := filepath.Abs(cleanTarget); err == nil {
		cleanTarget = filepath.Clean(abs)
	}
	rel, err := filepath.Rel(ig.walkBase, cleanTarget)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	dirRel := filepath.ToSlash(rel)
	dirPrefix := dirRel + "/"
	for _, r := range ig.rules {
		if !r.negate || !r.anchored {
			continue
		}
		fullPat := r.pattern
		if r.baseRel != "" {
			fullPat = r.baseRel + "/" + fullPat
		}
		if strings.HasPrefix(fullPat, dirPrefix) || strings.HasPrefix(dirPrefix, fullPat+"/") {
			return true
		}
	}
	return false
}

func (ig *workspaceIgnorer) IsIgnored(absOrWalkPath string, isDir bool) bool {
	if ig == nil || len(ig.rules) == 0 {
		return false
	}
	cleanTarget := filepath.Clean(absOrWalkPath)
	if abs, err := filepath.Abs(cleanTarget); err == nil {
		cleanTarget = filepath.Clean(abs)
	}
	rel, err := filepath.Rel(ig.walkBase, cleanTarget)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	relSlash := filepath.ToSlash(rel)

	ignored := false
	for _, r := range ig.rules {
		subRel := relSlash
		if r.baseRel != "" {
			prefix := r.baseRel + "/"
			if !strings.HasPrefix(relSlash, prefix) {
				continue
			}
			subRel = strings.TrimPrefix(relSlash, prefix)
		}
		if subRel == "" {
			continue
		}
		if r.matches(subRel, isDir) {
			ignored = !r.negate
		}
	}
	return ignored
}

func (r ignoreRule) matches(subRel string, isDir bool) bool {
	if r.dirOnly {
		if isDir && r.matchPath(subRel) {
			return true
		}
		// Match files inside a directory matched by a dirOnly rule when checking a file path directly.
		parts := strings.Split(subRel, "/")
		for i := 1; i < len(parts); i++ {
			if r.matchPath(strings.Join(parts[:i], "/")) {
				return true
			}
		}
		return false
	}
	return r.matchPath(subRel)
}

func (r ignoreRule) matchPath(subRel string) bool {
	if r.anchored {
		if matchGlob(r.pattern, subRel) {
			return true
		}
		return matchGlob(r.pattern+"/**", subRel)
	}
	if !strings.Contains(r.pattern, "/") {
		for _, seg := range strings.Split(subRel, "/") {
			if matchSingleGlobSegment(r.pattern, seg) {
				return true
			}
		}
		return false
	}
	return matchGlob(r.pattern, subRel) || matchGlob("**/"+r.pattern, subRel)
}
