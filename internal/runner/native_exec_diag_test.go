package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func writeFakeExecutable(t *testing.T, dir, name, scriptBody string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+scriptBody+"\n"), 0o755); err != nil {
		t.Fatalf("write fake executable %s: %v", name, err)
	}
	return p
}

// isolateFakeBinPath pins fakebin ahead of any host toolchain directories,
// including directories that agentcli.WithUserBinPath prepends when missing
// from PATH (e.g. ~/.cargo/bin or /opt/homebrew/bin in non-login shells).
func isolateFakeBinPath(t *testing.T, root, binDir string) {
	t.Helper()
	t.Setenv("HOME", root)
	t.Setenv("HOXI_HOME", filepath.Join(root, ".hoxi"))
	sep := string(os.PathListSeparator)
	pathVal := binDir
	if cur := os.Getenv("PATH"); cur != "" {
		pathVal += sep + cur
	}
	if runtime.GOOS != "windows" {
		pathVal += sep + "/opt/homebrew/bin" + sep + "/usr/local/bin"
	}
	t.Setenv("PATH", pathVal)
}

func TestNativePostEditDiagnosticsVuePHPDart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell shims used in test")
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nodeBinDir := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(nodeBinDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pubspec.yaml"), []byte("name: demo_app\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeFakeExecutable(t, nodeBinDir, "vue-tsc", `echo "src/App.vue(14,7): error TS2322: Type 'number' is not assignable to type 'string'."
exit 2`)
	writeFakeExecutable(t, binDir, "php", `if [ "$1" = "-l" ]; then
  echo "PHP Parse error:  syntax error, unexpected token '}' in $2 on line 9"
  exit 255
fi
exit 0`)
	writeFakeExecutable(t, binDir, "dart", `if [ "$1" = "analyze" ]; then
  echo "ERROR|COMPILE_TIME_ERROR|UNDEFINED_IDENTIFIER|$3|21|5|3|Undefined name 'foo'."
  exit 3
fi
exit 0`)

	isolateFakeBinPath(t, root, binDir)

	vueFile := filepath.Join(root, "src", "App.vue")
	phpFile := filepath.Join(root, "app", "OrderService.php")
	dartFile := filepath.Join(root, "lib", "main.dart")
	for _, f := range []string{vueFile, phpFile, dartFile} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("// stub\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(root, nil, nil, "")
	rec := SessionRecord{
		ID:           "ses_nat_diag_polyglot",
		Runtime:      string(RuntimeNative),
		ProjectDir:   root,
		WorktreePath: root,
		SandboxMode:  "off",
	}
	diagFn := svc.nativeDiagnostics(rec)

	vueOut := diagFn(context.Background(), root, vueFile)
	if !strings.Contains(vueOut, "[vue-tsc --noEmit]") || !strings.Contains(vueOut, "TS2322") {
		t.Fatalf("expected vue-tsc diagnostic output, got: %q", vueOut)
	}

	phpOut := diagFn(context.Background(), root, phpFile)
	if !strings.Contains(phpOut, "[php -l]") || !strings.Contains(phpOut, "PHP Parse error") {
		t.Fatalf("expected php -l diagnostic output, got: %q", phpOut)
	}

	dartOut := diagFn(context.Background(), root, dartFile)
	if !strings.Contains(dartOut, "[dart analyze]") || !strings.Contains(dartOut, "UNDEFINED_IDENTIFIER") {
		t.Fatalf("expected dart analyze diagnostic output, got: %q", dartOut)
	}
}

func TestNativePostEditDiagnosticsPythonAndStreamingCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell shims used in test")
	}

	root := t.TempDir()
	svc := NewService(root, nil, nil, "")
	rec := SessionRecord{
		ID:           "ses_nat_diag_py",
		Runtime:      string(RuntimeNative),
		ProjectDir:   root,
		WorktreePath: root,
		SandboxMode:  "off",
	}
	diagFn := svc.nativeDiagnostics(rec)

	// 1. Python ast.parse fallback catches syntax errors without writing __pycache__
	pyDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	badPy := filepath.Join(pyDir, "broken.py")
	if err := os.WriteFile(badPy, []byte("def broken(:\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if resolveWorkspacePythonBin(rec, pyDir, root, "python3") != "" || resolveWorkspacePythonBin(rec, pyDir, root, "python") != "" {
		pyOut := diagFn(context.Background(), root, badPy)
		if !strings.Contains(pyOut, "SyntaxError") {
			t.Fatalf("expected Python SyntaxError diagnostic, got: %q", pyOut)
		}
		if _, err := os.Stat(filepath.Join(pyDir, "__pycache__")); err == nil {
			t.Fatal("expected python -I -B check not to create __pycache__")
		}
	}

	// 2. Workspace .venv/bin/ruff takes priority when available
	venvBin := filepath.Join(root, ".venv", "bin")
	if err := os.MkdirAll(venvBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeExecutable(t, venvBin, "ruff", `echo "pkg/broken.py:1:12: F821 Undefined name 'missing_var'"
exit 1`)
	ruffOut := diagFn(context.Background(), root, badPy)
	if !strings.Contains(ruffOut, "[ruff check]") || !strings.Contains(ruffOut, "F821") {
		t.Fatalf("expected .venv ruff diagnostic, got: %q", ruffOut)
	}

	// 3. Streaming command runner emits live output and rejects outside working directory
	var progress []string
	var captured strings.Builder
	streamFn := svc.nativeStreamCommandRunner(rec, nil)
	out, code, err := streamFn(context.Background(), root, "printf 'hello_live_stream\\n'", &captured, func(s string) {
		progress = append(progress, s)
	})
	if err != nil || code != 0 || !strings.Contains(out, "hello_live_stream") || len(progress) == 0 || captured.String() != "hello_live_stream\n" {
		t.Fatalf("expected live progress from nativeStreamCommandRunner, got out=%q code=%d err=%v progress=%+v", out, code, err, progress)
	}
	outside := t.TempDir()
	if _, outCode, outErr := streamFn(context.Background(), outside, "echo nope", nil, nil); outErr == nil || outCode == 0 {
		t.Fatal("expected nativeStreamCommandRunner to reject working directory outside session worktree")
	}
}

func TestNativePostEditDiagnosticsRustOffline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell shims used in test")
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname = \"demo\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modeFile := filepath.Join(root, "cargo_mode.txt")
	if err := os.WriteFile(modeFile, []byte("compile_err"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFakeExecutable(t, binDir, "cargo", `if [ "$1" != "check" ] || [ "$2" != "--offline" ]; then
  echo "missing --offline flag: $@"
  exit 9
fi
mode=$(cat "`+modeFile+`")
if [ "$mode" = "offline_miss" ]; then
  printf '%s\n' 'error: failed to get `+"`serde`"+` as a dependency of package `+"`demo`"+` because --offline was specified'
  exit 101
fi
echo "src/lib.rs:4:12: error[E0308]: mismatched types"
exit 101`)
	isolateFakeBinPath(t, root, binDir)

	rsFile := filepath.Join(root, "src", "lib.rs")
	if err := os.MkdirAll(filepath.Dir(rsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rsFile, []byte("pub fn bad() -> i32 { \"nope\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(root, nil, nil, "")
	rec := SessionRecord{
		ID:           "ses_nat_diag_rust",
		Runtime:      string(RuntimeNative),
		ProjectDir:   root,
		WorktreePath: root,
		SandboxMode:  "off",
	}
	diagFn := svc.nativeDiagnostics(rec)

	got := diagFn(context.Background(), root, rsFile)
	if !strings.Contains(got, "[cargo check]") || !strings.Contains(got, "E0308") {
		t.Fatalf("expected cargo check --offline E0308 diagnostic, got: %q", got)
	}

	if err := os.WriteFile(modeFile, []byte("offline_miss"), 0o644); err != nil {
		t.Fatal(err)
	}
	if noisy := diagFn(context.Background(), root, rsFile); noisy != "Not completed: cargo dependencies unavailable offline." {
		t.Fatalf("expected unavailable offline dependencies to be reported, got: %q", noisy)
	}
}

func TestNativeLimitedWriterAdvancesLiveTailPastCapAndRetainsEndFailure(t *testing.T) {
	var liveSnapshots []string
	w := &nativeLimitedWriter{
		limit: nativeCmdMaxOutputBytes,
		onProgress: func(s string) {
			liveSnapshots = append(liveSnapshots, s)
		},
	}

	headMarker := "HEAD_BUILD_BANNER_001\n"
	_, _ = w.Write([]byte(headMarker))

	// Write > 200 KiB across chunks, resetting lastEmit to simulate elapsed wall-clock intervals.
	chunk := strings.Repeat("compiling_module_progress_中文🚀_line_padding_0123456789\n", 64)
	for i := 0; i < 65; i++ {
		w.lastEmit = time.Time{}
		_, _ = w.Write([]byte(chunk))
	}

	// Write a final failure marker well past the 128 KiB capture cap.
	tailFailure := "FAIL\tgithub.com/demo/pkg\npanic: FATAL_END_REASON_98765\n"
	w.lastEmit = time.Time{}
	_, _ = w.Write([]byte(tailFailure))

	if len(liveSnapshots) < 3 {
		t.Fatalf("expected multiple live progress snapshots, got %d", len(liveSnapshots))
	}
	lastLive := liveSnapshots[len(liveSnapshots)-1]
	if !strings.Contains(lastLive, "FATAL_END_REASON_98765") {
		t.Fatalf("expected live progress tail to advance past 128 KiB cap and surface final failure, got:\n%s", lastLive)
	}
	for i, snap := range liveSnapshots {
		if !utf8.ValidString(snap) {
			t.Fatalf("live snapshot #%d contains invalid UTF-8", i)
		}
	}

	finalOut := w.String()
	if !utf8.ValidString(finalOut) {
		t.Fatal("final captured command output contains invalid UTF-8")
	}
	if !strings.Contains(finalOut, "HEAD_BUILD_BANNER_001") {
		t.Fatalf("expected final output to retain head banner, got len=%d", len(finalOut))
	}
	if !strings.Contains(finalOut, "FATAL_END_REASON_98765") {
		t.Fatalf("expected final output to retain end-of-stream failure past 128 KiB, got len=%d", len(finalOut))
	}
	if !strings.Contains(finalOut, "bytes omitted") {
		t.Fatalf("expected explicit omitted-bytes notice on >128 KiB output")
	}
}

func TestCodeIndexAndBashReadOnlyCallsRemainRetryableOnStreamInterrupt(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(root, "sessions.json"))

	if isMutatingRunnerTool("load_host_tool", nil) {
		t.Fatal("schema discovery must be retryable on interrupt")
	}
	for _, act := range []string{"context", "search", "inspect", "callers", "callees", "impact", "refresh"} {
		if isMutatingRunnerTool("hoxi_code_index", map[string]interface{}{"action": act}) {
			t.Fatalf("hoxi_code_index action=%q must not be classified as mutating", act)
		}
	}
	if isMutatingRunnerTool("bash", map[string]interface{}{"action": "output", "procId": "bg_1"}) {
		t.Fatal("bash action=output must not be classified as mutating")
	}
	if isMutatingRunnerTool("bash", map[string]interface{}{"action": "list"}) {
		t.Fatal("bash action=list must not be classified as mutating")
	}
	if !isMutatingRunnerTool("bash", map[string]interface{}{"command": "go build ./..."}) {
		t.Fatal("bash command execution must remain classified as mutating")
	}

	rec := SessionRecord{
		ID:           "ses_ci_retry_check",
		Runtime:      string(RuntimeNative),
		ProjectDir:   root,
		WorktreePath: root,
		Turns: []MessageTurn{
			{
				Info: MessageInfo{ID: "msg_a_ci", SessionID: "ses_ci_retry_check", Role: "assistant"},
				Parts: []MessagePart{
					{
						ID:     "prt_ci_1",
						Type:   "tool",
						Tool:   "hoxi_code_index",
						CallID: "call_ci_1",
						State: &ToolState{
							Status: "completed",
							Input:  map[string]interface{}{"action": "context", "symbol": "RunTurn"},
							Output: "engine.go:159",
						},
					},
				},
			},
		},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	finish, msgErr := svc.classifyNativeTurnFailure(rec.ID, "msg_a_ci", errors.New("unexpected EOF"), nil)
	if finish != TurnFinishInterrupted || msgErr == nil || msgErr.Data == nil {
		t.Fatalf("unexpected classifyNativeTurnFailure result: finish=%q err=%+v", finish, msgErr)
	}
	if !msgErr.Data.IsRetryable || msgErr.Data.Kind == ToolOutcomeUnknown {
		t.Fatalf("expected turn that only ran hoxi_code_index to remain retryable after EOF, got %+v", msgErr.Data)
	}
}

func TestNativeDiagnosticsReportsUnavailableAndCooldown(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "sample.go")
	if err := os.WriteFile(file, []byte("package sample"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewService(root, nil, nil, "")
	rec := SessionRecord{ID: "ses_diag_" + filepath.Base(root), WorktreePath: root}
	diag := svc.nativeDiagnostics(rec)
	if got := diag(context.Background(), root, file); !strings.HasPrefix(got, "Not run:") {
		t.Fatalf("Go file without a go.mod: %q", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := diag(ctx, root, file); !strings.Contains(got, "cancelled") {
		t.Fatalf("cancelled check: %q", got)
	}
	noteDiagToolBudgetExceeded(rec.ID, ".go")
	t.Cleanup(func() {
		diagCooldownMu.Lock()
		delete(diagToolCooldown, rec.ID+"\x00.go")
		diagCooldownMu.Unlock()
	})
	if got := diag(context.Background(), root, file); !strings.Contains(got, "cooling down") {
		t.Fatalf("check during cooldown: %q", got)
	}
}

func TestNativeToolLookupAndSubprocessEnvCaseInsensitiveKeys(t *testing.T) {
	binDir := t.TempDir()
	toolName := "hoxi-case-probe-bin"
	var toolPath string
	if runtime.GOOS == "windows" {
		toolPath = filepath.Join(binDir, toolName+".cmd")
		if err := os.WriteFile(toolPath, []byte("@exit /b 0\r\n"), 0o644); err != nil {
			t.Fatalf("write fake windows executable: %v", err)
		}
	} else {
		toolPath = writeFakeExecutable(t, binDir, toolName, "exit 0")
	}

	// Mixed-case `Path=` (Windows convention) must be discovered by resolveNativeToolBinaryFromEnv.
	got := resolveNativeToolBinaryFromEnv([]string{"Path=" + binDir}, toolName)
	if got != toolPath {
		t.Fatalf("resolveNativeToolBinaryFromEnv(Path=...) = %q, want %q", got, toolPath)
	}

	// Mixed-case interactive prompt env vars must be stripped case-insensitively.
	t.Setenv("Git_AskPass", "/bin/false")
	t.Setenv("ssh_askpass", "/bin/false")
	t.Setenv("GIT_EDITOR", "vim")
	cleaned := nativeSubprocessEnv(SessionRecord{Runtime: string(RuntimeNative)})
	for _, kv := range cleaned {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "GIT_ASKPASS", "SSH_ASKPASS", "GIT_EDITOR":
			t.Fatalf("expected %q stripped from nativeSubprocessEnv, got %v", kv, cleaned)
		}
	}
}

func TestNativePostEditDiagnosticsJSONAndJS(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root, nil, nil, "")
	rec := SessionRecord{
		ID:           "ses_nat_diag_json_js",
		Runtime:      string(RuntimeNative),
		ProjectDir:   root,
		WorktreePath: root,
		SandboxMode:  "off",
	}
	diagFn := svc.nativeDiagnostics(rec)

	// 1. Strict JSON: valid passes, broken reports exact line:col.
	goodJSON := filepath.Join(root, "package.json")
	if err := os.WriteFile(goodJSON, []byte("{\n  \"name\": \"demo\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := diagFn(context.Background(), root, goodJSON); got != "Passed: json syntax." {
		t.Fatalf("expected valid package.json to pass, got: %q", got)
	}

	badJSON := filepath.Join(root, "broken.json")
	if err := os.WriteFile(badJSON, []byte("{\n  \"a\": 1,\n  \"b\": }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := diagFn(context.Background(), root, badJSON); !strings.Contains(got, "[json]") || !strings.Contains(got, "broken.json:3:") {
		t.Fatalf("expected broken.json line 3 syntax diagnostic, got: %q", got)
	}

	// 2. JSONC config files (tsconfig.json, hoxi.json) allow comments and trailing commas,
	//    while still flagging real syntax errors and unterminated block comments.
	goodTSConfig := filepath.Join(root, "tsconfig.json")
	if err := os.WriteFile(goodTSConfig, []byte("{\n  // strict mode\n  \"compilerOptions\": {\n    \"strict\": true,\n  },\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := diagFn(context.Background(), root, goodTSConfig); got != "Passed: jsonc syntax." {
		t.Fatalf("expected valid JSONC tsconfig.json to pass, got: %q", got)
	}

	badHoxiJSON := filepath.Join(root, "hoxi.json")
	if err := os.WriteFile(badHoxiJSON, []byte("{\n  /* unterminated comment\n  \"skills\": []\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := diagFn(context.Background(), root, badHoxiJSON); !strings.Contains(got, "[jsonc]") || !strings.Contains(got, "hoxi.json:2:3") {
		t.Fatalf("expected unterminated block comment at hoxi.json:2:3, got: %q", got)
	}

	// 3. JavaScript (.js/.mjs) via node --check and JSX via workspace oxlint.
	if runtime.GOOS == "windows" {
		return
	}
	binDir := filepath.Join(root, "fakebin")
	nodeBinDir := filepath.Join(root, "node_modules", ".bin")
	for _, d := range []string{binDir, nodeBinDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFakeExecutable(t, binDir, "node", `if [ "$1" = "--check" ]; then
  if grep -q "BROKEN_SYNTAX" "$2"; then
    echo "$2:2"
    echo "SyntaxError: Unexpected token '}'"
    exit 1
  fi
  exit 0
fi
exit 0`)
	writeFakeExecutable(t, nodeBinDir, "oxlint", `if grep -q "BAD_JSX" "$2"; then
  echo "App.jsx:1:10: error: Expected corresponding JSX closing tag"
  exit 1
fi
exit 0`)
	isolateFakeBinPath(t, root, binDir)

	goodMJS := filepath.Join(root, "script.mjs")
	badJS := filepath.Join(root, "bad.js")
	badJSX := filepath.Join(root, "App.jsx")
	if err := os.WriteFile(goodMJS, []byte("export const x = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badJS, []byte("function f() {\n  BROKEN_SYNTAX }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badJSX, []byte("export const App = () => <div>BAD_JSX</span>;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := svc.RunNativeDiagnostics(context.Background(), rec.ID, root, goodMJS); got != "Passed: node --check." {
		t.Fatalf("expected good.mjs to pass node --check, got: %q", got)
	}
	if got := diagFn(context.Background(), root, badJS); !strings.Contains(got, "[node --check]") || !strings.Contains(got, "SyntaxError") {
		t.Fatalf("expected bad.js node --check failure, got: %q", got)
	}
	if got := diagFn(context.Background(), root, badJSX); !strings.Contains(got, "[oxlint]") || !strings.Contains(got, "JSX") {
		t.Fatalf("expected bad.jsx oxlint failure, got: %q", got)
	}
}
