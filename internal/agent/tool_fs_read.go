package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const maxReadMediaBytes = 10 * 1024 * 1024

type readTool struct{}

func (t *readTool) Name() string { return "read" }
func (t *readTool) Description() string {
	return "Read a file from the workspace with line numbers (supports offset/limit for text, and inline multimodal inspection for PNG/JPEG/WebP/GIF images and PDF documents)."
}
func (t *readTool) IsReadOnly() bool { return true }
func (t *readTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filePath": map[string]any{"type": "string", "description": "Path to the file to read"},
			"offset":   map[string]any{"type": "integer", "description": "1-based starting line number"},
			"limit":    map[string]any{"type": "integer", "description": "Maximum number of lines to read"},
		},
		"required": []string{"filePath"},
	}
}

func readMediaMimeType(target string) string {
	switch strings.ToLower(filepath.Ext(target)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".pdf":
		return "application/pdf"
	default:
		return ""
	}
}

func (t *readTool) Execute(_ context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		FilePath string `json:"filePath"`
		Path     string `json:"path"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "read", IsError: true}, err
	}
	p := strings.TrimSpace(args.FilePath)
	if p == "" {
		p = strings.TrimSpace(args.Path)
	}
	target, err := resolveWorkspacePath(tc.WorktreeDir, p, false)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if !tc.PermissionVetted {
		if !isRealPathWithin(tc.WorktreeDir, realTarget) {
			return ToolOutput{Title: p, IsError: true}, fmt.Errorf("reading %q outside workspace requires approval", p)
		}
		if isSensitiveAgentPath(target) || isSensitiveAgentPath(realTarget) {
			return ToolOutput{Title: p, IsError: true}, fmt.Errorf("reading sensitive credential or key file %q is blocked", filepath.Base(realTarget))
		}
	}
	info, err := os.Stat(realTarget)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(realTarget)
		if err != nil {
			return ToolOutput{Title: p, IsError: true}, err
		}
		var lines []string
		for _, e := range entries {
			suffix := ""
			if e.IsDir() {
				suffix = "/"
			}
			lines = append(lines, e.Name()+suffix)
		}
		return ToolOutput{Title: p, Output: appendSubdirectoryRules(tc, realTarget, strings.Join(lines, "\n"))}, nil
	}
	if mimeType := readMediaMimeType(target); mimeType != "" {
		if info.Size() > maxReadMediaBytes {
			return ToolOutput{
				Title:  p,
				Output: fmt.Sprintf("media file %s exceeds inline read limit (%d > %d bytes)", filepath.Base(target), info.Size(), maxReadMediaBytes),
			}, nil
		}
		raw, err := os.ReadFile(realTarget)
		if err != nil {
			return ToolOutput{Title: p, IsError: true}, err
		}
		tc.FileRevisions.RecordBytes(realTarget, raw)
		if len(raw) == 0 {
			return ToolOutput{Title: p, Output: "(empty file)"}, nil
		}
		baseName := filepath.Base(target)
		return ToolOutput{
			Title:  p,
			Output: fmt.Sprintf("Read %s (%s, %d bytes)", baseName, mimeType, len(raw)),
			Media: []ContentBlock{{
				Type:     ContentMedia,
				MimeType: mimeType,
				Data:     base64.StdEncoding.EncodeToString(raw),
				Filename: baseName,
			}},
		}, nil
	}
	raw, err := os.ReadFile(realTarget)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if bytes.IndexByte(raw[:min(len(raw), 4096)], 0) >= 0 {
		tc.FileRevisions.RecordPartialRead(realTarget, raw)
		return ToolOutput{Title: p, Output: fmt.Sprintf("binary file (%d bytes)", len(raw))}, nil
	}
	allLines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	start := max(1, args.Offset)
	limit := args.Limit
	if limit <= 0 {
		limit = 2000
	}
	if start > len(allLines) {
		tc.FileRevisions.RecordPartialRead(realTarget, raw)
		return ToolOutput{Title: p, Output: fmt.Sprintf("(file has %d lines; offset %d is past EOF)", len(allLines), start)}, nil
	}
	end := min(len(allLines), start+limit-1)
	var b strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%d: %s\n", i, allLines[i-1])
	}
	if end < len(allLines) {
		fmt.Fprintf(&b, "\n(showing lines %d-%d of %d)", start, end, len(allLines))
	}
	outText := appendSubdirectoryRules(tc, realTarget, b.String())
	deliveredComplete := len(ensureValidUTF8(outText)) <= maxToolOutputBytes
	tc.FileRevisions.RecordReadLines(realTarget, raw, start, end, len(allLines), deliveredComplete)
	return ToolOutput{Title: p, Output: outText}, nil
}

type globTool struct{}

func (t *globTool) Name() string { return "glob" }
func (t *globTool) Description() string {
	return "Find files matching a glob pattern (e.g. *.go, internal/**/*.ts) inside the workspace."
}
func (t *globTool) IsReadOnly() bool { return true }
func (t *globTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string", "description": "Glob pattern to match"},
			"path":    map[string]any{"type": "string", "description": "Optional directory to search"},
		},
		"required": []string{"pattern"},
	}
}

func (t *globTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "glob", IsError: true}, err
	}
	root := tc.WorktreeDir
	if strings.TrimSpace(args.Path) != "" {
		resolved, err := resolveWorkspacePath(tc.WorktreeDir, args.Path, false)
		if err != nil {
			return ToolOutput{Title: args.Pattern, IsError: true}, err
		}
		root = resolved
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ToolOutput{Title: args.Pattern, IsError: true}, err
	}
	if !tc.PermissionVetted && !isRealPathWithin(tc.WorktreeDir, realRoot) {
		return ToolOutput{Title: args.Pattern, IsError: true}, fmt.Errorf("glob path %q resolves outside workspace", args.Path)
	}
	pat := strings.TrimSpace(args.Pattern)
	if pat == "" {
		pat = "*"
	}
	ignorer := newWorkspaceIgnorer(tc.WorktreeDir, root)
	var matches []string
	const maxMatches = 200
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if p != root {
				if shouldSkipSearchDir(d.Name()) {
					return filepath.SkipDir
				}
				if ignorer != nil && ignorer.IsIgnored(p, true) && !ignorer.CanReincludeUnderDir(p) {
					return filepath.SkipDir
				}
			}
			ignorer.EnterDir(p)
			return nil
		}
		if isSensitiveAgentPathLexical(p) {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			realFile, lErr := filepath.EvalSymlinks(p)
			if lErr != nil || !isLexicalPathWithin(realRoot, realFile) || isSensitiveAgentPathLexical(realFile) {
				return nil
			}
		}
		if ignorer != nil && ignorer.IsIgnored(p, false) {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if matchGlob(pat, relSlash) {
			matches = append(matches, relSlash)
			if len(matches) >= maxMatches {
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return ToolOutput{Title: pat, IsError: true}, err
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return ToolOutput{Title: pat, Output: "(no matching files)"}, nil
	}
	out := strings.Join(matches, "\n")
	if len(matches) >= maxMatches {
		out += fmt.Sprintf("\n(truncated at %d matches)", maxMatches)
	}
	return ToolOutput{Title: pat, Output: out}, nil
}

type grepTool struct{}

func (t *grepTool) Name() string { return "grep" }
func (t *grepTool) Description() string {
	return "Search file contents in the workspace using a regular expression or literal string."
}
func (t *grepTool) IsReadOnly() bool { return true }
func (t *grepTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string", "description": "Regex or text pattern to search for"},
			"path":    map[string]any{"type": "string", "description": "Optional file or directory to search"},
			"include": map[string]any{"type": "string", "description": "Optional filename glob filter (e.g. *.go)"},
		},
		"required": []string{"pattern"},
	}
}

func (t *grepTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Include string `json:"include"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "grep", IsError: true}, err
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		re = regexp.MustCompile(regexp.QuoteMeta(args.Pattern))
	}
	root := tc.WorktreeDir
	if strings.TrimSpace(args.Path) != "" {
		resolved, err := resolveWorkspacePath(tc.WorktreeDir, args.Path, false)
		if err != nil {
			return ToolOutput{Title: args.Pattern, IsError: true}, err
		}
		root = resolved
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ToolOutput{Title: args.Pattern, IsError: true}, err
	}
	rootInfo, err := os.Stat(realRoot)
	if err != nil {
		return ToolOutput{Title: args.Pattern, IsError: true}, err
	}
	if !tc.PermissionVetted {
		if !isRealPathWithin(tc.WorktreeDir, realRoot) {
			return ToolOutput{Title: args.Pattern, IsError: true}, fmt.Errorf("grep path %q resolves outside workspace", args.Path)
		}
		if !rootInfo.IsDir() && (isSensitiveAgentPath(root) || isSensitiveAgentPath(realRoot)) {
			return ToolOutput{Title: args.Pattern, IsError: true}, fmt.Errorf("searching sensitive credential or key file %q is blocked", filepath.Base(realRoot))
		}
	}

	type grepCandidate struct {
		displayPath string
		openPath    string
		expectInfo  os.FileInfo
	}
	var files []grepCandidate
	const maxGrepFileSize = 2 * 1024 * 1024

	if !rootInfo.IsDir() {
		if rootInfo.Mode().IsRegular() && rootInfo.Size() <= maxGrepFileSize {
			if args.Include == "" || matchGlob(args.Include, filepath.Base(root)) {
				files = append(files, grepCandidate{displayPath: root, openPath: realRoot, expectInfo: rootInfo})
			}
		}
	} else {
		ignorer := newWorkspaceIgnorer(tc.WorktreeDir, root)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if walkErr != nil {
				return nil
			}
			if d.IsDir() {
				if p != root {
					if shouldSkipSearchDir(d.Name()) {
						return filepath.SkipDir
					}
					if ignorer != nil && ignorer.IsIgnored(p, true) && !ignorer.CanReincludeUnderDir(p) {
						return filepath.SkipDir
					}
				}
				ignorer.EnterDir(p)
				return nil
			}
			if isSensitiveAgentPathLexical(p) {
				return nil
			}
			if ignorer != nil && ignorer.IsIgnored(p, false) {
				return nil
			}
			if args.Include != "" {
				relInc, relErr := filepath.Rel(root, p)
				relIncSlash := d.Name()
				if relErr == nil && relInc != "." {
					relIncSlash = filepath.ToSlash(relInc)
				}
				if !matchGlob(args.Include, relIncSlash) {
					return nil
				}
			}
			realFile, rErr := filepath.EvalSymlinks(p)
			if rErr != nil {
				return nil
			}
			if !isLexicalPathWithin(realRoot, realFile) || hasSkippedSearchSegment(realRoot, realFile) {
				return nil
			}
			if isSensitiveAgentPathLexical(realFile) {
				return nil
			}
			if realSt, sErr := os.Stat(realFile); sErr == nil && realSt.Mode().IsRegular() && realSt.Size() <= maxGrepFileSize {
				files = append(files, grepCandidate{displayPath: p, openPath: realFile, expectInfo: realSt})
			}
			return nil
		})
	}

	type hit struct {
		rel  string
		line int
		text string
	}
	var mu sync.Mutex
	var hits []hit
	const maxHits = 150
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup

	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		mu.Lock()
		full := len(hits) >= maxHits
		mu.Unlock()
		if full {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(cand grepCandidate) {
			defer wg.Done()
			defer func() { <-sem }()
			fh, err := os.Open(cand.openPath)
			if err != nil {
				return
			}
			defer fh.Close()
			fhSt, err := fh.Stat()
			if err != nil || !fhSt.Mode().IsRegular() || (cand.expectInfo != nil && !os.SameFile(cand.expectInfo, fhSt)) {
				return
			}
			rel, relErr := filepath.Rel(tc.WorktreeDir, cand.displayPath)
			if relErr != nil || strings.HasPrefix(rel, "..") {
				rel = cand.displayPath
			}
			relSlash := filepath.ToSlash(rel)
			sc := bufio.NewScanner(fh)
			sc.Buffer(make([]byte, 32*1024), 1024*1024)
			lineNo := 0
			for sc.Scan() {
				lineNo++
				b := sc.Bytes()
				if bytes.IndexByte(b, 0) >= 0 {
					return
				}
				if re.Match(b) {
					mu.Lock()
					if len(hits) < maxHits {
						hits = append(hits, hit{rel: relSlash, line: lineNo, text: strings.TrimSpace(string(b))})
					}
					mu.Unlock()
				}
			}
		}(f)
	}
	wg.Wait()

	if len(hits) == 0 {
		return ToolOutput{Title: args.Pattern, Output: "(no matches found)"}, nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].rel != hits[j].rel {
			return hits[i].rel < hits[j].rel
		}
		return hits[i].line < hits[j].line
	})
	var out strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&out, "%s:%d: %s\n", h.rel, h.line, h.text)
	}
	return ToolOutput{Title: args.Pattern, Output: out.String()}, nil
}

