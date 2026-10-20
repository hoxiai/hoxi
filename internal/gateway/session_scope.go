package gateway

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

const sessionDirectoryHeader = "X-Hoxi-Directory"

func (g *Gateway) guardSessionScope(w http.ResponseWriter, r *http.Request) bool {
	sessionID, ok := sessionPathID(r.URL.Path)
	if !ok || g.worktrees == nil {
		return true
	}
	queryDir, queryPresent, err := directoryQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	headerValues := r.Header.Values(sessionDirectoryHeader)
	if len(headerValues) > 1 || (len(headerValues) == 1 && strings.TrimSpace(headerValues[0]) == "") {
		http.Error(w, "invalid session directory header", http.StatusBadRequest)
		return false
	}
	headerDir := ""
	if len(headerValues) == 1 {
		headerDir = strings.TrimSpace(headerValues[0])
	}
	if (queryPresent && !directorySelectorValid(queryDir)) || (headerDir != "" && !directorySelectorValid(headerDir)) {
		http.Error(w, "malformed session directory selector", http.StatusBadRequest)
		return false
	}
	// This is a gateway-only selector; never let upstream interpret it.
	r.Header.Del(sessionDirectoryHeader)
	if queryPresent && headerDir != "" && !sameDirectory(queryDir, headerDir) {
		http.Error(w, "conflicting session directory selectors", http.StatusBadRequest)
		return false
	}

	binding, known, err := g.worktrees.BindingForSession(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return false
	}
	if !known {
		return true
	}
	selected := queryDir
	if selected == "" {
		selected = headerDir
	}
	if selected != "" && !sameDirectory(selected, binding.Directory) {
		http.Error(w, "session directory does not match its sandbox", http.StatusForbidden)
		return false
	}
	setDirectoryQuery(r, binding.Directory)
	return true
}

func sessionPathID(requestPath string) (string, bool) {
	if strings.HasPrefix(requestPath, "/api/hoxi/runner/sessions/") {
		parts := strings.Split(strings.TrimPrefix(requestPath, "/api/hoxi/runner/sessions/"), "/")
		if len(parts) == 0 || parts[0] == "" || parts[0] == "status" {
			return "", false
		}
		return parts[0], true
	}
	p := strings.TrimPrefix(requestPath, "/api")
	if p != "/session" && !strings.HasPrefix(p, "/session/") {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(p, "/session/"), "/")
	if len(parts) == 0 || parts[0] == "" || parts[0] == "session" {
		return "", false
	}
	return parts[0], true
}

func directoryQuery(rawQuery string) (string, bool, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", false, errors.New("malformed query string")
	}
	dirs, present := values["directory"]
	if len(dirs) > 1 {
		return "", false, errors.New("duplicate directory selector")
	}
	if !present {
		return "", false, nil
	}
	if strings.TrimSpace(dirs[0]) == "" {
		return "", false, errors.New("empty directory selector")
	}
	return dirs[0], true, nil
}

func sameDirectory(left, right string) bool {
	leftCanonical, leftOK := canonicalDirectory(left)
	rightCanonical, rightOK := canonicalDirectory(right)
	return leftOK && rightOK && leftCanonical == rightCanonical
}

func canonicalDirectory(directory string) (string, bool) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", false
	}
	resolved, err = filepath.Abs(resolved)
	return filepath.Clean(resolved), err == nil
}

func directorySelectorValid(directory string) bool {
	return filepath.IsAbs(strings.TrimSpace(directory)) && func() bool {
		_, ok := canonicalDirectory(directory)
		return ok
	}()
}

func setDirectoryQuery(r *http.Request, directory string) {
	values, _ := url.ParseQuery(r.URL.RawQuery)
	values.Set("directory", directory)
	r.URL.RawQuery = values.Encode()
}
