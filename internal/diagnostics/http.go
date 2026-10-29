package diagnostics

import (
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

// Handler serves diagnostics, a bounded metadata-only export, and the fixed
// directory opener.
// Mount behind the gateway's existing origin/auth checks.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := defaultStore()
		if s == nil {
			http.Error(w, "diagnostics unavailable", http.StatusServiceUnavailable)
			return
		}
		s.serveHTTP(w, r)
	})
}

func (s *store) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	export := r.URL.Path == "/api/hoxi/diagnostics/export"
	open := r.URL.Path == "/api/hoxi/diagnostics/open-directory"
	if r.URL.Path != "/api/hoxi/diagnostics" && !export && !open {
		http.NotFound(w, r)
		return
	}
	method := http.MethodGet
	if open {
		method = http.MethodPost
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": method + " required"})
		return
	}
	if open {
		s.mu.Lock()
		err := s.prepare()
		s.mu.Unlock()
		if err == nil {
			opener := s.openDirectory
			if opener == nil {
				opener = openLogDirectory
			}
			err = opener(s.directory)
		}
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to open diagnostics directory"})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		}
		return
	}
	q := r.URL.Query()
	limit := 200
	if raw := q.Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid limit"})
			return
		}
		limit = min(v, 1000)
	}
	entries, err := s.entries(filter{q.Get("kind"), q.Get("level"), q.Get("sessionID"), q.Get("providerID"), q.Get("q"), limit})
	body := map[string]any{"entries": entries, "directory": s.directory, "limit": limit, "retentionDays": 7, "maxBytes": maxBytes}
	if err != nil {
		body["error"] = "diagnostics storage unavailable"
	}
	if export {
		delete(body, "directory")
		w.Header().Set("Content-Disposition", `attachment; filename="hoxi-diagnostics.json"`)
		body["exportedAt"] = time.Now().UTC()
		body["version"] = 1
	}
	_ = json.NewEncoder(w).Encode(body)
}

func openLogDirectory(directory string) error {
	program := "xdg-open"
	switch runtime.GOOS {
	case "darwin":
		program = "open"
	case "windows":
		program = "explorer.exe"
	}
	// No shell, user-supplied arguments, or captured child output.
	err := exec.Command(program, directory).Run()
	var exitErr *exec.ExitError
	if runtime.GOOS == "windows" && errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil
	}
	return err
}
