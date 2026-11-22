package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/agyhook"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// Hoxi owns exactly one named hook in agy's global hooks.json and leaves
// every other key untouched. Outside Hoxi sessions (HOXI_RUNNER_SESSION_ID
// unset), the shell wrapper immediately returns a passthrough `allow` without
// spawning Hoxi, so terminal/IDE agy keeps working even after an uninstall.
// Inside Hoxi sessions, a missing binary falls back to HOXI_HOOK_BIN or the
// installed app bundle and otherwise fails closed (`deny`) with a clear reason.

var (
	agyHookMu       sync.Mutex
	agyHookWarned   bool
	hookExecutable  = defaultHookExecutable
	agyHooksPathFor = func() string {
		if gRoot := geminiHomeDir(); gRoot != "" {
			return filepath.Join(gRoot, "config", "hooks.json")
		}
		return ""
	}
)

type AgyHookStatus struct {
	Path       string `json:"path"`
	Executable string `json:"executable"`
	Installed  bool   `json:"installed"`
	Current    bool   `json:"current"`
	Error      string `json:"error,omitempty"`
}

func currentHookExecutable() (string, error) {
	agyHookMu.Lock()
	fn := hookExecutable
	agyHookMu.Unlock()
	return fn()
}

func looksLikeGoTestBinary(exe string) bool {
	lower := strings.ToLower(strings.TrimSpace(exe))
	return strings.HasSuffix(lower, ".test") || strings.HasSuffix(lower, ".test.exe")
}

func usableExecutableForOS(goos, raw string) (string, bool) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil || strings.TrimSpace(resolved) == "" {
		return "", false
	}
	st, err := os.Stat(resolved)
	if err != nil || st.IsDir() {
		return "", false
	}
	if goos != "windows" && st.Mode()&0o111 == 0 {
		return "", false
	}
	return resolved, true
}

