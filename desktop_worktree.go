package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const gitCommandTimeout = 20 * time.Second

type worktreeMeta struct {
	SessionID  string `json:"sessionID"`
	Isolated   bool   `json:"isolated"`
	Directory  string `json:"directory"`
	Branch     string `json:"branch,omitempty"`
	BaseCommit string `json:"baseCommit,omitempty"`
	CreatedAt  string `json:"createdAt"`
}

type worktreeStatus struct {
	SessionID        string `json:"sessionID"`
	Isolated         bool   `json:"isolated"`
	Directory        string `json:"directory"`
	Branch           string `json:"branch,omitempty"`
	BaseCommit       string `json:"baseCommit,omitempty"`
	Dirty            bool   `json:"dirty"`
	ChangedFiles     int    `json:"changedFiles"`
	UncommittedLines int    `json:"uncommittedLines"`
}

type worktreeManager struct {
	mu       sync.RWMutex
	rootDir  string
	sessions map[string]worktreeMeta
}

func newWorktreeManager(rootDir string) *worktreeManager {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		absRoot = rootDir
	}
	return &worktreeManager{
		rootDir:  absRoot,
		sessions: make(map[string]worktreeMeta),
	}
}

func (m *worktreeManager) Create(sessionID string) (worktreeMeta, error) {
	sessionID = sanitizeSessionID(sessionID)
	if sessionID == "" {
		return worktreeMeta{}, errors.New("sessionID is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.sessions[sessionID]; ok {
		if _, err := os.Stat(existing.Directory); err == nil {
			return existing, nil
		}
	}

	topLevel, err := runGit(m.rootDir, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(topLevel) == "" {
		meta := worktreeMeta{
			SessionID: sessionID,
			Isolated:  false,
			Directory: m.rootDir,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		m.sessions[sessionID] = meta
		return meta, nil
	}

	repoRoot := filepath.Clean(strings.TrimSpace(topLevel))
	baseCommit, err := runGit(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		meta := worktreeMeta{
			SessionID: sessionID,
			Isolated:  false,
			Directory: repoRoot,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		m.sessions[sessionID] = meta
		return meta, nil
	}

	worktreesRoot := filepath.Join(repoRoot, ".hoxi", "worktrees")
	if err := os.MkdirAll(worktreesRoot, 0o755); err != nil {
		return worktreeMeta{}, fmt.Errorf("create worktrees dir: %w", err)
	}
	ensureGitExclude(repoRoot, ".hoxi/")

	worktreeDir := filepath.Join(worktreesRoot, sessionID)
	branch := "hoxi/" + sessionID

	if _, err := os.Stat(worktreeDir); os.IsNotExist(err) {
		_, err = runGit(repoRoot, "-c", "checkout.workers=0", "worktree", "add", "-b", branch, worktreeDir, strings.TrimSpace(baseCommit))
		if err != nil {
			// Branch may already exist from previous run; try without -b
			_, err = runGit(repoRoot, "-c", "checkout.workers=0", "worktree", "add", worktreeDir, branch)
			if err != nil {
				return worktreeMeta{}, fmt.Errorf("git worktree add: %w", err)
			}
		}
	}

	// Symlink .opencode into the worktree so plugins and memory are shared
	srcOpenCode := filepath.Join(repoRoot, ".opencode")
	dstOpenCode := filepath.Join(worktreeDir, ".opencode")
	if _, err := os.Stat(srcOpenCode); err == nil {
		if _, err := os.Lstat(dstOpenCode); os.IsNotExist(err) {
			_ = os.Symlink(srcOpenCode, dstOpenCode)
		}
	}

	meta := worktreeMeta{
		SessionID:  sessionID,
		Isolated:   true,
		Directory:  worktreeDir,
		Branch:     branch,
		BaseCommit: strings.TrimSpace(baseCommit),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	m.sessions[sessionID] = meta
	return meta, nil
}

func (m *worktreeManager) Status(sessionID string) (worktreeStatus, error) {
	sessionID = sanitizeSessionID(sessionID)
	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			meta = worktreeMeta{
				SessionID: sessionID,
				Isolated:  true,
				Directory: candidate,
				Branch:    "hoxi/" + sessionID,
			}
		} else {
			return worktreeStatus{
				SessionID: sessionID,
				Isolated:  false,
				Directory: m.rootDir,
			}, nil
		}
	}

	porcelain, _ := runGit(meta.Directory, "status", "--porcelain")
	lines := 0
	for _, l := range strings.Split(strings.TrimSpace(porcelain), "\n") {
		if strings.TrimSpace(l) != "" {
			lines++
		}
	}

	return worktreeStatus{
		SessionID:        meta.SessionID,
		Isolated:         meta.Isolated,
		Directory:        meta.Directory,
		Branch:           meta.Branch,
		BaseCommit:       meta.BaseCommit,
		Dirty:            lines > 0,
		ChangedFiles:     lines,
		UncommittedLines: lines,
	}, nil
}

func (m *worktreeManager) Apply(sessionID, mode string) error {
	sessionID = sanitizeSessionID(sessionID)
	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok || !meta.Isolated {
		return errors.New("session does not have an isolated worktree")
	}

	// Commit any uncommitted changes in the worktree before generating patch or merging
	statusOut, err := runGit(meta.Directory, "status", "--porcelain")
	if err == nil && strings.TrimSpace(statusOut) != "" {
		_, _ = runGit(meta.Directory, "add", "-A")
		_, _ = runGit(meta.Directory, "commit", "-m", "chore(hoxi): snapshot before apply ("+sessionID+")")
	}

	if mode == "merge" {
		_, err := runGit(m.rootDir, "merge", "--no-ff", "-m", "Merge hoxi session "+sessionID, meta.Branch)
		return err
	}

	// Default mode: "patch" — applies changes cleanly onto main workspace without polluting commit history
	base := meta.BaseCommit
	if base == "" {
		base, _ = runGit(m.rootDir, "merge-base", "HEAD", meta.Branch)
	}
	diffPatch, err := runGitRaw(meta.Directory, "diff", "--binary", strings.TrimSpace(base)+"..HEAD")
	if err != nil {
		return fmt.Errorf("generate worktree patch: %w", err)
	}
	if strings.TrimSpace(diffPatch) == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", m.rootDir, "apply", "--3way", "-")
	cmd.Stdin = strings.NewReader(diffPatch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply --3way failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *worktreeManager) Remove(sessionID string, force bool) error {
	sessionID = sanitizeSessionID(sessionID)
	m.mu.Lock()
	meta, ok := m.sessions[sessionID]
	if !ok {
		meta = worktreeMeta{
			SessionID: sessionID,
			Isolated:  true,
			Directory: filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID),
			Branch:    "hoxi/" + sessionID,
		}
	}
	defer m.mu.Unlock()

	if !meta.Isolated {
		delete(m.sessions, sessionID)
		return nil
	}

	if !force {
		porcelain, err := runGit(meta.Directory, "status", "--porcelain")
		if err == nil && strings.TrimSpace(porcelain) != "" {
			return errors.New("worktree has uncommitted changes; pass force=true to discard")
		}
	}

	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, meta.Directory)
	if _, err := runGit(m.rootDir, args...); err != nil {
		_, _ = runGit(m.rootDir, "worktree", "prune")
	}
	if meta.Branch != "" {
		_, _ = runGit(m.rootDir, "branch", "-D", meta.Branch)
	}
	delete(m.sessions, sessionID)
	return nil
}

func (m *worktreeManager) RevertFile(sessionID, relPath string) error {
	sessionID = sanitizeSessionID(sessionID)
	cleaned := filepath.Clean(strings.TrimPrefix(strings.TrimSpace(relPath), "./"))
	if cleaned == "" || cleaned == "." || strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return errors.New("invalid relative file path")
	}

	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	workDir := m.rootDir
	base := "HEAD"
	if ok && meta.Isolated && meta.Directory != "" {
		workDir = meta.Directory
		if meta.BaseCommit != "" {
			base = meta.BaseCommit
		}
	} else {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			workDir = candidate
		}
	}

	// Try restoring from base commit or HEAD; if untracked in base, remove the newly added file
	if _, err := runGit(workDir, "checkout", base, "--", cleaned); err != nil {
		if _, headErr := runGit(workDir, "checkout", "HEAD", "--", cleaned); headErr != nil {
			_ = os.Remove(filepath.Join(workDir, cleaned))
		}
	}
	return nil
}

