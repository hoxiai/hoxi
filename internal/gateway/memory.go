package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/worktree"
)

const maxMemoryDocumentBytes = 256 << 10

var gatewayMemoryMu sync.Mutex

func sanitizeMemorySessionID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 128 {
		return ""
	}
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return ""
	}
	return trimmed
}

func (g *Gateway) resolveProjectMemoryRoot(rawDir, sessionID string) (string, bool) {
	var candidates []string
	if sessionID != "" && g.worktrees != nil {
		if binding, known, err := g.worktrees.BindingForSession(sessionID); err == nil && known && binding.Directory != "" {
			candidates = []string{binding.Directory}
		}
	}
	if len(candidates) == 0 {
		if trimmed := strings.TrimSpace(rawDir); trimmed != "" {
			candidates = append(candidates, trimmed)
		}
		if g.rootDir != "" {
			candidates = append(candidates, g.rootDir)
		}
	}
	for _, cand := range candidates {
		cleaned := strings.TrimSpace(expandUserPath(cand))
		if cleaned == "" {
			continue
		}
		if abs, err := filepath.Abs(cleaned); err == nil {
			cleaned = filepath.Clean(abs)
		} else {
			cleaned = filepath.Clean(cleaned)
		}
		if root := hoxipaths.ProjectRootForWorktree(cleaned); root != "" {
			cleaned = root
		}
		if st, err := os.Stat(cleaned); err == nil && st.IsDir() {
			return cleaned, true
		}
	}
	return "", false
}

func (g *Gateway) handleMemory(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "route is not available for remote controllers")
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "PUT or POST required")
		return
	}
	var body struct {
		Scope     string `json:"scope"`
		SessionID string `json:"sessionID"`
		Directory string `json:"directory"`
		Content   string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxMemoryDocumentBytes+4096)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Content) > maxMemoryDocumentBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "memory document exceeds maximum size")
		return
	}
	scope := strings.ToLower(strings.TrimSpace(body.Scope))
	if scope == "" {
		scope = "project"
	}
	if scope != "project" && scope != "session" {
		writeJSONError(w, http.StatusBadRequest, "scope must be 'project' or 'session'")
		return
	}
	sid := sanitizeMemorySessionID(body.SessionID)
	if scope == "session" && sid == "" {
		writeJSONError(w, http.StatusBadRequest, "valid sessionID is required for session memory")
		return
	}
	rawDir := strings.TrimSpace(body.Directory)
	if rawDir == "" {
		rawDir = strings.TrimSpace(r.URL.Query().Get("directory"))
	}
	projectRoot, ok := g.resolveProjectMemoryRoot(rawDir, sid)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "workspace directory not found")
		return
	}

	var targetFile string
	if scope == "project" {
		targetFile = hoxipaths.ProjectMemoryFile(projectRoot)
	} else {
		targetFile = hoxipaths.ProjectSessionMemoryFile(projectRoot, sid)
	}
	if targetFile == "" || !isPathWithinDir(projectRoot, targetFile) {
		writeJSONError(w, http.StatusBadRequest, "could not resolve memory file path")
		return
	}

	gatewayMemoryMu.Lock()
	defer gatewayMemoryMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(targetFile), 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	worktree.EnsureGitExclude(projectRoot, ".hoxi/")
	normalized := strings.TrimRight(strings.ReplaceAll(body.Content, "\r\n", "\n"), " \t\n")
	if normalized != "" {
		normalized += "\n"
	}
	if err := hoxipaths.WriteFileAtomic(targetFile, []byte(normalized), 0o600); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"scope":   scope,
		"path":    targetFile,
		"content": normalized,
	})
}

