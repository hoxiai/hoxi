package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/osopen"
)

const (
	maxLocalFilePreviewBytes = 10 * 1024 * 1024
	maxValidateBatchPaths    = 200
	maxFallbackWalkEntries   = 6000
)

var (
	hashLineSuffixRE  = regexp.MustCompile(`(?i)#L(\d+)(?:[-–—]L?\d+)?$`)
	colonLineSuffixRE = regexp.MustCompile(`:(\d+)(?:[:\-–—]\d+)?$`)
)

func expandUserPath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func cleanRequestedFilePath(raw string) (string, int) {
	str := strings.TrimSpace(raw)
	if str == "" {
		return "", 0
	}
	if strings.HasPrefix(strings.ToLower(str), "file://") {
		if u, err := url.Parse(str); err == nil && u.Path != "" {
			decoded := u.Path
			if unescaped, uErr := url.PathUnescape(u.Path); uErr == nil {
				decoded = unescaped
			}
			if len(decoded) >= 4 && decoded[0] == '/' && decoded[2] == ':' &&
				((decoded[1] >= 'A' && decoded[1] <= 'Z') || (decoded[1] >= 'a' && decoded[1] <= 'z')) {
				decoded = decoded[1:]
			}
			str = decoded
			if u.Fragment != "" {
				str += "#" + u.Fragment
			}
		} else {
			str = strings.TrimSpace(str[len("file://"):])
			if unescaped, uErr := url.PathUnescape(str); uErr == nil {
				str = unescaped
			}
		}
	}

	line := 0
	if m := hashLineSuffixRE.FindStringSubmatchIndex(str); len(m) >= 4 {
		line, _ = strconv.Atoi(str[m[2]:m[3]])
		str = str[:m[0]]
	} else if m := colonLineSuffixRE.FindStringSubmatchIndex(str); len(m) >= 4 {
		prefix := str[:m[0]]
		// Avoid stripping Windows drive root like "C:"
		if len(prefix) > 1 || (len(prefix) == 1 && !((prefix[0] >= 'A' && prefix[0] <= 'Z') || (prefix[0] >= 'a' && prefix[0] <= 'z'))) {
			line, _ = strconv.Atoi(str[m[2]:m[3]])
			str = prefix
		}
	}

	str = strings.TrimSpace(str)
	str = expandUserPath(str)
	return str, line
}

