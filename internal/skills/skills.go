package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
)

const (
	maxUpstreamSkillBytes = 8 << 20
	builtinLocationToken  = "<built-in>"
	defaultCacheHint      = "Refreshing synchronizes Hoxi with the engine's current catalog. Newly added files on disk may require reloading the workspace."
)

var (
	ErrBadRequest  = errors.New("bad request")
	ErrForbidden   = errors.New("forbidden")
	ErrNotFound    = errors.New("not found")
	ErrUnsupported = errors.New("unsupported runtime")
)

type OpenCodeSkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Location    string `json:"location"`
	Content     string `json:"content"`
}

type SkillDescriptor struct {
	Key             string `json:"key"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Runtime         string `json:"runtime"`
	Source          string `json:"source"` // "builtin" | "project" | "user" | "external"
	SourceLabel     string `json:"sourceLabel"`
	CanOpenLocation bool   `json:"canOpenLocation"`
	ContentDigest   string `json:"contentDigest,omitempty"`
}

type SkillDetail struct {
	SkillDescriptor
	Content  string `json:"content"`
	Location string `json:"location,omitempty"`
}

type SkillSelection struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type ListResponse struct {
	Runtime           string            `json:"runtime"`
	Supported         bool              `json:"supported"`
	Directory         string            `json:"directory"`
	SessionID         string            `json:"sessionID,omitempty"`
	Skills            []SkillDescriptor `json:"skills"`
	UnsupportedReason string            `json:"unsupportedReason,omitempty"`
	CacheHint         string            `json:"cacheHint,omitempty"`
}

type RequestContext struct {
	SessionID    string
	Runtime      string
	Directory    string
	ProjectRoot  string
	AllowedRoots []string
}

type ContextResolver func(ctx context.Context, directory, sessionID, requestedRuntime string) (RequestContext, error)

type cachedSkillEntry struct {
	detail       SkillDetail
	resolvedPath string
}

type cachedCatalog struct {
	updatedAt time.Time
	entries   []cachedSkillEntry
}

type Service struct {
	openCode *ocapi.Client
	resolver ContextResolver

	mu    sync.RWMutex
	cache map[string]cachedCatalog
}

// upstreamSkillTimeout bounds one engine /skill listing.
const upstreamSkillTimeout = 15 * time.Second

func NewService(openCodeURL, password string, resolver ContextResolver) *Service {
	return &Service{
		openCode: ocapi.New(openCodeURL, password),
		resolver: resolver,
		cache:    make(map[string]cachedCatalog),
	}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/skills/list", s.handleList)
	mux.HandleFunc("/api/hoxi/skills/detail", s.handleDetail)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	q := r.URL.Query()
	rawDir := strings.TrimSpace(q.Get("directory"))
	sessionID := strings.TrimSpace(q.Get("sessionID"))
	reqRuntime := strings.TrimSpace(q.Get("runtime"))

	rc, err := s.resolveContext(r.Context(), rawDir, sessionID, reqRuntime)
	if err != nil {
		writeErrorResponse(w, err)
		return
	}

	entries, err := s.loadOpenCodeCatalog(r.Context(), rc, true)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "failed to load skills from OpenCode: "+err.Error())
		return
	}

	descriptors := make([]SkillDescriptor, 0, len(entries))
	for _, e := range entries {
		descriptors = append(descriptors, e.detail.SkillDescriptor)
	}

	writeJSON(w, http.StatusOK, ListResponse{
		Runtime:   rc.Runtime,
		Supported: true,
		Directory: rc.Directory,
		SessionID: rc.SessionID,
		Skills:    descriptors,
		CacheHint: defaultCacheHint,
	})
}

func (s *Service) handleDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	q := r.URL.Query()
	key := strings.TrimSpace(q.Get("key"))
	if key == "" || !strings.HasPrefix(key, "skl_") {
		writeJSONError(w, http.StatusBadRequest, "valid skill key is required")
		return
	}
	rawDir := strings.TrimSpace(q.Get("directory"))
	sessionID := strings.TrimSpace(q.Get("sessionID"))
	reqRuntime := strings.TrimSpace(q.Get("runtime"))

	rc, err := s.resolveContext(r.Context(), rawDir, sessionID, reqRuntime)
	if err != nil {
		writeErrorResponse(w, err)
		return
	}
	entries, err := s.loadOpenCodeCatalog(r.Context(), rc, false)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "failed to load skill detail: "+err.Error())
		return
	}
	for _, entry := range entries {
		if entry.detail.Key == key {
			writeJSON(w, http.StatusOK, entry.detail)
			return
		}
	}

	// Retry once against upstream in case the catalog has not been refreshed yet.
	entries, err = s.loadOpenCodeCatalog(r.Context(), rc, true)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "failed to load skill detail: "+err.Error())
		return
	}
	for _, entry := range entries {
		if entry.detail.Key == key {
			writeJSON(w, http.StatusOK, entry.detail)
			return
		}
	}

	writeJSONError(w, http.StatusNotFound, "skill not found in active directory")
}

// ResolveSelection checks that sel.Key and sel.Name match an active skill in the
// session's effective directory and returns its content for inline delivery.
func (s *Service) ResolveSelection(ctx context.Context, directory, sessionID string, sel SkillSelection) (SkillDetail, error) {
	key := strings.TrimSpace(sel.Key)
	name := strings.TrimSpace(sel.Name)
	if key == "" || name == "" || !strings.HasPrefix(key, "skl_") {
		return SkillDetail{}, fmt.Errorf("%w: invalid skill selection", ErrBadRequest)
	}
	rc, err := s.resolveContext(ctx, directory, sessionID, "")
	if err != nil {
		return SkillDetail{}, err
	}
	entries, err := s.loadOpenCodeCatalog(ctx, rc, true)
	if err != nil {
		return SkillDetail{}, err
	}
	for _, entry := range entries {
		if entry.detail.Name == name && entry.detail.Key == key {
			return entry.detail, nil
		}
	}
	for _, entry := range entries {
		if entry.detail.Name == name {
			return SkillDetail{}, fmt.Errorf("%w: skill %q source changed in the active directory; please re-select the skill", ErrForbidden, name)
		}
	}
	return SkillDetail{}, fmt.Errorf("%w: skill %q is not available in the active directory", ErrNotFound, name)
}

// VerifySelection validates a skill selection without loading its content.
func (s *Service) VerifySelection(ctx context.Context, directory, sessionID string, sel SkillSelection) (SkillDescriptor, error) {
	detail, err := s.ResolveSelection(ctx, directory, sessionID, sel)
	if err != nil {
		return SkillDescriptor{}, err
	}
	return detail.SkillDescriptor, nil
}

func (s *Service) resolveContext(ctx context.Context, directory, sessionID, requestedRuntime string) (RequestContext, error) {
	if s.resolver == nil {
		return RequestContext{}, fmt.Errorf("%w: skill context resolver is not configured", ErrBadRequest)
	}
	rc, err := s.resolver(ctx, directory, sessionID, requestedRuntime)
	if err != nil {
		return RequestContext{}, err
	}
	rc.Directory = canonicalizeDir(rc.Directory)
	rc.ProjectRoot = canonicalizeDir(rc.ProjectRoot)
	if rc.Directory == "" {
		return RequestContext{}, fmt.Errorf("%w: valid workspace directory is required", ErrBadRequest)
	}
	if rc.Runtime == "" {
		rc.Runtime = "opencode"
	}
	return rc, nil
}

func catalogCacheKey(rc RequestContext) string {
	return rc.Runtime + "\x00" + rc.Directory + "\x00" + rc.SessionID
}

func (s *Service) loadOpenCodeCatalog(ctx context.Context, rc RequestContext, forceRefresh bool) ([]cachedSkillEntry, error) {
	cKey := catalogCacheKey(rc)
	if !forceRefresh {
		s.mu.RLock()
		cached, ok := s.cache[cKey]
		s.mu.RUnlock()
		if ok && time.Since(cached.updatedAt) < 30*time.Second {
			return cached.entries, nil
		}
	}

	rawItems, err := s.fetchOpenCodeSkills(ctx, rc.Directory)
	if err != nil {
		return nil, err
	}

	entries := make([]cachedSkillEntry, 0, len(rawItems))
	seenNames := make(map[string]bool, len(rawItems))
	for _, item := range rawItems {
		name := strings.TrimSpace(item.Name)
		if name == "" || seenNames[name] {
			continue
		}
		seenNames[name] = true
		entries = append(entries, buildSkillEntry(rc, item))
	}

	s.mu.Lock()
	s.cache[cKey] = cachedCatalog{
		updatedAt: time.Now(),
		entries:   entries,
	}
	s.mu.Unlock()
	return entries, nil
}

func (s *Service) fetchOpenCodeSkills(ctx context.Context, directory string) ([]OpenCodeSkillInfo, error) {
	if !s.openCode.Available() {
		return nil, errors.New("OpenCode endpoint is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, upstreamSkillTimeout)
	defer cancel()
	status, body, err := s.openCode.Raw(ctx, http.MethodGet, "/skill", url.Values{"directory": {directory}}, nil)
	if err != nil {
		return nil, err
	}
	if len(body) > maxUpstreamSkillBytes {
		return nil, fmt.Errorf("OpenCode skill catalog exceeds %d bytes", maxUpstreamSkillBytes)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}

	var items []OpenCodeSkillInfo
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func buildSkillEntry(rc RequestContext, item OpenCodeSkillInfo) cachedSkillEntry {
	name := strings.TrimSpace(item.Name)
	desc := strings.TrimSpace(item.Description)
	rawLoc := strings.TrimSpace(item.Location)

	source, sourceLabel, resolvedPath, canOpen := classifySkillLocation(rc, rawLoc)
	locIdentity := rawLoc
	if resolvedPath != "" {
		locIdentity = resolvedPath
	}

	keySum := sha256.Sum256([]byte(rc.Runtime + "\x00" + rc.Directory + "\x00" + name + "\x00" + locIdentity))
	key := "skl_" + hex.EncodeToString(keySum[:10])

	var digest string
	if item.Content != "" {
		cSum := sha256.Sum256([]byte(item.Content))
		digest = "sha256:" + hex.EncodeToString(cSum[:8])
	}

	descriptor := SkillDescriptor{
		Key:             key,
		Name:            name,
		Description:     desc,
		Runtime:         rc.Runtime,
		Source:          source,
		SourceLabel:     sourceLabel,
		CanOpenLocation: canOpen,
		ContentDigest:   digest,
	}

	detailLoc := ""
	if source == "builtin" {
		detailLoc = builtinLocationToken
	} else if canOpen && resolvedPath != "" {
		detailLoc = resolvedPath
	}

	return cachedSkillEntry{
		detail: SkillDetail{
			SkillDescriptor: descriptor,
			Content:         item.Content,
			Location:        detailLoc,
		},
		resolvedPath: resolvedPath,
	}
}

func classifySkillLocation(rc RequestContext, rawLocation string) (source, sourceLabel, resolvedPath string, canOpen bool) {
	if rawLocation == "" || rawLocation == builtinLocationToken || strings.HasPrefix(rawLocation, "<") {
		return "builtin", builtinLocationToken, "", false
	}

	cleaned := filepath.Clean(rawLocation)
	if !filepath.IsAbs(cleaned) {
		cleaned = filepath.Join(rc.Directory, cleaned)
	}
	resolved := canonicalizeFilePath(cleaned)
	if resolved == "" {
		resolved = cleaned
	}

	fileExists := false
	if fi, err := os.Stat(resolved); err == nil && !fi.IsDir() {
		fileExists = true
	}

	// 1. Project-scoped skill (inside effective session directory or project root)
	if isWithinDir(rc.Directory, resolved) {
		rel, err := filepath.Rel(rc.Directory, resolved)
		if err != nil || rel == "." {
			rel = filepath.Base(resolved)
		}
		return "project", filepath.ToSlash(rel), resolved, fileExists
	}
	if rc.ProjectRoot != "" && isWithinDir(rc.ProjectRoot, resolved) {
		rel, err := filepath.Rel(rc.ProjectRoot, resolved)
		if err != nil || rel == "." {
			rel = filepath.Base(resolved)
		}
		return "project", filepath.ToSlash(rel), resolved, fileExists
	}

	// 2. User-level skill (inside home config roots or XDG config roots)
	for _, root := range userSkillRoots() {
		if isWithinDir(root.dir, resolved) {
			rel, err := filepath.Rel(root.dir, resolved)
			label := root.label
			if err == nil && rel != "." {
				label = root.label + "/" + filepath.ToSlash(rel)
			}
			return "user", label, resolved, fileExists
		}
	}

	// 3. Additional allowed workspace roots
	for _, allowed := range rc.AllowedRoots {
		if isWithinDir(allowed, resolved) {
			rel, err := filepath.Rel(allowed, resolved)
			if err == nil && rel != "." {
				return "external", filepath.ToSlash(rel), resolved, fileExists
			}
			return "external", filepath.Base(resolved), resolved, fileExists
		}
	}

	return "external", filepath.Base(resolved), resolved, false
}

type labeledRoot struct {
	dir   string
	label string
}

func userSkillRoots() []labeledRoot {
	var roots []labeledRoot
	seen := make(map[string]bool)
	add := func(dir, label string) {
		c := canonicalizeDir(dir)
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
		roots = append(roots, labeledRoot{dir: c, label: label})
	}

	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		add(filepath.Join(xdg, "opencode"), "$XDG_CONFIG_HOME/opencode")
		add(filepath.Join(xdg, "agents"), "$XDG_CONFIG_HOME/agents")
		add(filepath.Join(xdg, "claude"), "$XDG_CONFIG_HOME/claude")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, ".config", "opencode"), "~/.config/opencode")
		add(filepath.Join(home, ".opencode"), "~/.opencode")
		add(filepath.Join(home, ".claude"), "~/.claude")
		add(filepath.Join(home, ".agents"), "~/.agents")
		if dir := DefaultUserDir(); dir != "" {
			add(dir, "~/.hoxi/skills")
		}
		add(filepath.Join(home, ".gemini"), "~/.gemini")
	}
	return roots
}

func canonicalizeDir(dir string) string {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(trimmed); err == nil {
		if abs, err := filepath.Abs(resolved); err == nil {
			return filepath.Clean(abs)
		}
		return filepath.Clean(resolved)
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(trimmed)
}

func canonicalizeFilePath(p string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(trimmed); err == nil {
		if abs, err := filepath.Abs(resolved); err == nil {
			return filepath.Clean(abs)
		}
		return filepath.Clean(resolved)
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(trimmed)
}

func isWithinDir(baseDir, targetPath string) bool {
	base := canonicalizeDir(baseDir)
	target := canonicalizeFilePath(targetPath)
	if base == "" || target == "" {
		return false
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func writeErrorResponse(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		writeJSONError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrNotFound):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrUnsupported):
		writeJSONError(w, http.StatusConflict, err.Error())
	default:
		writeJSONError(w, http.StatusBadRequest, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{"error": msg})
}