func extractPromotableRulesFromSessionMemory(md string) []string {
	var rules []string
	seen := make(map[string]bool)
	addRule := func(raw string) {
		clean := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "-"))
		clean = strings.TrimSpace(strings.TrimPrefix(clean, "User Constraint:"))
		if clean == "" || seen[clean] {
			return
		}
		// Skip generic boilerplate lines
		lower := strings.ToLower(clean)
		if strings.HasPrefix(lower, "initial request:") ||
			strings.HasPrefix(lower, "latest request:") ||
			strings.HasPrefix(lower, "active engineering session") ||
			strings.HasPrefix(lower, "carried in quoted") ||
			strings.HasPrefix(lower, "no explicit file") ||
			strings.HasPrefix(lower, "no unresolved tool") ||
			strings.HasPrefix(lower, "executed ") ||
			strings.HasPrefix(lower, "all tracked tasks") ||
			strings.HasPrefix(lower, "ready for next") ||
			lower == "none" {
			return
		}
		seen[clean] = true
		rules = append(rules, clean)
	}

	inSection := ""
	for _, rawLine := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "## ") {
			inSection = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		if strings.HasPrefix(line, "- User Constraint:") {
			addRule(line)
			continue
		}
		if strings.Contains(inSection, "constraint") ||
			strings.Contains(inSection, "primary request") ||
			strings.Contains(inSection, "problem solving") ||
			strings.Contains(inSection, "errors and fixes") {
			if strings.HasPrefix(line, "- Verification:") || strings.HasPrefix(line, "- Latest Summary:") {
				addRule(strings.TrimPrefix(line, "- Latest Summary:"))
				continue
			}
			addRule(line)
		}
	}
	return rules
}

func (g *Gateway) handleMemoryPromote(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "route is not available for remote controllers")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body struct {
		SessionID string `json:"sessionID"`
		Directory string `json:"directory"`
		Content   string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxMemoryDocumentBytes+4096)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sid := sanitizeMemorySessionID(body.SessionID)
	rawDir := strings.TrimSpace(body.Directory)
	if rawDir == "" {
		rawDir = strings.TrimSpace(r.URL.Query().Get("directory"))
	}
	projectRoot, ok := g.resolveProjectMemoryRoot(rawDir, sid)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "workspace directory not found")
		return
	}

	sourceMD := strings.TrimSpace(body.Content)
	if sourceMD == "" && sid != "" {
		for _, cand := range []string{
			hoxipaths.ProjectSessionMemoryFile(projectRoot, sid),
			hoxipaths.LegacyProjectSessionMemoryFile(projectRoot, sid),
		} {
			if raw, err := os.ReadFile(cand); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
				sourceMD = string(raw)
				break
			}
		}
	}
	rules := extractPromotableRulesFromSessionMemory(sourceMD)
	if len(rules) == 0 && sourceMD != "" {
		for _, line := range strings.Split(sourceMD, "\n") {
			trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "Session:") && !strings.HasPrefix(trimmed, "Updated:") {
				rules = append(rules, trimmed)
				if len(rules) >= 6 {
					break
				}
			}
		}
	}
	if len(rules) == 0 {
		writeJSONError(w, http.StatusBadRequest, "no promotable rules or notes found in session memory")
		return
	}

	gatewayMemoryMu.Lock()
	defer gatewayMemoryMu.Unlock()

	projFile := hoxipaths.ProjectMemoryFile(projectRoot)
	if projFile == "" || !isPathWithinDir(projectRoot, projFile) {
		writeJSONError(w, http.StatusBadRequest, "could not resolve project memory file path")
		return
	}
	existingBytes, err := os.ReadFile(projFile)
	if err != nil {
		existingBytes, _ = os.ReadFile(hoxipaths.LegacyProjectMemoryFile(projectRoot))
	}
	existing := strings.TrimRight(strings.ReplaceAll(string(existingBytes), "\r\n", "\n"), " \t\n")
	if existing == "" {
		existing = "# Project Memory"
	}

	added := 0
	var b strings.Builder
	b.WriteString(existing)
	b.WriteByte('\n')
	for _, rule := range rules {
		if strings.Contains(existing, rule) {
			continue
		}
		b.WriteString("- ")
		b.WriteString(rule)
		b.WriteByte('\n')
		added++
	}
	updated := b.String()

	if err := os.MkdirAll(filepath.Dir(projFile), 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	worktree.EnsureGitExclude(projectRoot, ".hoxi/")
	if err := hoxipaths.WriteFileAtomic(projFile, []byte(updated), 0o600); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"path":       projFile,
		"content":    updated,
		"addedCount": added,
	})
}