func isPathWithinDir(baseDir, targetPath string) bool {
	if strings.TrimSpace(baseDir) == "" || strings.TrimSpace(targetPath) == "" {
		return false
	}
	cleanBase, baseOK := canonicalDirectory(baseDir)
	if !baseOK {
		absBase, err := filepath.Abs(baseDir)
		if err != nil {
			return false
		}
		cleanBase = filepath.Clean(absBase)
	}
	cleanTarget := filepath.Clean(targetPath)
	if resolved, err := filepath.EvalSymlinks(targetPath); err == nil {
		if absResolved, err := filepath.Abs(resolved); err == nil {
			cleanTarget = filepath.Clean(absResolved)
		}
	} else if absTarget, err := filepath.Abs(targetPath); err == nil {
		cleanTarget = filepath.Clean(absTarget)
	}
	rel, err := filepath.Rel(cleanBase, cleanTarget)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func (g *Gateway) candidateBaseDirs(rawDir, sessionID string) []string {
	var dirs []string
	seen := make(map[string]struct{})
	add := func(d string) {
		cleaned := strings.TrimSpace(expandUserPath(d))
		if cleaned == "" {
			return
		}
		abs, err := filepath.Abs(cleaned)
		if err == nil {
			cleaned = filepath.Clean(abs)
		} else {
			cleaned = filepath.Clean(cleaned)
		}
		if _, exists := seen[cleaned]; exists {
			return
		}
		seen[cleaned] = struct{}{}
		dirs = append(dirs, cleaned)
	}

	if sessionID != "" && g.worktrees != nil {
		if binding, known, err := g.worktrees.BindingForSession(sessionID); err == nil && known && binding.Directory != "" {
			add(binding.Directory)
			return dirs
		}
	}
	if rawDir != "" {
		add(rawDir)
		if len(dirs) > 0 {
			return dirs
		}
	}
	if g.rootDir != "" {
		add(g.rootDir)
	}
	return dirs
}

func shouldSkipFallbackDir(name string) bool {
	if name == "" {
		return true
	}
	return skipFileSearchDir(name)
}

func findFallbackInDirs(dirs []string, relQuery string) (string, os.FileInfo) {
	cleanedSlash := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(relQuery)), "./")
	if cleanedSlash == "" || cleanedSlash == "." || strings.HasPrefix(cleanedSlash, "..") {
		return "", nil
	}
	targetBase := filepath.Base(cleanedSlash)
	if targetBase == "" || targetBase == "." || targetBase == "/" {
		return "", nil
	}
	hasSlash := strings.Contains(cleanedSlash, "/")
	suffixPattern := "/" + cleanedSlash

	var basenameMatchPath string
	var basenameMatchInfo os.FileInfo
	visited := 0

	for _, root := range dirs {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		var foundExactSuffix string
		var foundExactInfo os.FileInfo

		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() {
				if p != root && shouldSkipFallbackDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			visited++
			if visited > maxFallbackWalkEntries {
				return filepath.SkipAll
			}
			if d.Name() != targetBase {
				return nil
			}
			slashPath := filepath.ToSlash(p)
			if hasSlash && strings.HasSuffix(slashPath, suffixPattern) {
				if fi, statErr := d.Info(); statErr == nil && !fi.IsDir() {
					foundExactSuffix = p
					foundExactInfo = fi
					return filepath.SkipAll
				}
			}
			if basenameMatchPath == "" {
				if fi, statErr := d.Info(); statErr == nil && !fi.IsDir() {
					basenameMatchPath = p
					basenameMatchInfo = fi
					if !hasSlash {
						return filepath.SkipAll
					}
				}
			}
			return nil
		})

		if foundExactSuffix != "" && foundExactInfo != nil {
			return foundExactSuffix, foundExactInfo
		}
		if !hasSlash && basenameMatchPath != "" && basenameMatchInfo != nil {
			return basenameMatchPath, basenameMatchInfo
		}
		if visited > maxFallbackWalkEntries {
			break
		}
	}

	return basenameMatchPath, basenameMatchInfo
}

func allowFallbackLookup(cleanedPath string) bool {
	if filepath.IsAbs(cleanedPath) || strings.HasPrefix(cleanedPath, "..") {
		return false
	}
	slash := strings.TrimPrefix(filepath.ToSlash(cleanedPath), "./")
	if strings.Contains(slash, "/") {
		return true
	}
	if strings.Contains(slash, ".") && !strings.HasPrefix(slash, ".") {
		return true
	}
	switch slash {
	case "Dockerfile", "Containerfile", "Makefile", "GNUmakefile", "Justfile", "Caddyfile", "Procfile", "Gemfile", "Rakefile", "Brewfile", "Vagrantfile", "LICENSE", "README", "CHANGELOG", "CODEOWNERS":
		return true
	default:
		return false
	}
}

