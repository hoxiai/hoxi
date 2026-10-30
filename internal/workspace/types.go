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
	// ID names an existing folder of the profile being saved. Sending a known
	// ID with a different path is an explicit relocation; without an ID a
	// path change never moves sessions.
	ID        string `json:"id,omitempty"`
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
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Color   string            `json:"color,omitempty"`
	Surface string            `json:"surface,omitempty"`
	Folders []workspaceFolder `json:"folders"`
	// Hidden projects are registered automatically for directories that own
	// sessions but were never saved as a workspace. They keep stable session
	// attribution without adding rows to the sidebar.
	Hidden bool `json:"hidden,omitempty"`
	// Aliases are IDs of hidden projects merged into this profile. Sessions
	// still carrying an alias ID resolve to this profile.
	Aliases   []string `json:"aliases,omitempty"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
}

type saveWorkspaceRequest struct {
	ID      string                 `json:"id,omitempty"`
	Name    string                 `json:"name,omitempty"`
	Color   string                 `json:"color,omitempty"`
	Surface string                 `json:"surface,omitempty"`
	Folders []workspaceFolderInput `json:"folders"`
}

func normalizeWorkspaceSurface(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "work":
		return "work"
	case "code":
		return "code"
	default:
		return ""
	}
}

func isDefaultWorkSurfaceDir(path string) bool {
	slash := strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(path))))
	return strings.HasSuffix(slash, "/.hoxi/workspaces/default") ||
		strings.Contains(slash, "/.hoxi/workspaces/default/")
}

type workspaceStoreFile struct {
	Workspaces []workspaceProfile `json:"workspaces"`
	Dismissed  []string           `json:"dismissed,omitempty"`
	Removed    []removedProject   `json:"removed,omitempty"`
}

// removedProject is a tombstone for a folder whose project was removed.
// Registering the folder again reuses the ID, so sessions created before
// the removal stay attributed to it.
type removedProject struct {
	Path    string   `json:"path"`
	ID      string   `json:"id"`
	Aliases []string `json:"aliases,omitempty"`
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

func expandUserPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "~" {
		return canonicalHomeDir()
	}
	if strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, `~\`) {
		if home := canonicalHomeDir(); home != "" {
			return filepath.Join(home, trimmed[2:])
		}
	}
	return trimmed
}

// CleanValidProjectDir resolves and validates whether dir can serve as a workspace project root.
// It rejects empty paths, non-existent directories, filesystem roots ("/"), the user's home directory,
// directories inside a macOS .app bundle, the Hoxi application binary directory, and the built-in
// default workspace directory (~/.hoxi/workspaces/default).
func CleanValidProjectDir(dir string) string {
	trimmed := expandUserPath(dir)
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
	if isForbiddenProjectRoot(resolved) {
		return ""
	}
	if isAppInstallOrDefaultWorkspaceDir(resolved) {
		return ""
	}
	return cleaned
}

func isAppInstallOrDefaultWorkspaceDir(resolved string) bool {
	if strings.Contains(resolved, ".app/Contents") {
		return true
	}
	slash := strings.ToLower(filepath.ToSlash(resolved))
	if strings.HasSuffix(slash, "/.hoxi/workspaces/default") || strings.Contains(slash, "/.hoxi/workspaces/default/") {
		return true
	}
	if strings.Contains(slash, "/appdata/local/programs/hoxi") ||
		strings.Contains(slash, "/program files/hoxi") ||
		strings.Contains(slash, "/program files (x86)/hoxi") {
		return true
	}
	if slash == "c:/windows" || strings.HasPrefix(slash, "c:/windows/") {
		return true
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		exeDir := filepath.Clean(filepath.Dir(exe))
		if realExeDir, err := filepath.EvalSymlinks(exeDir); err == nil && realExeDir != "" {
			exeDir = filepath.Clean(realExeDir)
		}
		if comparableFolderPath(resolved) == comparableFolderPath(exeDir) {
			return true
		}
	}
	return false
}

func ResolveInitialRootDir(candidate string) string {
	if valid := CleanValidProjectDir(candidate); valid != "" {
		return valid
	}
	return newWorkspaceStore("").PrimaryRootDir("")
}

// isForbiddenProjectRoot reports whether resolved is the user's home
// directory or a filesystem root. Neither may own a project: every session
// on the machine would be attributed to it.
func isForbiddenProjectRoot(resolved string) bool {
	comp := comparableFolderPath(resolved)
	home := comparableFolderPath(canonicalHomeDir())
	return comp == home || comp == "/" || comp == "\\" || filepath.Dir(resolved) == resolved
}

func validateWorkspaceFolders(inputs []workspaceFolderInput) ([]workspaceFolderInput, error) {
	return validateWorkspaceFoldersKeeping(inputs, nil)
}

// validateWorkspaceFoldersKeeping canonicalizes folders and moves the primary
// first. Missing paths are dropped unless keepMissing accepts them: updates
// keep folders that are already saved but temporarily absent on disk, so a
// re-save never silently forgets them.
func validateWorkspaceFoldersKeeping(inputs []workspaceFolderInput, keepMissing func(comp string) bool) ([]workspaceFolderInput, error) {
	if len(inputs) == 0 {
		return nil, errors.New("A workspace needs at least one folder.")
	}
	canonical := make([]workspaceFolderInput, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))

	for _, item := range inputs {
		trimmed := expandUserPath(item.Path)
		if trimmed == "" {
			continue
		}
		resolved := filepath.Clean(trimmed)
		if realPath, err := filepath.EvalSymlinks(resolved); err == nil && realPath != "" {
			resolved = filepath.Clean(realPath)
		}
		stat, err := os.Stat(resolved)
		if err != nil || !stat.IsDir() {
			if err == nil || keepMissing == nil || !keepMissing(comparableFolderPath(resolved)) {
				continue
			}
		}
		comp := comparableFolderPath(resolved)
		if _, ok := seen[comp]; ok {
			continue
		}
		seen[comp] = struct{}{}
		if isForbiddenProjectRoot(resolved) {
			return nil, errors.New("Filesystem roots and the home directory cannot be workspace folders.")
		}
		canonical = append(canonical, workspaceFolderInput{
			ID: strings.TrimSpace(item.ID), Path: resolved, IsPrimary: item.IsPrimary,
		})
	}

	if len(canonical) == 0 {
		return nil, errors.New("A workspace needs at least one folder.")
	}
	primaryIndex := 0
	for index, f := range canonical {
		if f.IsPrimary {
			primaryIndex = index
			break
		}
	}
	ordered := make([]workspaceFolderInput, 0, len(canonical))
	primary := canonical[primaryIndex]
	primary.IsPrimary = true
	ordered = append(ordered, primary)
	for index, f := range canonical {
		if index != primaryIndex {
			f.IsPrimary = false
			ordered = append(ordered, f)
		}
	}
	return ordered, nil
}

func generateWorkspaceUUID() string {
	if hexStr, err := generateRandomHex(16); err == nil && len(hexStr) == 32 {
		return fmt.Sprintf("%s-%s-%s-%s-%s",
			hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:32])
	}
	return fmt.Sprintf("ws_%d", time.Now().UnixNano())
}
