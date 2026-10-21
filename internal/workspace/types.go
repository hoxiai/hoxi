package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func generateRandomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

const defaultWorkspaceIdentityColor = "#5EA0F7"

var (
	shortHexColorRE = regexp.MustCompile(`^#([0-9a-fA-F]{3})$`)
	longHexColorRE  = regexp.MustCompile(`^#([0-9a-fA-F]{6})$`)
)

type workspaceFolderInput struct {
	Path      string `json:"path"`
	IsPrimary bool   `json:"isPrimary"`
}

type workspaceFolder struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	IsPrimary bool   `json:"isPrimary"`
	Position  int    `json:"position"`
}

type workspaceProfile struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Color     string            `json:"color,omitempty"`
	Folders   []workspaceFolder `json:"folders"`
	// Hidden projects are registered automatically for directories that own
	// sessions but were never saved as a workspace. They keep stable session
	// attribution without adding rows to the sidebar.
	Hidden    bool   `json:"hidden,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type saveWorkspaceRequest struct {
	ID      string                 `json:"id,omitempty"`
	Name    string                 `json:"name,omitempty"`
	Color   string                 `json:"color,omitempty"`
	Folders []workspaceFolderInput `json:"folders"`
}

type workspaceStoreFile struct {
	Workspaces []workspaceProfile `json:"workspaces"`
	Dismissed  []string           `json:"dismissed,omitempty"`
}

func userHoxiWorkspacesPath() string {
	return hoxipaths.Join("workspaces.json")
}

func normalizeWorkspaceIdentityColor(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if m := shortHexColorRE.FindStringSubmatch(trimmed); len(m) == 2 {
		d := strings.ToUpper(m[1])
		return fmt.Sprintf("#%c%c%c%c%c%c", d[0], d[0], d[1], d[1], d[2], d[2])
	}
	if m := longHexColorRE.FindStringSubmatch(trimmed); len(m) == 2 {
		return "#" + strings.ToUpper(m[1])
	}
	return ""
}

func comparableFolderPath(p string) string {
	cleaned := filepath.Clean(strings.TrimSpace(p))
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}

func canonicalHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil && resolved != "" {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(home)
}

// CleanValidProjectDir resolves and validates whether dir can serve as a workspace project root.
// It rejects empty paths, non-existent directories, filesystem roots ("/"), the user's home directory,
// and directories inside a macOS .app bundle.
func CleanValidProjectDir(dir string) string {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" || trimmed == "." {
		return ""
	}
	cleaned := filepath.Clean(trimmed)
	resolved := cleaned
	if realPath, err := filepath.EvalSymlinks(cleaned); err == nil && realPath != "" {
		resolved = filepath.Clean(realPath)
	}
	stat, err := os.Stat(resolved)
	if err != nil || !stat.IsDir() {
		return ""
	}
	comp := comparableFolderPath(resolved)
	home := comparableFolderPath(canonicalHomeDir())
	if comp == home || comp == "/" || comp == "\\" || filepath.Dir(resolved) == resolved {
		return ""
	}
	if strings.Contains(resolved, ".app/Contents") {
		return ""
	}
	return cleaned
}

func ResolveInitialRootDir(candidate string) string {
	if valid := CleanValidProjectDir(candidate); valid != "" {
		return valid
	}
	return newWorkspaceStore("").PrimaryRootDir("")
}

func validateWorkspaceFolders(inputs []workspaceFolderInput) ([]workspaceFolderInput, error) {
	if len(inputs) == 0 {
		return nil, errors.New("A workspace needs at least one folder.")
	}
	home := comparableFolderPath(canonicalHomeDir())
	canonical := make([]workspaceFolderInput, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))

	for _, item := range inputs {
		trimmed := strings.TrimSpace(item.Path)
		if trimmed == "" {
			continue
		}
		resolved := filepath.Clean(trimmed)
		if realPath, err := filepath.EvalSymlinks(resolved); err == nil && realPath != "" {
			resolved = filepath.Clean(realPath)
		}
		stat, err := os.Stat(resolved)
		if err != nil || !stat.IsDir() {
			continue
		}
		comp := comparableFolderPath(resolved)
		if _, ok := seen[comp]; ok {
			continue
		}
		seen[comp] = struct{}{}
		if comp == home || comp == "/" || comp == "\\" || filepath.Dir(resolved) == resolved {
			return nil, errors.New("Filesystem roots and the home directory cannot be workspace folders.")
		}
		canonical = append(canonical, workspaceFolderInput{Path: resolved, IsPrimary: item.IsPrimary})
	}

	if len(canonical) == 0 {
		return nil, errors.New("A workspace needs at least one folder.")
	}
	var primary workspaceFolderInput
	others := make([]workspaceFolderInput, 0, len(canonical)-1)
	for _, f := range canonical {
		if f.IsPrimary && primary.Path == "" {
			primary = workspaceFolderInput{Path: f.Path, IsPrimary: true}
		} else {
			others = append(others, workspaceFolderInput{Path: f.Path, IsPrimary: false})
		}
	}
	if primary.Path == "" && len(others) > 0 {
		primary = workspaceFolderInput{Path: others[0].Path, IsPrimary: true}
		others = others[1:]
	}
	return append([]workspaceFolderInput{primary}, others...), nil
}

func generateWorkspaceUUID() string {
	if hexStr, err := generateRandomHex(16); err == nil && len(hexStr) == 32 {
		return fmt.Sprintf("%s-%s-%s-%s-%s",
			hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:32])
	}
	return fmt.Sprintf("ws_%d", time.Now().UnixNano())
}
