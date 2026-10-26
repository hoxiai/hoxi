package codeindex

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

type Config struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
	ChunkLines    int
	ChunkOverlap  int
	MaxFileChunks int
}

func DefaultConfig() Config {
	return Config{
		MaxFiles:      20000,
		MaxFileBytes:  512 * 1024,
		MaxTotalBytes: 48 * 1024 * 1024,
		ChunkLines:    80,
		ChunkOverlap:  12,
		MaxFileChunks: 200,
	}
}

type Chunk struct {
	Path      string `json:"path"`
	Symbol    string `json:"symbol,omitempty"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Text      string `json:"text"`
}

type FileRecord struct {
	Path    string   `json:"path"`
	ModTime int64    `json:"modTime"`
	Size    int64    `json:"size"`
	Hash    string   `json:"hash"`
	Imports []string `json:"imports,omitempty"`
	Chunks  []Chunk  `json:"chunks"`
}

type Index struct {
	Version   int                    `json:"version"`
	Directory string                 `json:"directory"`
	BuiltAt   int64                  `json:"builtAt"`
	Files     map[string]*FileRecord `json:"files"`
}

// Ported from Kilo Code packages/kilo-indexing/src/file/ignore.ts.
var ignoredFolders = map[string]bool{
	"node_modules": true, "bower_components": true, ".pnpm-store": true,
	"vendor": true, ".npm": true, "dist": true, "build": true, "out": true,
	".next": true, "target": true, "bin": true, "obj": true, ".git": true,
	".svn": true, ".hg": true, ".vscode": true, ".idea": true, ".turbo": true,
	".output": true, "desktop": true, ".sst": true, ".cache": true,
	".webkit-cache": true, "__pycache__": true, ".pytest_cache": true,
	"mypy_cache": true, ".history": true, ".gradle": true,
	// Hoxi-specific
	".hoxi": true, ".claude": true, "worktrees": true,
	"Pods": true, "DerivedData": true, ".dart_tool": true, ".pub-cache": true,
	"go-build": true, "go-build-cache": true, ".gocache": true, ".gomodcache": true,
	".venv": true, "venv": true, "coverage": true, ".bundle": true,
	"cache": true, "caches": true, "tmp": true, "temp": true, "logs": true,
}

var indexableExtensions = map[string]bool{}

func init() {
	for _, ext := range strings.Split(
		"go,ts,tsx,js,jsx,mjs,cjs,py,pyi,rb,rs,java,kt,kts,swift,c,h,cc,cpp,cxx,hpp,cs,php,scala,sh,bash,zsh,fish,ps1,"+
			"sql,md,mdx,json,jsonc,yaml,yml,toml,xml,html,htm,css,scss,sass,less,vue,svelte,dart,lua,r,ex,exs,erl,hs,"+
			"clj,cljs,tf,hcl,proto,graphql,gql,ini,cfg,conf,lock,gradle,mk,cmake", ",") {
		if ext != "" {
			indexableExtensions["."+ext] = true
		}
	}
}

var (
	declarationRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:default\s+)?(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:static\s+)?(?:abstract\s+)?(?:readonly\s+)?(?:function|class|interface|type|struct|enum|trait|impl|def|func|fn|const|let|var|module|namespace|object|record)\s+([A-Za-z_$][\w$]*)`)
	goMethodRe    = regexp.MustCompile(`(?m)^\s*func\s*\([^)]+\)\s*([A-Za-z_]\w*)`)
	assignRe      = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:(?:const|let|var)\s+)?([A-Za-z_$][\w$]*)\s*(?::[^=\n]+)?[:=]\s*(?:async\s*)?(?:function|\(|=>)`)
	quotedImportRe = regexp.MustCompile(`["']([^"'\s]+)["']`)
	pyFromImportRe = regexp.MustCompile(`^\s*from\s+([A-Za-z0-9_.]+)\s+import\b`)
	pyImportRe     = regexp.MustCompile(`^\s*import\s+([A-Za-z0-9_.]+)`)
	rustUseRe      = regexp.MustCompile(`^\s*(?:pub\s+)?use\s+([A-Za-z0-9_:]+)`)
)

func extractFileImports(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	maxLines := len(lines)
	if maxLines > 160 {
		maxLines = 160
	}
	var imports []string
	seen := make(map[string]struct{})
	addImport := func(imp string) {
		imp = strings.TrimSpace(imp)
		if imp == "" || len(imports) >= 32 {
			return
		}
		if _, ok := seen[imp]; ok {
			return
		}
		seen[imp] = struct{}{}
		imports = append(imports, imp)
	}

	inGoImportBlock := false
	for i := 0; i < maxLines; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if inGoImportBlock {
			if strings.HasPrefix(trimmed, ")") {
				inGoImportBlock = false
				continue
			}
			if m := quotedImportRe.FindStringSubmatch(trimmed); len(m) == 2 {
				addImport(m[1])
			}
			continue
		}
		if strings.HasPrefix(trimmed, "import (") || trimmed == "import(" {
			inGoImportBlock = true
			continue
		}
		if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "export ") || strings.Contains(trimmed, "require(") {
			if matches := quotedImportRe.FindAllStringSubmatch(trimmed, -1); len(matches) > 0 {
				for _, m := range matches {
					if len(m) == 2 {
						addImport(m[1])
					}
				}
				continue
			}
		}
		if m := pyFromImportRe.FindStringSubmatch(trimmed); len(m) == 2 {
			addImport(m[1])
			continue
		}
		if m := pyImportRe.FindStringSubmatch(trimmed); len(m) == 2 {
			addImport(m[1])
			continue
		}
		if m := rustUseRe.FindStringSubmatch(trimmed); len(m) == 2 {
			addImport(m[1])
			continue
		}
	}
	return imports
}

func declaredSymbolOnLine(line string) string {
	if m := goMethodRe.FindStringSubmatch(line); len(m) == 2 {
		return m[1]
	}
	if m := declarationRe.FindStringSubmatch(line); len(m) == 2 {
		return m[1]
	}
	if m := assignRe.FindStringSubmatch(line); len(m) == 2 {
		return m[1]
	}
	return ""
}

func symbolAt(lines []string, start, end int) string {
	for i := start; i < end && i < len(lines); i++ {
		if sym := extractLineSymbolCandidate(lines[i]); sym != "" {
			return sym
		}
	}
	for i := start - 1; i >= 0 && i < len(lines); i-- {
		if sym := extractLineSymbolCandidate(lines[i]); sym != "" {
			if estimateSymbolEndLine(lines, i) > start {
				return sym
			}
			return ""
		}
	}
	return ""
}

func chunkFile(path, content string, cfg Config) []Chunk {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil
	}
	size := cfg.ChunkLines
	if size <= 0 {
		size = 80
	}
	overlap := cfg.ChunkOverlap
	if overlap < 0 || overlap >= size {
		overlap = 0
	}
	maxChunks := cfg.MaxFileChunks
	if maxChunks <= 0 {
		maxChunks = 200
	}
	var chunks []Chunk
	for start := 0; start < len(lines); {
		end := start + size
		if end > len(lines) {
			end = len(lines)
		}
		splitAtSymbol := false
		if len(chunks) < maxChunks-4 {
			hasFirstSymbol := false
			for i := start; i < end; i++ {
				if declaredSymbolOnLine(lines[i]) != "" {
					if hasFirstSymbol && i > start {
						end = i
						splitAtSymbol = true
						break
					}
					hasFirstSymbol = true
				}
			}
		}
		text := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		if text != "" {
			chunks = append(chunks, Chunk{
				Path:      path,
				Symbol:    symbolAt(lines, start, end),
				StartLine: start + 1,
				EndLine:   end,
				Text:      text,
			})
			if len(chunks) >= maxChunks {
				break
			}
		}
		if end >= len(lines) {
			break
		}
		next := end - overlap
		if splitAtSymbol || next <= start {
			next = end
		}
		start = next
	}
	return chunks
}

var protectedWorktreeSegments = map[string]bool{
	".git":      true,
	".agents":   true,
	".opencode": true,
	".claude":   true,
	".hoxi":     true,
	".ssh":      true,
	".aws":      true,
	".gnupg":    true,
	".kube":     true,
	".docker":   true,
	".azure":    true,
	".config":   true,
	".envrc":    true,
}

// secretFile reports files that commonly hold credentials or protected Hoxi state.
// The index is written to ~/.hoxi in plain text, so they are never indexed or inspected.
func secretFile(lower string) bool {
	if lower == ".env" || strings.HasPrefix(lower, ".env.") || strings.HasSuffix(lower, ".env") {
		return true
	}
	if hoxipaths.IsProtectedStateFile(lower) || lower == "hoxi.db" || strings.HasPrefix(lower, "hoxi.db-") {
		return true
	}
	switch lower {
	case ".npmrc", ".pypirc", ".netrc", ".git-credentials", "credentials", "credentials.json", ".credentials.json",
		"id_rsa", "id_ed25519", "id_ecdsa", "id_dsa", "authorized_keys", ".envrc",
		".zshrc", ".zshenv", ".zprofile", ".zlogin", ".bashrc", ".bash_profile", ".bash_login", ".profile",
		".gitconfig", ".curlrc", ".wgetrc":
		return true
	}
	for _, suffix := range []string{".tfvars", ".tfvars.json", ".tfstate", ".pem", ".key", ".p12", ".pfx", ".keystore"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func isProtectedOrSecretRelPath(rel string) bool {
	slash := filepath.ToSlash(strings.TrimSpace(rel))
	if slash == "" || slash == "." {
		return true
	}
	lowerSlash := strings.ToLower(slash)
	if lowerSlash == ".vscode/settings.json" || strings.HasSuffix(lowerSlash, "/.vscode/settings.json") {
		return true
	}
	for _, seg := range strings.Split(slash, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." || ignoredFolders[seg] || ignoredFolders[strings.ToLower(seg)] || protectedWorktreeSegments[strings.ToLower(seg)] {
			return true
		}
	}
	return !indexableFile(slash)
}

func isForbiddenWorkspaceRoot(absDir string) bool {
	slash := filepath.ToSlash(strings.TrimSpace(absDir))
	segs := strings.Split(slash, "/")
	for i, raw := range segs {
		seg := strings.ToLower(strings.TrimSpace(raw))
		if seg == "" {
			continue
		}
		if seg == ".hoxi" {
			// Isolated session worktrees live at <project>/.hoxi/worktrees/<id>;
			// any other path inside .hoxi (e.g. ~/.hoxi or <project>/.hoxi/memory) is forbidden.
			if i+2 < len(segs) && strings.ToLower(segs[i+1]) == "worktrees" && strings.TrimSpace(segs[i+2]) != "" {
				continue
			}
			return true
		}
		switch seg {
		case ".git", ".claude", ".opencode", ".agents", ".ssh", ".aws", ".gnupg", ".kube":
			return true
		}
	}
	return false
}

func indexableFile(path string) bool {
	base := filepath.Base(path)
	lower := strings.ToLower(base)
	if secretFile(lower) {
		return false
	}
	switch lower {
	case "dockerfile", "makefile", "cmakelists.txt", "gemfile", "rakefile", "procfile", "justfile":
		return true
	}
	ext := strings.ToLower(filepath.Ext(base))
	if indexableExtensions[ext] {
		return true
	}
	return false
}

// maxFilesPerDir bounds how many files a single flat directory can contribute
// so static dump folders do not exhaust the workspace indexing budget.
const maxFilesPerDir = 500

// build walks the workspace and incrementally reuses chunks for unchanged
// files. It mirrors Kilo's ignore list and file-size bounds.
func (s *Service) build(directory string, previous *Index) (*Index, error) {
	cfg := s.cfg
	index := &Index{Version: indexVersion, Directory: directory, BuiltAt: s.now(), Files: map[string]*FileRecord{}}
	var total int64
	files := 0
	dirCounts := make(map[string]int)
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != directory {
				name := entry.Name()
				lowerName := strings.ToLower(name)
				if ignoredFolders[name] || ignoredFolders[lowerName] || protectedWorktreeSegments[lowerName] {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if files >= cfg.MaxFiles || total >= cfg.MaxTotalBytes {
			return filepath.SkipAll
		}
		if !indexableFile(path) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() <= 0 || info.Size() > cfg.MaxFileBytes {
			return nil
		}
		rel, err := filepath.Rel(directory, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if isProtectedOrSecretRelPath(rel) {
			return nil
		}
		parentDir := filepath.Dir(rel)
		if dirCounts[parentDir] >= maxFilesPerDir {
			return nil
		}
		dirCounts[parentDir]++
		files++
		total += info.Size()
		if prev := previous.Files[rel]; prev != nil && prev.Size == info.Size() && prev.ModTime == info.ModTime().UnixMilli() {
			index.Files[rel] = prev
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if isBinary(raw) {
			return nil
		}
		sum := sha256.Sum256(raw)
		hash := hex.EncodeToString(sum[:])
		if prev := previous.Files[rel]; prev != nil && prev.Hash == hash {
			prev.Size, prev.ModTime = info.Size(), info.ModTime().UnixMilli()
			index.Files[rel] = prev
			return nil
		}
		contentStr := string(raw)
		chunks := chunkFile(rel, contentStr, cfg)
		imports := extractFileImports(contentStr)
		index.Files[rel] = &FileRecord{
			Path: rel, ModTime: info.ModTime().UnixMilli(), Size: info.Size(), Hash: hash, Imports: imports, Chunks: chunks,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return index, nil
}

func isBinary(raw []byte) bool {
	limit := len(raw)
	if limit > 8000 {
		limit = 8000
	}
	for i := 0; i < limit; i++ {
		if raw[i] == 0 {
			return true
		}
	}
	return false
}
