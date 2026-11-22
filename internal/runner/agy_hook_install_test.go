package runner

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agyhook"
	"github.com/hoxiai/hoxi/internal/testutil/execstub"
)

func setTestHookExecutable(fn func() (string, error)) {
	agyHookMu.Lock()
	hookExecutable = fn
	agyHookMu.Unlock()
}

func withHookPaths(t *testing.T, exe string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config", "hooks.json")
	agyHookMu.Lock()
	prevPath, prevExe := agyHooksPathFor, hookExecutable
	agyHooksPathFor = func() string { return path }
	hookExecutable = func() (string, error) { return exe, nil }
	agyHookMu.Unlock()
	t.Cleanup(func() {
		agyHookMu.Lock()
		agyHooksPathFor, hookExecutable = prevPath, prevExe
		agyHookMu.Unlock()
	})
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
	if err := json.Unmarshal(doc[agyHookName], &entry); err != nil || len(entry.PreToolUse) != 1 {
		t.Fatalf("unexpected hook entry: %s (%v)", doc[agyHookName], err)
	}
	if entry.PreToolUse[0].Matcher != "*" || len(entry.PreToolUse[0].Hooks) != 1 {
		t.Fatalf("matcher = %+v, want wildcard", entry.PreToolUse[0])
	}
	h := entry.PreToolUse[0].Hooks[0]
	if h.Type != "command" || h.Timeout != 600 ||
		h.Command != agyHookCommand("/Applications/Hoxi.app/Contents/MacOS/hoxi") {
		t.Fatalf("unexpected hook entry: %+v", entry)
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), `\u0026`) {
		t.Fatalf("hooks.json should keep && readable: %s", raw)
	}
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode changed to %v", fi.Mode().Perm())
	}

	before, _ := os.ReadFile(path)
	if _, err := InstallAgyApprovalHook(); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatalf("idempotent install rewrote the file")
	}

	setTestHookExecutable(func() (string, error) { return "/Applications/Hoxi 2.app/Contents/MacOS/hoxi", nil })
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

// The hook command runs under `sh -c`; it must short-circuit in shell when
// HOXI_RUNNER_SESSION_ID is unset (even when the binary is gone), exec Hoxi or
// its per-turn / installed fallbacks when inside a Hoxi session, and fail
// closed (`deny`) when inside a Hoxi session with every binary missing.
func TestAgyHookCommandShellBehavior(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh wrapper test")
	}
	dir := filepath.Join(t.TempDir(), "it's here")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := execstub.Write(t, dir, "hoxi", "echo \"ran primary $1\"\n")
	envExe := execstub.Write(t, dir, "hoxi-env", "echo \"ran env $1\"\n")
	fallbackExe := execstub.Write(t, dir, "hoxi-fallback", "echo \"ran fallback $1\"\n")
	missingExe := filepath.Join(dir, "missing")

	// Outside Hoxi (no HOXI_RUNNER_SESSION_ID): shell fast-path without spawning Hoxi,
	// even when every binary is uninstalled.
	cmdUnset := exec.Command("sh", "-c", agyHookCommandWithFallbacks("darwin", missingExe, nil))
	cmdUnset.Env = append(os.Environ(), "HOXI_RUNNER_SESSION_ID=", "HOXI_HOOK_BIN=")
	out, err := cmdUnset.Output()
	if err != nil || strings.TrimSpace(string(out)) != `{"decision":"allow"}` {
		t.Fatalf("unset session fast-path: %q %v", out, err)
	}

	// Inside a Hoxi runner session: execs the primary Hoxi binary.
	cmdSet := exec.Command("sh", "-c", agyHookCommandWithFallbacks("darwin", exe, []string{fallbackExe}))
	cmdSet.Env = append(os.Environ(), "HOXI_RUNNER_SESSION_ID=sess-1", "HOXI_HOOK_BIN="+envExe)
	out, err = cmdSet.Output()
	if err != nil || strings.TrimSpace(string(out)) != "ran primary agy-hook" {
		t.Fatalf("present binary: %q %v", out, err)
	}

	// Stale primary in hooks.json falls back to per-turn HOXI_HOOK_BIN.
	cmdEnvFallback := exec.Command("sh", "-c", agyHookCommandWithFallbacks("darwin", missingExe, []string{fallbackExe}))
	cmdEnvFallback.Env = append(os.Environ(), "HOXI_RUNNER_SESSION_ID=sess-1", "HOXI_HOOK_BIN="+envExe)
	out, err = cmdEnvFallback.Output()
	if err != nil || strings.TrimSpace(string(out)) != "ran env agy-hook" {
		t.Fatalf("HOXI_HOOK_BIN fallback: %q %v", out, err)
	}

	// Mid-turn deleted build/bin/Hoxi.app falls back to installed /Applications bundle.
	cmdAppFallback := exec.Command("sh", "-c", agyHookCommandWithFallbacks("darwin", missingExe, []string{fallbackExe}))
	cmdAppFallback.Env = append(os.Environ(), "HOXI_RUNNER_SESSION_ID=sess-1", "HOXI_HOOK_BIN="+missingExe)
	out, err = cmdAppFallback.Output()
	if err != nil || strings.TrimSpace(string(out)) != "ran fallback agy-hook" {
		t.Fatalf("installed app fallback: %q %v", out, err)
	}

	// Inside a Hoxi session with ALL binaries missing: fail-closed deny (never allow).
	cmdMissing := exec.Command("sh", "-c", agyHookCommandWithFallbacks("darwin", missingExe, nil))
	cmdMissing.Env = append(os.Environ(), "HOXI_RUNNER_SESSION_ID=sess-1", "HOXI_HOOK_BIN=")
	out, err = cmdMissing.Output()
	if err != nil {
		t.Fatalf("missing binary exit error: %v (%q)", err, out)
	}
	var d agyhook.Decision
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatalf("missing binary output %q is not valid JSON: %v", out, err)
	}
	if d.Decision != "deny" || d.Reason != agyhook.MissingBinaryReason {
		t.Fatalf("missing binary inside Hoxi session must fail closed, got %+v", d)
	}
}

