package gateway

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type fileSearchResult struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Type string `json:"type"`
}

const (
	maxFileSearchResults = 80
	maxFileSearchEntries = 20000
)

func skipFileSearchDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".git", ".hg", ".svn", ".hoxi", ".claude",
		"node_modules", ".pnpm-store", "dist", "build", "target", "vendor",
		".next", ".nuxt", ".turbo", ".cache", "coverage", "pods", "deriveddata",
		".dart_tool", ".pub-cache", "go-build", "go-build-cache", ".gocache", ".gomodcache",
		"__pycache__", ".pytest_cache", ".venv", "venv", ".idea", ".gradle", ".m2":
		return true
	}
	return false
}

func fileSearchScore(relPath, name, query string) int {
	rel := strings.ToLower(filepath.ToSlash(relPath))
	base := strings.ToLower(name)
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 0
	}
	switch {
	case base == q:
		return 0
	case strings.HasPrefix(base, q):
		return 1
	case strings.Contains(base, q):
		return 2
	case strings.HasPrefix(rel, q):
		return 3
	case strings.Contains(rel, q):
		return 4
	default:
		return -1
	}
}

func (g *Gateway) handleFileSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusOK, []fileSearchResult{})
		return
	}
	rawDir := strings.TrimSpace(r.URL.Query().Get("directory"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))

	limit := 60
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		if n, err := strconv.Atoi(rawLimit); err == nil && n > 0 && n <= maxFileSearchResults {
			limit = n
		}
	}

	dirs := g.candidateBaseDirs(rawDir, sessionID)
	baseDir := g.rootDir
	if len(dirs) > 0 {
		baseDir = dirs[0]
	}
	if strings.TrimSpace(baseDir) == "" {
		writeJSON(w, http.StatusOK, []fileSearchResult{})
		return
	}
	if canonical, ok := canonicalDirectory(baseDir); ok {
		baseDir = canonical
	}

	results := make([]fileSearchResult, 0, limit)
	scanned := 0
	walkErr := filepath.WalkDir(baseDir, func(target string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if target == baseDir {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() && skipFileSearchDir(name) {
			return filepath.SkipDir
		}
		scanned++
		if scanned > maxFileSearchEntries {
			return fs.SkipAll
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || isSensitiveCredentialPath(target) {
			return nil
		}
		rel, relErr := filepath.Rel(baseDir, target)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		score := fileSearchScore(rel, name, query)
		if score < 0 {
			return nil
		}
		results = append(results, fileSearchResult{
			Path: rel,
			Name: name,
			Type: "file",
		})
		if len(results) >= maxFileSearchResults {
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		writeJSONError(w, http.StatusInternalServerError, walkErr.Error())
		return
	}

	sort.Slice(results, func(i, j int) bool {
		si := fileSearchScore(results[i].Path, results[i].Name, query)
		sj := fileSearchScore(results[j].Path, results[j].Name, query)
		if si != sj {
			return si < sj
		}
		if len(results[i].Path) != len(results[j].Path) {
			return len(results[i].Path) < len(results[j].Path)
		}
		return strings.ToLower(results[i].Path) < strings.ToLower(results[j].Path)
	})
	if len(results) > limit {
		results = results[:limit]
	}
	writeJSON(w, http.StatusOK, results)
}
