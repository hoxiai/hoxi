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
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/agyhook"
)

// Hoxi owns exactly one named hook in agy's global hooks.json and leaves
// every other key untouched. The command degrades to a passthrough `allow`
// when the Hoxi binary is gone, so agy and the IDE keep working after an
// uninstall (a failing hook would turn every command into a tool error).

const agyHookName = "hoxi-approval-gate"

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

func defaultHookExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func agyHookCommand(exe string) string {
	q := shellQuote(exe)
	return fmt.Sprintf(`[ -x %s ] && exec %s %s || echo '{"decision":"allow"}'`, q, q, agyhook.Subcommand)
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
	return marshalJSON(map[string]interface{}{
		"PreToolUse": []interface{}{map[string]interface{}{
			"matcher": "run_command",
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hooks-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
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
	exe, err := hookExecutable()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Executable = exe
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
	return agyHookStatusLocked(), nil
}

func UninstallAgyApprovalHook() (AgyHookStatus, error) {
	agyHookMu.Lock()
	defer agyHookMu.Unlock()
	st := agyHookStatusLocked()
	if st.Error != "" || !st.Installed {
		return st, nil
	}
	doc, mode, err := readAgyHooksDoc(st.Path)
	if err != nil {
		return st, err
	}
	delete(doc, agyHookName)
	if err := writeAgyHooksDoc(st.Path, doc, mode); err != nil {
		return st, err
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