func shouldSkipSearchDir(name string) bool {
	switch name {
	case ".git", "node_modules", "dist", "build", ".hoxi", ".opencode", "target", ".dart_tool",
		".next", ".nuxt", ".svelte-kit", ".turbo", "coverage", "vendor",
		".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".gradle", "Pods":
		return true
	}
	return false
}

func hasSkippedSearchSegment(realRoot, realTarget string) bool {
	rel, err := filepath.Rel(realRoot, realTarget)
	if err != nil || rel == "." {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) <= 1 {
		return false
	}
	for _, seg := range parts[:len(parts)-1] {
		if shouldSkipSearchDir(seg) {
			return true
		}
	}
	return false
}

var sensitiveAgentBasenames = map[string]bool{
	".envrc": true, ".git-credentials": true, ".netrc": true, ".npmrc": true,
	".pypirc": true, ".credentials.json": true, "oauth_creds.json": true,
	"jetski-standalone-oauth-token": true, "authorized_keys": true,
	"id_rsa": true, "id_ed25519": true, "id_ecdsa": true, "id_dsa": true,
	".zshrc": true, ".zshenv": true, ".zprofile": true, ".zlogin": true,
	".bashrc": true, ".bash_profile": true, ".bash_login": true, ".profile": true,
}

var sensitiveAgentDirFragments = []string{
	"/.ssh/", "/.aws/", "/.gnupg/", "/.kube/", "/.codex/",
	"/.docker/", "/.azure/", "/.config/opencode/", "/.local/share/opencode/",
	"/.config/hoxi/", "/.config/claude/", "/.config/gh/", "/.config/gcloud/",
	"/library/keychains/",
}

