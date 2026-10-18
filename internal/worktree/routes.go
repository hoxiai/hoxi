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
		meta, err := m.scoped(body.Directory, body.SessionID).Create(body.SessionID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, meta)
	})

	mux.HandleFunc("/api/hoxi/worktree/status", func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("sessionID")
		st, err := m.scoped(r.URL.Query().Get("directory"), sessionID).Status(sessionID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("/api/hoxi/worktree/diff", func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("sessionID")
		diffs, err := m.scoped(r.URL.Query().Get("directory"), sessionID).Diff(sessionID)
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
		if err := m.scoped(r.URL.Query().Get("directory"), body.SessionID).Apply(body.SessionID, body.Mode); err != nil {
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
		if err := m.scoped(r.URL.Query().Get("directory"), body.SessionID).RevertFile(body.SessionID, body.File); err != nil {
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
			Push      bool   `json:"push"`
			Directory string `json:"directory"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		dir := body.Directory
		if dir == "" {
			dir = r.URL.Query().Get("directory")
		}
		res, err := m.scoped(dir, body.SessionID).CommitAndPushWorktree(body.SessionID, body.Message, body.Push)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	mux.HandleFunc("/api/hoxi/worktree/push", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			SessionID string `json:"sessionID"`
			Directory string `json:"directory"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		dir := body.Directory
		if dir == "" {
			dir = r.URL.Query().Get("directory")
		}
		res, err := m.scoped(dir, body.SessionID).PushWorktree(body.SessionID)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	mux.HandleFunc("/api/hoxi/worktree/branches", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		res, err := m.ForDirectory(r.URL.Query().Get("directory")).ListBranches()
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
		st, err := m.ForDirectory(body.Directory).CheckoutBranch(body.Branch, body.Create)
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
		br, err := m.scoped(body.Directory, body.SessionID).RenameBranch(body.SessionID, body.NewBranch)
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
		removed, err := m.ForDirectory(body.Directory).PruneSandboxes(body.ExcludeSessionID)
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
		if err := m.scoped(r.URL.Query().Get("directory"), sessionID).RemoveWithOptions(sessionID, force, keepBranch); err != nil {
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
