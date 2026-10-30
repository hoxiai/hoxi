package worktree

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
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
		q := r.URL.Query()
		sessionID := q.Get("sessionID")
		dir := q.Get("directory")
		if !m.checkSessionDirectory(w, sessionID, dir) {
			return
		}
		st, err := m.scoped(dir, sessionID).StatusWithExtraDirs(sessionID, q["extraDir"])
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("/api/hoxi/worktree/diff", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sessionID := q.Get("sessionID")
		dir := q.Get("directory")
		if !m.checkSessionDirectory(w, sessionID, dir) {
			return
		}
		scoped := m.scoped(dir, sessionID)
		if file := strings.TrimSpace(q.Get("file")); file != "" {
			diff, err := scoped.DiffFile(sessionID, file)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, diff)
			return
		}
		diffs, err := scoped.DiffWithExtraDirs(sessionID, q["extraDir"])
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
			Message   string `json:"message"`
			Directory string `json:"directory"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		dir := firstNonEmpty(body.Directory, r.URL.Query().Get("directory"))
		if !m.checkSessionDirectory(w, body.SessionID, body.Directory, r.URL.Query().Get("directory")) {
			return
		}
		if err := m.scoped(dir, body.SessionID).ApplyWithMessage(body.SessionID, body.Mode, body.Message); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/hoxi/worktree/sync", func(w http.ResponseWriter, r *http.Request) {
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
		if !m.checkSessionDirectory(w, body.SessionID, body.Directory, r.URL.Query().Get("directory")) {
			return
		}
		dir := firstNonEmpty(body.Directory, r.URL.Query().Get("directory"))
		st, err := m.scoped(dir, body.SessionID).SyncWithBase(body.SessionID)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("/api/hoxi/worktree/revert-file", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			SessionID string `json:"sessionID"`
			File      string `json:"file"`
			Directory string `json:"directory"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !m.checkSessionDirectory(w, body.SessionID, body.Directory, r.URL.Query().Get("directory")) {
			return
		}
		dir := firstNonEmpty(body.Directory, r.URL.Query().Get("directory"))
		if err := m.scoped(dir, body.SessionID).RevertFile(body.SessionID, body.File); err != nil {
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
			SessionID    string         `json:"sessionID"`
			Message      string         `json:"message"`
			Push         bool           `json:"push"`
			Directory    string         `json:"directory"`
			SessionDiffs *[]SessionDiff `json:"sessionDiffs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		var res CommitAndPushResult
		var err error
		binding, known, bindErr := m.BindingForSession(body.SessionID)
		if bindErr != nil {
			writeJSONError(w, http.StatusForbidden, bindErr.Error())
			return
		}
		// ParseQuery reports malformed selectors that URL.Query would drop.
		query, queryErr := url.ParseQuery(r.URL.RawQuery)
		if queryErr != nil || len(query["directory"]) > 1 {
			writeJSONError(w, http.StatusBadRequest, "invalid directory query")
			return
		}
		selected := ""
		selectors := []string{}
		if body.Directory != "" {
			selectors = append(selectors, body.Directory)
		}
		if values, present := query["directory"]; present {
			selectors = append(selectors, values[0])
		}
		for _, selector := range selectors {
			canonical, resolveErr := bindingFromDirectory(strings.TrimSpace(selector))
			if !filepath.IsAbs(strings.TrimSpace(selector)) || resolveErr != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid session directory selector")
				return
			}
			if known && canonical.Directory != binding.Directory {
				writeJSONError(w, http.StatusForbidden, "session directory does not match its sandbox")
				return
			}
			if selected != "" && selected != canonical.Directory {
				writeJSONError(w, http.StatusBadRequest, "conflicting session directory selectors")
				return
			}
			selected = canonical.Directory
		}
		if known {
			selected = binding.Directory
		}
		if body.SessionDiffs != nil {
			res, err = m.scoped(selected, body.SessionID).CommitSessionChanges(body.SessionID, body.Message, body.Push, *body.SessionDiffs)
		} else {
			res, err = m.scoped(selected, body.SessionID).CommitAndPushWorktree(body.SessionID, body.Message, body.Push)
		}
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
		if !m.checkSessionDirectory(w, body.SessionID, body.Directory, r.URL.Query().Get("directory")) {
			return
		}
		dir := firstNonEmpty(body.Directory, r.URL.Query().Get("directory"))
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
		if !m.checkSessionDirectory(w, body.SessionID, body.Directory, r.URL.Query().Get("directory")) {
			return
		}
		dir := firstNonEmpty(body.Directory, r.URL.Query().Get("directory"))
		br, err := m.scoped(dir, body.SessionID).RenameBranch(body.SessionID, body.NewBranch)
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

	mux.HandleFunc("/api/hoxi/worktree/setup-retry", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			SessionID string `json:"sessionID"`
			Directory string `json:"directory"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !m.checkSessionDirectory(w, body.SessionID, body.Directory, r.URL.Query().Get("directory")) {
			return
		}
		dir := firstNonEmpty(body.Directory, r.URL.Query().Get("directory"))
		st, err := m.scoped(dir, body.SessionID).RetrySetup(body.SessionID)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("/api/hoxi/worktree/verify-status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		sessionID := q.Get("sessionID")
		dir := q.Get("directory")
		if !m.checkSessionDirectory(w, sessionID, dir) {
			return
		}
		st, err := m.scoped(dir, sessionID).VerifyStatus(sessionID)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("/api/hoxi/worktree/verify-config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Directory   string `json:"directory"`
			RunCommand  string `json:"runCommand"`
			TestCommand string `json:"testCommand"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		dir := firstNonEmpty(body.Directory, r.URL.Query().Get("directory"))
		cfg, err := m.ForDirectory(dir).SaveVerifyConfig(WorktreeVerifyConfig{
			RunCommand:  body.RunCommand,
			TestCommand: body.TestCommand,
		})
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	})

	mux.HandleFunc("/api/hoxi/worktree/verify-run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			SessionID string `json:"sessionID"`
			Directory string `json:"directory"`
			Kind      string `json:"kind"`
			Command   string `json:"command"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !m.checkSessionDirectory(w, body.SessionID, body.Directory, r.URL.Query().Get("directory")) {
			return
		}
		dir := firstNonEmpty(body.Directory, r.URL.Query().Get("directory"))
		st, err := m.scoped(dir, body.SessionID).StartVerify(body.SessionID, body.Kind, body.Command)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("/api/hoxi/worktree/verify-stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			SessionID string `json:"sessionID"`
			Directory string `json:"directory"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !m.checkSessionDirectory(w, body.SessionID, body.Directory, r.URL.Query().Get("directory")) {
			return
		}
		dir := firstNonEmpty(body.Directory, r.URL.Query().Get("directory"))
		st, err := m.scoped(dir, body.SessionID).StopVerify(body.SessionID)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.HandleFunc("/api/hoxi/worktree/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		sessionID := strings.TrimPrefix(r.URL.Path, "/api/hoxi/worktree/")
		dir := r.URL.Query().Get("directory")
		if !m.checkSessionDirectory(w, sessionID, dir) {
			return
		}
		force := r.URL.Query().Get("force") == "true"
		keepBranch := r.URL.Query().Get("keepBranch") == "true"
		if err := m.scoped(dir, sessionID).RemoveWithOptions(sessionID, force, keepBranch); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}

func (m *worktreeManager) checkSessionDirectory(w http.ResponseWriter, sessionID string, dirs ...string) bool {
	if len(dirs) == 0 {
		if !m.sessionAllowsDirectory(sessionID, "") {
			writeJSONError(w, http.StatusForbidden, "session directory does not match its sandbox")
			return false
		}
		return true
	}
	for _, dir := range dirs {
		if !m.sessionAllowsDirectory(sessionID, dir) {
			writeJSONError(w, http.StatusForbidden, "session directory does not match its sandbox")
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