func (g *Gateway) resolveLocalFileTarget(rawPath, rawDir, sessionID string, allowFallback bool) (string, os.FileInfo, bool, error) {
	cleanedPath, _ := cleanRequestedFilePath(rawPath)
	if cleanedPath == "" {
		return "", nil, false, os.ErrNotExist
	}
	dirs := g.candidateBaseDirs(rawDir, sessionID)

	checkOutside := func(target string) bool {
		for _, d := range dirs {
			if isPathWithinDir(d, target) {
				return false
			}
		}
		return true
	}

	if filepath.IsAbs(cleanedPath) {
		target := filepath.Clean(cleanedPath)
		if info, err := os.Stat(target); err == nil {
			return target, info, checkOutside(target), nil
		} else if !os.IsNotExist(err) {
			return target, nil, checkOutside(target), err
		}
		// If the absolute path pointed inside an older/cleaned worktree (.hoxi/worktrees/<id>/...)
		// whose worktree directory no longer exists on disk, try resolving the relative suffix
		// against the parent repo or active candidate dirs.
		slash := filepath.ToSlash(target)
		if idx := strings.Index(slash, "/.hoxi/worktrees/"); idx > 0 {
			rest := slash[idx+len("/.hoxi/worktrees/"):]
			if slashIdx := strings.IndexByte(rest, '/'); slashIdx > 0 {
				wtRoot := filepath.FromSlash(slash[:idx+len("/.hoxi/worktrees/")+slashIdx])
				if _, wtErr := os.Stat(wtRoot); os.IsNotExist(wtErr) {
					relSuffix := filepath.FromSlash(rest[slashIdx+1:])
					parentRepo := filepath.FromSlash(slash[:idx])
					altTarget := filepath.Join(parentRepo, relSuffix)
					if info, err := os.Stat(altTarget); err == nil {
						return altTarget, info, checkOutside(altTarget), nil
					}
					for _, d := range dirs {
						cand := filepath.Join(d, relSuffix)
						if info, err := os.Stat(cand); err == nil {
							return cand, info, checkOutside(cand), nil
						}
					}
				}
			}
		}
		return target, nil, true, os.ErrNotExist
	}

	for _, d := range dirs {
		cand := filepath.Join(d, cleanedPath)
		if info, err := os.Stat(cand); err == nil {
			return cand, info, checkOutside(cand), nil
		}
	}

	if allowFallback && allowFallbackLookup(cleanedPath) {
		if fbPath, fbInfo := findFallbackInDirs(dirs, cleanedPath); fbPath != "" && fbInfo != nil {
			return fbPath, fbInfo, checkOutside(fbPath), nil
		}
	}

	if len(dirs) > 0 {
		target := filepath.Join(dirs[0], cleanedPath)
		return target, nil, checkOutside(target), os.ErrNotExist
	}
	target := filepath.Clean(cleanedPath)
	return target, nil, true, os.ErrNotExist
}

func detectFileMimeType(targetPath string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(targetPath))
	switch ext {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".bmp":
		return "image/bmp"
	case ".avif":
		return "image/avif"
	case ".md", ".mdx", ".markdown":
		return "text/markdown"
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
}

func isBinaryImageMime(mimeType string) bool {
	m := strings.ToLower(strings.TrimSpace(mimeType))
	return strings.HasPrefix(m, "image/") && !strings.HasPrefix(m, "image/svg")
}

func (g *Gateway) handleFileContent(w http.ResponseWriter, r *http.Request) {
	remote := isRemoteRequest(r)
	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if r.Method != http.MethodGet || rawPath == "" {
		// Remote requests never fall through to the OpenCode proxy.
		if remote {
			writeJSONError(w, http.StatusForbidden, "route is not available for remote controllers")
			return
		}
		g.proxy.ServeHTTP(w, r)
		return
	}
	rawDir := strings.TrimSpace(r.URL.Query().Get("directory"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))

	cleanedPreview, _ := cleanRequestedFilePath(rawPath)
	if cleanedPreview != "" && isSensitiveCredentialPath(cleanedPreview) {
		writeJSONError(w, http.StatusForbidden, "access to sensitive credential file is forbidden")
		return
	}

	targetPath, info, outsideWorkspace, err := g.resolveLocalFileTarget(rawPath, rawDir, sessionID, true)
	if targetPath != "" && isSensitiveCredentialPath(targetPath) {
		writeJSONError(w, http.StatusForbidden, "access to sensitive credential file is forbidden")
		return
	}
	// outsideWorkspace is judged against request-supplied directories too, so
	// remote reads must also land inside a registered workspace or worktree.
	// Checked before existence so the tunnel cannot probe arbitrary paths.
	if remote && (outsideWorkspace || !g.tunnelFilePathAllowed(targetPath, sessionID)) {
		writeJSONError(w, http.StatusForbidden, "path is outside the workspace")
		return
	}
	if err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "file not found: "+rawPath)
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if info.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":            "path is a directory: " + rawPath,
			"isDirectory":      true,
			"resolvedPath":     targetPath,
			"outsideWorkspace": outsideWorkspace,
		})
		return
	}

	f, err := os.Open(targetPath)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxLocalFilePreviewBytes))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	mimeType := detectFileMimeType(targetPath, data)
	if isBinaryImageMime(mimeType) || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		writeJSON(w, http.StatusOK, map[string]any{
			"type":             "binary",
			"content":          base64.StdEncoding.EncodeToString(data),
			"encoding":         "base64",
			"mimeType":         mimeType,
			"resolvedPath":     targetPath,
			"outsideWorkspace": outsideWorkspace,
			"size":             info.Size(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"type":             "text",
		"content":          string(data),
		"mimeType":         mimeType,
		"resolvedPath":     targetPath,
		"outsideWorkspace": outsideWorkspace,
		"size":             info.Size(),
	})
}