func stripAgentWorktreePrefix(slash string) string {
	for _, marker := range []string{"/.hoxi/worktrees/", ".hoxi/worktrees/"} {
		if idx := strings.LastIndex(slash, marker); idx != -1 {
			rest := slash[idx+len(marker):]
			if next := strings.IndexByte(rest, '/'); next != -1 {
				return rest[next+1:]
			}
			return ""
		}
	}
	return slash
}

func isSensitiveAgentPath(fullPath string) bool {
	if isSensitiveAgentPathLexical(fullPath) {
		return true
	}
	if real, ok := evalAgentRealPath(fullPath, 0); ok && real != fullPath {
		return isSensitiveAgentPathLexical(real)
	}
	return false
}

func isSensitiveAgentPathLexical(fullPath string) bool {
	trimmed := strings.TrimSpace(fullPath)
	if trimmed == "" {
		return false
	}
	rawSlash := strings.TrimRight(strings.ToLower(filepath.ToSlash(filepath.Clean(trimmed))), "/")
	slash := strings.TrimRight(stripAgentWorktreePrefix(rawSlash), "/")
	if slash == "" {
		return false
	}
	base := slash[strings.LastIndex(slash, "/")+1:]
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		switch base {
		case ".env.example", ".env.sample", ".env.template", ".env.dist":
			return false
		}
		return true
	}
	if sensitiveAgentBasenames[base] {
		return true
	}
	switch filepath.Ext(base) {
	case ".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".kdbx":
		return true
	}
	norm := "/" + strings.TrimLeft(slash, "/") + "/"
	if strings.Contains(norm, "/.hoxi/skills/") || strings.Contains(norm, "/.hoxi/attachments/") ||
		strings.Contains(norm, "/.claude/skills/") {
		return false
	}
	if strings.Contains(norm, "/.hoxi/") || strings.Contains(norm, "/.claude/") {
		return true
	}
	for _, frag := range sensitiveAgentDirFragments {
		if strings.Contains(norm, frag) {
			return true
		}
	}
	return false
}
