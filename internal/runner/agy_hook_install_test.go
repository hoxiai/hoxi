package runner

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func withHookPaths(t *testing.T, exe string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config", "hooks.json")
	prevPath, prevExe := agyHooksPathFor, hookExecutable
	agyHooksPathFor = func() string { return path }
	hookExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { agyHooksPathFor, hookExecutable = prevPath, prevExe })
	return path
}

func readHooks(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v", err)
	}
	return doc
}

func TestInstallAgyHookCreatesMergesAndRefreshes(t *testing.T) {
	path := withHookPaths(t, "/Applications/Hoxi.app/Contents/MacOS/hoxi")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"lint":{"PostToolUse":[{"matcher":"run_command","hooks":[{"command":"./lint.sh"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := InstallAgyApprovalHook()
	if err != nil || !st.Installed || !st.Current {
		t.Fatalf("install: %+v %v", st, err)
	}
	doc := readHooks(t, path)
	if !strings.Contains(string(doc["lint"]), "./lint.sh") {
		t.Fatalf("user hook was not preserved: %s", doc["lint"])
	}
	var entry struct {
		PreToolUse []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"PreToolUse"`
	}
	if err := json.Unmarshal(doc[agyHookName], &entry); err != nil || len(entry.PreToolUse) != 1 || len(entry.PreToolUse[0].Hooks) != 1 {
		t.Fatalf("unexpected hook entry: %s (%v)", doc[agyHookName], err)
	}
	h := entry.PreToolUse[0].Hooks[0]
	if entry.PreToolUse[0].Matcher != "run_command" || h.Type != "command" || h.Timeout != 600 ||
		h.Command != agyHookCommand("/Applications/Hoxi.app/Contents/MacOS/hoxi") {
		t.Fatalf("unexpected hook entry: %+v", entry)
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), `\u0026`) {
		t.Fatalf("hooks.json should keep && readable: %s", raw)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode changed to %v", fi.Mode().Perm())
	}

	before, _ := os.ReadFile(path)
	if _, err := InstallAgyApprovalHook(); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatalf("idempotent install rewrote the file")
	}

	hookExecutable = func() (string, error) { return "/Applications/Hoxi 2.app/Contents/MacOS/hoxi", nil }
	if st := AgyApprovalHookStatus(); !st.Installed || st.Current {
		t.Fatalf("moved binary should be stale: %+v", st)
	}
	if st, err := InstallAgyApprovalHook(); err != nil || !st.Current {
		t.Fatalf("refresh: %+v %v", st, err)
	}

	if st, err := UninstallAgyApprovalHook(); err != nil || st.Installed {
		t.Fatalf("uninstall: %+v %v", st, err)
	}
	doc = readHooks(t, path)
	if _, ok := doc[agyHookName]; ok || doc["lint"] == nil {
		t.Fatalf("uninstall must only remove Hoxi's entry: %v", doc)
	}
}

func TestInstallAgyHookRefusesUnparseableFile(t *testing.T) {
	path := withHookPaths(t, "/usr/local/bin/hoxi")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(`{ not json`), 0o644)
	if _, err := InstallAgyApprovalHook(); err == nil {
		t.Fatalf("expected an error for an unparseable hooks.json")
	}
	if raw, _ := os.ReadFile(path); string(raw) != `{ not json` {
		t.Fatalf("unparseable file was modified: %q", raw)
	}
}

func TestAutoInstallIsOptIn(t *testing.T) {
	path := withHookPaths(t, "/usr/local/bin/hoxi")
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.ensureAgyHookInstalled()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("hooks.json written without opt-in")
	}
	svc.EnableAgyHookAutoInstall()
	svc.ensureAgyHookInstalled()
	if st := AgyApprovalHookStatus(); !st.Current {
		t.Fatalf("opt-in install failed: %+v", st)
	}
}

// The hook command runs under `sh -c`; it must exec Hoxi when present and
// fall back to a passthrough allow when the binary is gone.
func TestAgyHookCommandShellBehavior(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "it's here")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "hoxi")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho \"ran $1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", agyHookCommand(exe)).Output()
	if err != nil || strings.TrimSpace(string(out)) != "ran agy-hook" {
		t.Fatalf("present binary: %q %v", out, err)
	}
	out, err = exec.Command("sh", "-c", agyHookCommand(filepath.Join(dir, "missing"))).Output()
	if err != nil || strings.TrimSpace(string(out)) != `{"decision":"allow"}` {
		t.Fatalf("missing binary: %q %v", out, err)
	}
}