func TestAgyHookCommandForWindowsUsesCmdIfExist(t *testing.T) {
	winCmd := agyHookCommandForOS("windows", `C:\Program Files\Hoxi\Hoxi.exe`)
	denyRaw := `{"decision":"deny","reason":"` + agyhook.MissingBinaryReason + `"}`
	want := `if not defined HOXI_RUNNER_SESSION_ID (echo {"decision":"allow"}) else if exist "C:\Program Files\Hoxi\Hoxi.exe" ("C:\Program Files\Hoxi\Hoxi.exe" agy-hook) else if defined HOXI_HOOK_BIN (if exist "%HOXI_HOOK_BIN%" ("%HOXI_HOOK_BIN%" agy-hook) else (echo ` + denyRaw + `)) else (echo ` + denyRaw + `)`
	if winCmd != want {
		t.Fatalf("agyHookCommandForOS(windows) = %q, want %q", winCmd, want)
	}
	darwinProd := agyHookCommandForOS("darwin", "/Applications/Hoxi.app/Contents/MacOS/Hoxi")
	if !strings.HasPrefix(darwinProd, `if [ -z "$HOXI_RUNNER_SESSION_ID" ]; then echo '{"decision":"allow"}'; elif [ -x '/Applications/Hoxi.app/Contents/MacOS/Hoxi' ]; then exec `) {
		t.Fatalf("agyHookCommandForOS(darwin prod) = %q", darwinProd)
	}
	if strings.Count(darwinProd, "/Applications/Hoxi.app/Contents/MacOS/Hoxi") != 2 {
		t.Fatalf("prod path should not duplicate fallback branch: %q", darwinProd)
	}
	darwinBuild := agyHookCommandForOS("darwin", "/Users/dev/hoxi/build/bin/Hoxi.app/Contents/MacOS/Hoxi")
	if !strings.Contains(darwinBuild, "elif [ -x '/Applications/Hoxi.app/Contents/MacOS/Hoxi' ]; then exec '/Applications/Hoxi.app/Contents/MacOS/Hoxi' agy-hook") {
		t.Fatalf("non-Applications darwin path should include /Applications fallback: %q", darwinBuild)
	}
}