func fallbackHookExecutables(goos, staleExe, homeDir, localAppData, programFiles string) []string {
	var raw []string
	switch goos {
	case "darwin":
		bundles := []string{"Hoxi.app", "Hoxi Dev.app"}
		if strings.Contains(staleExe, "Hoxi Dev.app") || strings.Contains(staleExe, "Hoxi-Dev.app") {
			bundles = []string{"Hoxi Dev.app", "Hoxi.app"}
		}
		roots := []string{"/Applications"}
		if h := strings.TrimSpace(homeDir); h != "" {
			roots = append(roots, filepath.Join(h, "Applications"))
		}
		for _, bundle := range bundles {
			for _, root := range roots {
				raw = append(raw, filepath.Join(root, bundle, "Contents", "MacOS", "Hoxi"))
			}
		}
	case "windows":
		if d := strings.TrimSpace(localAppData); d != "" {
			raw = append(raw, filepath.Join(d, "Programs", "Hoxi", "Hoxi.exe"))
		}
		if d := strings.TrimSpace(programFiles); d != "" {
			raw = append(raw, filepath.Join(d, "Hoxi", "Hoxi.exe"))
		}
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func resolveHookExecutable(goos, livePath, osExe string, fallbacks []string) (string, error) {
	if usable, ok := usableExecutableForOS(goos, livePath); ok {
		return usable, nil
	}
	if usable, ok := usableExecutableForOS(goos, osExe); ok {
		return usable, nil
	}
	for _, cand := range fallbacks {
		if usable, ok := usableExecutableForOS(goos, cand); ok {
			return usable, nil
		}
	}
	reported := firstNonEmptyStr(strings.TrimSpace(osExe), strings.TrimSpace(livePath), "hoxi")
	return "", fmt.Errorf("Hoxi executable %q no longer exists on disk; restart Hoxi from its installed application", reported)
}

func defaultHookExecutable() (string, error) {
	live := liveProcessExecutablePath()
	osExe, osErr := os.Executable()
	home, _ := os.UserHomeDir()
	fallbacks := fallbackHookExecutables(
		runtime.GOOS,
		firstNonEmptyStr(live, osExe),
		home,
		os.Getenv("LOCALAPPDATA"),
		os.Getenv("ProgramFiles"),
	)
	exe, err := resolveHookExecutable(runtime.GOOS, live, osExe, fallbacks)
	if err != nil && osErr != nil && strings.TrimSpace(live) == "" {
		return "", osErr
	}
	return exe, err
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func defaultShellFallbacksForOS(goos, exe string) []string {
	if goos != "darwin" {
		return nil
	}
	const prodApp = "/Applications/Hoxi.app/Contents/MacOS/Hoxi"
	if filepath.Clean(exe) == prodApp {
		return nil
	}
	return []string{prodApp}
}

func agyHookCommand(exe string) string {
	return agyHookCommandForOS(runtime.GOOS, exe)
}

func agyHookCommandForOS(goos, exe string) string {
	return agyHookCommandWithFallbacks(goos, exe, defaultShellFallbacksForOS(goos, exe))
}

func agyHookCommandWithFallbacks(goos, exe string, fallbacks []string) string {
	denyRaw := string(marshalJSON(agyhook.Decision{
		Decision: "deny",
		Reason:   agyhook.MissingBinaryReason,
	}, false))
	if goos == "windows" {
		q := `"` + strings.ReplaceAll(exe, `"`, `""`) + `"`
		return fmt.Sprintf(
			`if not defined %s (echo {"decision":"allow"}) else if exist %s (%s %s) else if defined %s (if exist "%%%s%%" ("%%%s%%" %s) else (echo %s)) else (echo %s)`,
			agyhook.EnvSessionID,
			q, q, agyhook.Subcommand,
			agyhook.EnvHookBin, agyhook.EnvHookBin, agyhook.EnvHookBin, agyhook.Subcommand,
			denyRaw,
			denyRaw,
		)
	}
	q := shellQuote(exe)
	branches := []string{
		fmt.Sprintf(`elif [ -x %s ]; then exec %s %s`, q, q, agyhook.Subcommand),
		fmt.Sprintf(`elif [ -n "$%s" ] && [ -x "$%s" ]; then exec "$%s" %s`, agyhook.EnvHookBin, agyhook.EnvHookBin, agyhook.EnvHookBin, agyhook.Subcommand),
	}
	for _, fb := range fallbacks {
		fb = strings.TrimSpace(fb)
		if fb == "" || fb == exe {
			continue
		}
		qfb := shellQuote(fb)
		branches = append(branches, fmt.Sprintf(`elif [ -x %s ]; then exec %s %s`, qfb, qfb, agyhook.Subcommand))
	}
	return fmt.Sprintf(
		`if [ -z "$%s" ]; then echo '{"decision":"allow"}'; %s; else echo %s; fi`,
		agyhook.EnvSessionID,
		strings.Join(branches, "; "),
		shellQuote(denyRaw),
	)
}

// marshalJSON keeps `&&` and quotes readable in the user's hooks.json.
func marshalJSON(v interface{}, indent bool) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	_ = enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func desiredAgyHook(exe string) json.RawMessage {
	// agy's matcher is a regular expression and "*" matches every tool. A
	// wildcard keeps new tools (read_file arrived in 1.4.3 after view_file)
	// from silently bypassing the gate; classification happens in the gate.
	return marshalJSON(map[string]interface{}{
		"PreToolUse": []interface{}{map[string]interface{}{
			"matcher": "*",
			"hooks": []interface{}{map[string]interface{}{
				"type":    "command",
				"command": agyHookCommand(exe),
				"timeout": agyhook.TimeoutSeconds,
			}},
		}},
	}, false)
}

func readAgyHooksDoc(path string) (map[string]json.RawMessage, os.FileMode, error) {
	doc := map[string]json.RawMessage{}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return doc, 0o644, nil
	}
	if err != nil {
		return nil, 0, err
	}
	mode := os.FileMode(0o644)
	if st, statErr := os.Stat(path); statErr == nil {
		mode = st.Mode().Perm()
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return doc, mode, nil
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, 0, fmt.Errorf("refusing to modify unparseable %s: %w", path, err)
	}
	return doc, mode, nil
}

func writeAgyHooksDoc(path string, doc map[string]json.RawMessage, mode os.FileMode) error {
	out := marshalJSON(doc, true)
	return hoxipaths.WriteFileAtomic(path, append(out, '\n'), mode)
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y interface{}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return bytes.Equal(marshalJSON(x, false), marshalJSON(y, false))
}

func AgyApprovalHookStatus() AgyHookStatus {
	agyHookMu.Lock()
	defer agyHookMu.Unlock()
	return agyHookStatusLocked()
}

func agyHookStatusLocked() AgyHookStatus {
	st := AgyHookStatus{Path: agyHooksPathFor()}
	if st.Path == "" {
		st.Error = "cannot resolve Antigravity config directory"
		return st
	}
	doc, _, err := readAgyHooksDoc(st.Path)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	cur, ok := doc[agyHookName]
	st.Installed = ok
	exe, exeErr := hookExecutable()
	if exeErr != nil {
		st.Error = exeErr.Error()
		return st
	}
	st.Executable = exe
	st.Current = ok && sameJSON(cur, desiredAgyHook(exe))
	return st
}

// InstallAgyApprovalHook writes or refreshes Hoxi's entry; it is a no-op when
// the entry already points at the running executable.
func InstallAgyApprovalHook() (AgyHookStatus, error) {
	agyHookMu.Lock()
	defer agyHookMu.Unlock()
	st := agyHookStatusLocked()
	if st.Error != "" {
		return st, errors.New(st.Error)
	}
	if st.Current {
		return st, nil
	}
	doc, mode, err := readAgyHooksDoc(st.Path)
	if err != nil {
		return st, err
	}
	doc[agyHookName] = desiredAgyHook(st.Executable)
	if err := writeAgyHooksDoc(st.Path, doc, mode); err != nil {
		return st, err
	}
	agyHookWarned = false
	return agyHookStatusLocked(), nil
}

func UninstallAgyApprovalHook() (AgyHookStatus, error) {
	agyHookMu.Lock()
	defer agyHookMu.Unlock()
	path := agyHooksPathFor()
	if path == "" {
		return agyHookStatusLocked(), nil
	}
	doc, mode, err := readAgyHooksDoc(path)
	if err != nil {
		return agyHookStatusLocked(), nil
	}
	if _, ok := doc[agyHookName]; !ok {
		return agyHookStatusLocked(), nil
	}
	delete(doc, agyHookName)
	if err := writeAgyHooksDoc(path, doc, mode); err != nil {
		return agyHookStatusLocked(), err
	}
	return agyHookStatusLocked(), nil
}

// EnableAgyHookAutoInstall lets the desktop app opt in; tests and library
// users never touch ~/.gemini implicitly.
func (s *Service) EnableAgyHookAutoInstall() {
	s.gate.mu.Lock()
	s.gate.autoInstall = true
	s.gate.mu.Unlock()
}

func (s *Service) ensureAgyHookInstalled() {
	s.gate.mu.Lock()
	enabled := s.gate.autoInstall
	s.gate.mu.Unlock()
	if !enabled {
		return
	}
	if _, err := InstallAgyApprovalHook(); err != nil {
		agyHookMu.Lock()
		warn := !agyHookWarned
		agyHookWarned = true
		agyHookMu.Unlock()
		if warn {
			slog.Warn("failed to install Antigravity approval hook; agy will soft-deny commands needing approval", "error", err)
		}
	}
}

func (s *Service) handleAgyHookInstall(w http.ResponseWriter, r *http.Request) {
	var (
		st  AgyHookStatus
		err error
	)
	switch r.Method {
	case http.MethodGet:
		st = AgyApprovalHookStatus()
	case http.MethodPost:
		st, err = InstallAgyApprovalHook()
	case http.MethodDelete:
		st, err = UninstallAgyApprovalHook()
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}
