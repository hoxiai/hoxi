package worktree

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (m *worktreeManager) RegisterRoutes(mux *http.ServeMux) {
	m.registerRoutes(mux)
}

func RunGit(workDir string, args ...string) (string, error) {
	return runGit(workDir, args...)
}

func RunGitRaw(workDir string, args ...string) (string, error) {
	return runGitRaw(workDir, args...)
}

func ParseBranchFromPorcelainHeader(headerLine string) string {
	return parseBranchFromPorcelainHeader(headerLine)
}

func (m *worktreeManager) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/worktree/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			SessionID string `json:"sessionID"`
			Directory string `json:"directory"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.Directory != "" {
			m.SetRootDir(body.Directory)
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
		if dir := r.URL.Query().Get("directory"); dir != "" && (sessionID == "" || sessionID == "__root__") {
			m.SetRootDir(dir)
		}
		st, err := m.Status(sessionID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("/api/hoxi/worktree/diff", func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("sessionID")
		if dir := r.URL.Query().Get("directory"); dir != "" && (sessionID == "" || sessionID == "__root__") {
			m.SetRootDir(dir)
		}
		diffs, err := m.Diff(sessionID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, diffs)
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

	mux.HandleFunc("/api/hoxi/worktree/branches", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if dir := r.URL.Query().Get("directory"); dir != "" {
			m.SetRootDir(dir)
		}
		res, err := m.ListBranches()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	mux.HandleFunc("/api/hoxi/worktree/checkout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Branch    string `json:"branch"`
			Create    bool   `json:"create"`
			Directory string `json:"directory"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.Directory != "" {
			m.SetRootDir(body.Directory)
		}
		st, err := m.CheckoutBranch(body.Branch, body.Create)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("/api/hoxi/worktree/rename-branch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			SessionID string `json:"sessionID"`
			NewBranch string `json:"newBranch"`
			Directory string `json:"directory"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.Directory != "" {
			m.SetRootDir(body.Directory)
		}
		br, err := m.RenameBranch(body.SessionID, body.NewBranch)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "branch": br})
	})

	mux.HandleFunc("/api/hoxi/worktree/prune-sandboxes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			ExcludeSessionID string `json:"excludeSessionID"`
			Directory        string `json:"directory"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Directory != "" {
			m.SetRootDir(body.Directory)
		}
		removed, err := m.PruneSandboxes(body.ExcludeSessionID)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removedCount": removed})
	})

	mux.HandleFunc("/api/hoxi/worktree/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		sessionID := strings.TrimPrefix(r.URL.Path, "/api/hoxi/worktree/")
		force := r.URL.Query().Get("force") == "true"
		keepBranch := r.URL.Query().Get("keepBranch") == "true"
		if err := m.RemoveWithOptions(sessionID, force, keepBranch); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