func (m *worktreeManager) CommitWorktree(sessionID, message string) (string, error) {
	sessionID = sanitizeSessionID(sessionID)
	msg := strings.TrimSpace(message)
	if msg == "" {
		return "", errors.New("commit message is required")
	}

	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	workDir := m.rootDir
	if ok && meta.Isolated && meta.Directory != "" {
		workDir = meta.Directory
	} else if sessionID != "" {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			workDir = candidate
		}
	}

	if _, err := runGit(workDir, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := runGit(workDir, "commit", "-m", msg); err != nil {
		return "", err
	}
	hash, _ := runGit(workDir, "rev-parse", "--short", "HEAD")
	return hash, nil
}

func (m *worktreeManager) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/worktree/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			SessionID string `json:"sessionID"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		meta, err := m.Create(body.SessionID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, meta)
	})

	mux.HandleFunc("/api/hoxi/worktree/status", func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("sessionID")
		st, err := m.Status(sessionID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("/api/hoxi/worktree/apply", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			SessionID string `json:"sessionID"`
			Mode      string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := m.Apply(body.SessionID, body.Mode); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/hoxi/worktree/revert-file", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			SessionID string `json:"sessionID"`
			File      string `json:"file"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := m.RevertFile(body.SessionID, body.File); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/hoxi/worktree/commit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			SessionID string `json:"sessionID"`
			Message   string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		hash, err := m.CommitWorktree(body.SessionID, body.Message)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "commit": hash})
	})

	mux.HandleFunc("/api/hoxi/worktree/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		sessionID := strings.TrimPrefix(r.URL.Path, "/api/hoxi/worktree/")
		force := r.URL.Query().Get("force") == "true"
		if err := m.Remove(sessionID, force); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}

func sanitizeSessionID(id string) string {
	id = strings.TrimSpace(id)
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func ensureGitExclude(repoRoot, pattern string) {
	excludeFile := filepath.Join(repoRoot, ".git", "info", "exclude")
	data, err := os.ReadFile(excludeFile)
	if err == nil && !strings.Contains(string(data), pattern) {
		_ = os.WriteFile(excludeFile, append(data, []byte("\n"+pattern+"\n")...), 0o644)
	}
}

func runGit(workDir string, args ...string) (string, error) {
	out, err := runGitRaw(workDir, args...)
	return strings.TrimSpace(out), err
}

func runGitRaw(workDir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmdArgs := append([]string{"-C", workDir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return string(output), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