func TestResolveHookExecutableHandlesMovedAndUnlinkedAppBundles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable bit test")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeStub := func(dir, name string) string {
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			t.Fatal(mkErr)
		}
		p := filepath.Join(dir, name)
		if wErr := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); wErr != nil {
			t.Fatal(wErr)
		}
		return p
	}
	movedExe := writeStub(filepath.Join(root, "Desktop", "Hoxi.app", "Contents", "MacOS"), "Hoxi")
	installedProd := writeStub(filepath.Join(root, "Applications", "Hoxi.app", "Contents", "MacOS"), "Hoxi")
	installedDev := writeStub(filepath.Join(root, "Applications", "Hoxi Dev.app", "Contents", "MacOS"), "Hoxi")
	staleBuild := filepath.Join(root, "build", "bin", "Hoxi.app", "Contents", "MacOS", "Hoxi")
	staleDevBuild := filepath.Join(root, "build", "bin", "Hoxi Dev.app", "Contents", "MacOS", "Hoxi")

	// 1. Live proc_pidpath (Finder move on same volume) wins over stale os.Executable().
	got, err := resolveHookExecutable("darwin", movedExe, staleBuild, []string{installedProd})
	if err != nil || got != movedExe {
		t.Fatalf("moved live path: got %q (%v), want %q", got, err, movedExe)
	}

	// 2. Unlinked build/bin/Hoxi.app (proc_pidpath empty, os.Executable missing) falls back to installed bundle.
	got, err = resolveHookExecutable("darwin", "", staleBuild, []string{installedProd, installedDev})
	if err != nil || got != installedProd {
		t.Fatalf("unlinked prod fallback: got %q (%v), want %q", got, err, installedProd)
	}
	fallbacksDev := fallbackHookExecutables("darwin", staleDevBuild, root, "", "")
	var tempOnlyFallbacks []string
	for _, fb := range fallbacksDev {
		if strings.HasPrefix(fb, root) {
			tempOnlyFallbacks = append(tempOnlyFallbacks, fb)
		}
	}
	got, err = resolveHookExecutable("darwin", "", staleDevBuild, tempOnlyFallbacks)
	if err != nil || got != installedDev {
		t.Fatalf("unlinked dev bundle should prefer Hoxi Dev.app: got %q (%v), want %q", got, err, installedDev)
	}

	// 3. Reject non-executable files, directories, and dangling symlinks; fail with error when nothing exists.
	nonExec := filepath.Join(root, "not-executable")
	if err := os.WriteFile(nonExec, []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(root, "dangling-link")
	if err := os.Symlink(staleBuild, dangling); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveHookExecutable("darwin", dangling, nonExec, []string{root, staleBuild}); err == nil {
		t.Fatal("expected error when every candidate is missing, a directory, non-executable, or a dangling symlink")
	}
}

func TestMissingExecutableReportsErrorAndStillAllowsUninstall(t *testing.T) {
	path := withHookPaths(t, "/Users/dev/build/bin/Hoxi.app/Contents/MacOS/Hoxi")
	if st, err := InstallAgyApprovalHook(); err != nil || !st.Current {
		t.Fatalf("initial install: %+v %v", st, err)
	}

	// Simulate the binary disappearing with no fallback installed anywhere.
	setTestHookExecutable(func() (string, error) {
		return "", errors.New("Hoxi executable no longer exists on disk")
	})
	st := AgyApprovalHookStatus()
	if st.Path != path || !st.Installed || st.Current || st.Error == "" {
		t.Fatalf("missing binary must report installed=true, current=false, error!=empty; got %+v", st)
	}
	if _, err := InstallAgyApprovalHook(); err == nil {
		t.Fatal("expected InstallAgyApprovalHook to fail when executable is missing")
	}

	// User can still cleanly uninstall Hoxi's entry from hooks.json.
	after, err := UninstallAgyApprovalHook()
	if err != nil || after.Installed {
		t.Fatalf("uninstall with missing executable failed: %+v %v", after, err)
	}
}

func TestLiveProcessExecutablePathCurrentProcess(t *testing.T) {
	live := liveProcessExecutablePath()
	if live == "" {
		return
	}
	if resolved, ok := usableExecutableForOS(runtime.GOOS, live); !ok || resolved == "" {
		t.Fatalf("liveProcessExecutablePath() = %q was rejected by usableExecutableForOS", live)
	}
}