func (g *Gateway) handleFileValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body struct {
		Directory string   `json:"directory"`
		SessionID string   `json:"sessionID"`
		Paths     []string `json:"paths"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	paths := body.Paths
	if len(paths) > maxValidateBatchPaths {
		paths = paths[:maxValidateBatchPaths]
	}

	existing := make([]string, 0, len(paths))
	resolved := make(map[string]string)

	for _, raw := range paths {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		targetPath, info, _, err := g.resolveLocalFileTarget(trimmed, body.Directory, body.SessionID, true)
		if err != nil || info == nil || info.IsDir() {
			continue
		}
		if isSensitiveCredentialPath(targetPath) {
			continue
		}
		existing = append(existing, raw)
		resolved[raw] = targetPath
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"existing": existing,
		"resolved": resolved,
	})
}

func (g *Gateway) handleFileList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	rawDir := strings.TrimSpace(r.URL.Query().Get("directory"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))

	dirs := g.candidateBaseDirs(rawDir, sessionID)
	baseDir := g.rootDir
	if len(dirs) > 0 {
		baseDir = dirs[0]
	}

	var targetDir string
	cleanedPath, _ := cleanRequestedFilePath(rawPath)
	if cleanedPath == "" || cleanedPath == "." {
		targetDir = baseDir
	} else if filepath.IsAbs(cleanedPath) {
		targetDir = filepath.Clean(cleanedPath)
	} else {
		targetDir = filepath.Join(baseDir, cleanedPath)
	}

	if isSensitiveCredentialPath(targetDir) {
		writeJSONError(w, http.StatusForbidden, "access to sensitive directory is forbidden")
		return
	}
	if isRemoteRequest(r) && !g.tunnelFilePathAllowed(targetDir, sessionID) {
		writeJSONError(w, http.StatusForbidden, "path is outside the workspace")
		return
	}

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "directory not found: "+targetDir)
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	type fileNode struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Absolute string `json:"absolute"`
		Type     string `json:"type"`
	}

	nodes := make([]fileNode, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" || name == ".DS_Store" {
			continue
		}
		abs := filepath.Join(targetDir, name)
		if isSensitiveCredentialPath(abs) {
			continue
		}
		nodeType := "file"
		if entry.IsDir() {
			nodeType = "directory"
		} else if entry.Type()&os.ModeSymlink != 0 {
			if st, statErr := os.Stat(abs); statErr == nil && st.IsDir() {
				nodeType = "directory"
			}
		}
		relPath := name
		if isPathWithinDir(baseDir, abs) {
			if rel, relErr := filepath.Rel(baseDir, abs); relErr == nil && rel != "." {
				relPath = filepath.ToSlash(rel)
			}
		} else {
			relPath = filepath.ToSlash(abs)
		}
		nodes = append(nodes, fileNode{
			Name:     name,
			Path:     relPath,
			Absolute: abs,
			Type:     nodeType,
		})
	}

	sort.Slice(nodes, func(i, j int) bool {
		if (nodes[i].Type == "directory") != (nodes[j].Type == "directory") {
			return nodes[i].Type == "directory"
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})

	writeJSON(w, http.StatusOK, nodes)
}

func (g *Gateway) handleFileOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body struct {
		Path      string `json:"path"`
		Directory string `json:"directory"`
		SessionID string `json:"sessionID"`
		Action    string `json:"action"` // "open" (default) or "reveal"
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rawPath := strings.TrimSpace(body.Path)
	if rawPath == "" {
		writeJSONError(w, http.StatusBadRequest, "path is required")
		return
	}

	targetPath, _, _, err := g.resolveLocalFileTarget(rawPath, body.Directory, body.SessionID, true)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "file not found: "+rawPath)
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if isSensitiveCredentialPath(targetPath) {
		writeJSONError(w, http.StatusForbidden, "access to sensitive credential file is forbidden")
		return
	}

	action := strings.ToLower(strings.TrimSpace(body.Action))
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := osopen.Open(ctx, targetPath, action == "reveal"); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to open path: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"resolvedPath": targetPath,
	})
}
