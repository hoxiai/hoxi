package agentcli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveWindowsNodeShimStandardNpmAndPnpm(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code", "cli.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodeDir := filepath.Join(dir, "nodejs")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nodeExe := filepath.Join(nodeDir, "node.exe")
	if err := os.WriteFile(nodeExe, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}

	npmShim := filepath.Join(dir, "claude.cmd")
	npmContent := "@ECHO off\r\n" +
		"GOTO start\r\n" +
		":find_dp0\r\n" +
		"SET dp0=%~dp0\r\n" +
		"EXIT /b\r\n" +
		":start\r\n" +
		"SETLOCAL\r\n" +
		"CALL :find_dp0\r\n" +
		"IF EXIST \"%dp0%\\node.exe\" (\r\n" +
		"  SET \"_prog=%dp0%\\node.exe\"\r\n" +
		") ELSE (\r\n" +
		"  SET \"_prog=node\"\r\n" +
		"  SET PATHEXT=%PATHEXT:;.JS;=;%\r\n" +
		")\r\n" +
		"endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  --no-warnings \"%dp0%\\node_modules\\@anthropic-ai\\claude-code\\cli.js\" %*\r\n"
	if err := os.WriteFile(npmShim, []byte(npmContent), 0o644); err != nil {
		t.Fatal(err)
	}

	gotExe, gotPrefix, ok := resolveWindowsNodeShimWith(
		npmShim,
		[]string{"Path=" + nodeDir + ";C:\\Windows\\System32"},
		';',
		isRegularFile,
		func(string) (string, error) { return "", errors.New("not found") },
	)
	if !ok {
		t.Fatalf("expected standard npm .cmd shim to unwrap")
	}
	if gotExe != nodeExe {
		t.Fatalf("execPath = %q, want %q", gotExe, nodeExe)
	}
	wantPrefix := []string{"--no-warnings", scriptPath}
	if !reflect.DeepEqual(gotPrefix, wantPrefix) {
		t.Fatalf("prefixArgs = %v, want %v", gotPrefix, wantPrefix)
	}

	// pnpm / older cmd-shim style with IF EXIST / ELSE branches and bundled local node.exe.
	pnpmDir := filepath.Join(dir, "pnpm-bin")
	pnpmScript := filepath.Join(pnpmDir, "dist", "codex.mjs")
	if err := os.MkdirAll(filepath.Dir(pnpmScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pnpmScript, []byte("export {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	localNode := filepath.Join(pnpmDir, "node.exe")
	if err := os.WriteFile(localNode, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	pnpmShim := filepath.Join(pnpmDir, "codex.cmd")
	pnpmContent := "@IF EXIST \"%~dp0\\node.exe\" (\r\n" +
		"  \"%~dp0\\node.exe\"  \"%~dp0\\dist\\codex.mjs\" %*\r\n" +
		") ELSE (\r\n" +
		"  @SETLOCAL\r\n" +
		"  @SET PATHEXT=%PATHEXT:;.JS;=;%\r\n" +
		"  node  \"%~dp0\\dist\\codex.mjs\" %*\r\n" +
		")\r\n"
	if err := os.WriteFile(pnpmShim, []byte(pnpmContent), 0o644); err != nil {
		t.Fatal(err)
	}
	gotExe, gotPrefix, ok = resolveWindowsNodeShimWith(
		pnpmShim,
		nil,
		';',
		isRegularFile,
		func(string) (string, error) { return "", errors.New("not found") },
	)
	if !ok || gotExe != localNode || !reflect.DeepEqual(gotPrefix, []string{pnpmScript}) {
		t.Fatalf("expected pnpm shim to resolve bundled local node.exe, got ok=%v exe=%q prefix=%v", ok, gotExe, gotPrefix)
	}

	// Bare command name lookup resolves `<name>.cmd` from PATH, and extensionless
	// npm bin scripts with a `#!/usr/bin/env node` shebang unwrap safely.
	bareDir := filepath.Join(dir, "bare-bin")
	extlessScript := filepath.Join(bareDir, "node_modules", "demo", "bin", "cli")
	if err := os.MkdirAll(filepath.Dir(extlessScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extlessScript, []byte("#!/usr/bin/env node\nconsole.log('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bareDir, "demo.cmd"), []byte("@echo off\r\nnode \"%~dp0\\node_modules\\demo\\bin\\cli\" %*\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gotExe, gotPrefix, ok = resolveWindowsNodeShimWith(
		"demo",
		[]string{"Path=" + bareDir + ";" + nodeDir},
		';',
		isRegularFile,
		func(string) (string, error) { return "", errors.New("not found") },
	)
	if !ok || gotExe != nodeExe || !reflect.DeepEqual(gotPrefix, []string{extlessScript}) {
		t.Fatalf("expected bare command + extensionless node bin to unwrap, got ok=%v exe=%q prefix=%v", ok, gotExe, gotPrefix)
	}
}

func TestResolveWindowsNodeShimFailsClosedOnCustomOrUnsafeWrappers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "cli.js")
	if err := os.WriteFile(scriptPath, []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	localNode := filepath.Join(dir, "node.exe")
	if err := os.WriteFile(localNode, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	nonNodeBin := filepath.Join(dir, "non_node_bin")
	if err := os.WriteFile(nonNodeBin, []byte("#!/usr/bin/env python3\nprint('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		file    string
		content string
	}{
		{
			name:    "non-cmd binary",
			file:    "claude.exe",
			content: "node \"%dp0%\\cli.js\" %*\r\n",
		},
		{
			name:    "custom env var in wrapper",
			file:    "custom_env.cmd",
			content: "@echo off\r\nSET CUSTOM_FLAG=1\r\nnode \"%dp0%\\cli.js\" %*\r\n",
		},
		{
			name:    "custom NODE_PATH in wrapper",
			file:    "custom_node_path.cmd",
			content: "@echo off\r\nSET NODE_PATH=%~dp0\\lib\r\nnode \"%dp0%\\cli.js\" %*\r\n",
		},
		{
			name:    "non-node executable",
			file:    "python_wrap.cmd",
			content: "@echo off\r\npython \"%dp0%\\cli.js\" %*\r\n",
		},
		{
			name:    "unsafe eval flag",
			file:    "eval_flag.cmd",
			content: "@echo off\r\nnode -e \"%dp0%\\cli.js\" %*\r\n",
		},
		{
			name:    "missing target script",
			file:    "missing_target.cmd",
			content: "@echo off\r\nnode \"%dp0%\\does-not-exist.js\" %*\r\n",
		},
		{
			name:    "mismatched branches",
			file:    "mismatch.cmd",
			content: "IF EXIST \"%~dp0\\node.exe\" (\r\n  \"%~dp0\\node.exe\" \"%~dp0\\cli.js\" %*\r\n) ELSE (\r\n  node --no-warnings \"%~dp0\\cli.js\" %*\r\n)\r\n",
		},
		{
			name:    "extensionless non-node script",
			file:    "non_node_script.cmd",
			content: "@echo off\r\nnode \"%~dp0\\non_node_bin\" %*\r\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.file)
			if err := os.WriteFile(p, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if exe, prefix, ok := resolveWindowsNodeShimWith(
				p,
				nil,
				';',
				isRegularFile,
				func(string) (string, error) { return "", errors.New("not found") },
			); ok {
				t.Fatalf("expected %s to fail closed, got exe=%q prefix=%v", tc.name, exe, prefix)
			}
		})
	}
}
