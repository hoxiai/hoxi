package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxReadMediaBytes  = 10 * 1024 * 1024
	maxReadOutputBytes = maxToolOutputBytes - 512
)

type readTool struct{}

func (t *readTool) Name() string { return "read" }
func (t *readTool) Description() string {
	return "Read numbered text lines or list directory entries with offset/limit. Inspect PNG/JPEG/WebP/GIF images and PDFs inline."
}
func (t *readTool) IsReadOnly() bool { return true }
func (t *readTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filePath": map[string]any{"type": "string", "description": "File or directory path"},
			"offset":   map[string]any{"type": "integer", "description": "1-based starting line or directory entry"},
			"limit":    map[string]any{"type": "integer", "description": "Maximum lines or entries; text defaults to 480 lines"},
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
		start := max(1, args.Offset) - 1
		end := len(entries)
		var listing string
		var meta map[string]any
		if start > 0 && start >= end {
			listing = fmt.Sprintf("(directory has %d entries; offset %d is past the end)", end, args.Offset)
		} else {
			if args.Limit > 0 {
				end = start + min(args.Limit, end-start)
			}
			var next int
			listing, next = fileListPage(start, end, func(i int) string {
				name := entries[i].Name()
				if entries[i].IsDir() {
					name += "/"
				}
				return name
			})
			if next < len(entries) {
				meta = map[string]any{"nextOffset": next + 1}
				listing += fmt.Sprintf("\n(showing entries %d-%d of %d; pass offset=%d to continue)", start+1, next, len(entries), next+1)
			}
		}
		return ToolOutput{Title: p, Output: appendSubdirectoryRules(tc, realTarget, listing), Metadata: meta}, nil
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
		limit = 480
	}
	if start > len(allLines) {
		tc.FileRevisions.RecordPartialRead(realTarget, raw)
		return ToolOutput{Title: p, Output: fmt.Sprintf("(file has %d lines; offset %d is past EOF)", len(allLines), start)}, nil
	}
	end := start - 1 + min(limit, len(allLines)-start+1)
	var b strings.Builder
	anyLineTruncated := false
	stoppedByBytes := false
	actualEnd := start - 1
	for i := start; i <= end; i++ {
		formatted, trunc := formatReadLine(i, allLines[i-1])
		if i > start && b.Len()+len(formatted) > maxReadOutputBytes {
			stoppedByBytes = true
			break
		}
		b.WriteString(formatted)
		actualEnd = i
		if trunc {
			anyLineTruncated = true
		}
	}
	renderedLines := b.String()
	if actualEnd < len(allLines) {
		if stoppedByBytes {
			remainingBytes := 0
			for i := actualEnd + 1; i <= end; i++ {
				remainingBytes += len(allLines[i-1]) + 1
			}
			fmt.Fprintf(&b, "\n(showing lines %d-%d of %d; %d bytes truncated at line boundary — pass offset=%d to continue, or inspect a symbol with hoxi_code_index)", start, actualEnd, len(allLines), remainingBytes, actualEnd+1)
		} else {
			fmt.Fprintf(&b, "\n(showing lines %d-%d of %d; pass offset=%d to continue, or inspect a symbol with hoxi_code_index)", start, actualEnd, len(allLines), actualEnd+1)
		}
	}
	body := b.String()
	if notice, ok := tryBuildUnchangedReadNotice(tc.VisibleMessages, tc.WorktreeDir, realTarget, p, renderedLines, raw, start, actualEnd, len(allLines)); ok {
		body = notice
	}
	outText := appendSubdirectoryRules(tc, realTarget, body)
	// The byte cap stops before the next line. All lines through actualEnd were delivered.
	deliveredComplete := !anyLineTruncated && len(ensureValidUTF8(body)) <= maxToolOutputBytes
	tc.FileRevisions.RecordReadLines(realTarget, raw, start, actualEnd, len(allLines), deliveredComplete)
	return ToolOutput{Title: p, Output: outText}, nil
}

type globTool struct{}

func (t *globTool) Name() string { return "glob" }
func (t *globTool) Description() string {
	return "Find files matching a glob pattern (e.g. *.go, internal/**/*.ts) inside the workspace, ordered by most recently modified first."
}
func (t *globTool) IsReadOnly() bool { return true }
func (t *globTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string", "description": "Glob pattern to match"},
			"path":    map[string]any{"type": "string", "description": "Optional directory to search"},
			"offset":  map[string]any{"type": "integer", "minimum": 0, "description": "Skip this many matching files; use returned nextOffset to continue"},
		},
		"required": []string{"pattern"},
	}
}

func (t *globTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Offset  int    `json:"offset"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "glob", IsError: true}, err
	}
	if args.Offset < 0 {
		return ToolOutput{Title: "glob", IsError: true}, fmt.Errorf("offset must be nonnegative")
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
	type globEntry struct {
		path    string
		modTime time.Time
	}
	ignorer := newWorkspaceIgnorer(tc.WorktreeDir, root)
	var matches []globEntry
	const (
		maxMatches = 200
		maxCollect = 5000
	)
	cappedCollect := false
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
			var modTime time.Time
			if info, iErr := d.Info(); iErr == nil {
				modTime = info.ModTime()
			}
			matches = append(matches, globEntry{path: relSlash, modTime: modTime})
			if len(matches) >= maxCollect {
				cappedCollect = true
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return ToolOutput{Title: pat, IsError: true}, err
	}
	sort.Slice(matches, func(i, j int) bool {
		if !matches[i].modTime.Equal(matches[j].modTime) {
			return matches[i].modTime.After(matches[j].modTime)
		}
		return matches[i].path < matches[j].path
	})
	if len(matches) == 0 {
		return ToolOutput{Title: pat, Output: "(no matching files)"}, nil
	}
	if args.Offset >= len(matches) {
		return ToolOutput{Title: pat, Output: "(no more matching files at this offset)"}, nil
	}
	out, pageEnd := fileListPage(args.Offset, min(len(matches), args.Offset+maxMatches), func(i int) string {
		return matches[i].path
	})
	meta := map[string]any{}
	if pageEnd < len(matches) || cappedCollect {
		if pageEnd < len(matches) {
			meta["nextOffset"] = pageEnd
			out += fmt.Sprintf("\n(showing matches %d-%d of %d; repeat glob with offset=%d)", args.Offset+1, pageEnd, len(matches), pageEnd)
		} else {
			out += fmt.Sprintf("\n(truncated at %d matches)", len(matches))
		}
	}
	return ToolOutput{Title: pat, Output: out, Metadata: meta}, nil
}

// Stop before the registry's output cap so the cursor follows a complete path.
func fileListPage(start, end int, nameAt func(int) string) (string, int) {
	var out strings.Builder
	for i := start; i < end; i++ {
		name := ensureValidUTF8(nameAt(i))
		if i > start {
			if out.Len()+1+len(name) > maxReadOutputBytes {
				return out.String(), i
			}
			out.WriteByte('\n')
		}
		out.WriteString(name)
	}
	return out.String(), end
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
