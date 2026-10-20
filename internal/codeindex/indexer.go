package codeindex

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	Path    string  `json:"path"`
	ModTime int64   `json:"modTime"`
	Size    int64   `json:"size"`
	Hash    string  `json:"hash"`
	Chunks  []Chunk `json:"chunks"`
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
	".hoxi": true, "worktrees": true, "Pods": true, "DerivedData": true,
	".venv": true, "venv": true, "coverage": true, ".bundle": true,
}

var indexableExtensions = map[string]bool{}

func init() {
	for _, ext := range strings.Split(
		"go,ts,tsx,js,jsx,mjs,cjs,py,pyi,rb,rs,java,kt,kts,swift,c,h,cc,cpp,cxx,hpp,cs,php,scala,sh,bash,zsh,fish,ps1,"+
			"sql,md,mdx,json,jsonc,yaml,yml,toml,xml,html,htm,css,scss,sass,less,vue,svelte,dart,lua,r,ex,exs,erl,hs,"+
			"clj,cljs,tf,tfvars,hcl,proto,graphql,gql,ini,cfg,conf,env,lock,gradle,mk,cmake", ",") {
		if ext != "" {
			indexableExtensions["."+ext] = true
		}
	}
}

var (
	declarationRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:default\s+)?(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:function|class|interface|type|struct|enum|trait|impl|def|func|fn|const|let|var|module|namespace|object|record)\s+([A-Za-z_$][\w$]*)`)
	assignRe      = regexp.MustCompile(`(?m)^\s*([A-Za-z_$][\w$]*)\s*(?::[^=\n]+)?[:=]\s*(?:async\s*)?(?:function|\(|=>)`)
)

func symbolAt(lines []string, start, end int) string {
	best := ""
	for i := start; i < end && i < len(lines); i++ {
		line := lines[i]
		if m := declarationRe.FindStringSubmatch(line); len(m) == 2 {
			best = m[1]
		}
	}
	if best == "" {
		for i := start; i < end && i < len(lines); i++ {
			if m := assignRe.FindStringSubmatch(lines[i]); len(m) == 2 {
				best = m[1]
			}
		}
	}
	return best
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
	var chunks []Chunk
	for start := 0; start < len(lines); {
		end := start + size
		if end > len(lines) {
			end = len(lines)
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
			if len(chunks) >= cfg.MaxFileChunks {
				break
			}
		}
		if end >= len(lines) {
			break
		}
		next := end - overlap
		if next <= start {
			next = end
		}
		start = next
	}
	return chunks
}

func indexableFile(path string) bool {
	base := filepath.Base(path)
	lower := strings.ToLower(base)
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

// build walks the workspace and incrementally reuses chunks for unchanged
// files. It mirrors Kilo's ignore list and file-size bounds.
func (s *Service) build(directory string, previous *Index) (*Index, error) {
	cfg := s.cfg
	index := &Index{Version: indexVersion, Directory: directory, BuiltAt: s.now(), Files: map[string]*FileRecord{}}
	var total int64
	files := 0
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != directory && ignoredFolders[entry.Name()] {
				return filepath.SkipDir
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
		chunks := chunkFile(rel, string(raw), cfg)
		index.Files[rel] = &FileRecord{
			Path: rel, ModTime: info.ModTime().UnixMilli(), Size: info.Size(), Hash: hash, Chunks: chunks,
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
