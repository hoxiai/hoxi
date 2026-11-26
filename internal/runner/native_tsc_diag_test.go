package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/sandbox"
)

func TestNativeTSCDiagnosticIncrementalAndFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell stub test")
	}

	root := t.TempDir()
	workspace := filepath.Join(root, "web")
	binDir := filepath.Join(workspace, "node_modules", ".bin")
	srcDir := filepath.Join(workspace, "src")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tsFile := filepath.Join(srcDir, "App.tsx")
	if err := os.WriteFile(tsFile, []byte("export const n: number = 'bad';\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Case 1: Modern tsc accepts --incremental --tsBuildInfoFile outside workspace
	// (leaving workspace clean of .tsbuildinfo files) and writes cache in os.TempDir().
	tscStub := filepath.Join(binDir, "tsc")
	modernScript := `#!/bin/sh
prev=""
buildinfo=""
for arg in "$@"; do
  if [ "$prev" = "--tsBuildInfoFile" ]; then
    buildinfo="$arg"
  fi
  prev="$arg"
done
if [ -z "$buildinfo" ]; then
  echo "missing --tsBuildInfoFile" >&2
  exit 2
fi
echo '{"version":"5.0"}' > "$buildinfo"
echo "src/App.tsx(1,14): error TS2322: Type 'string' is not assignable to type 'number'."
exit 1
`
	if err := os.WriteFile(tscStub, []byte(modernScript), 0o755); err != nil {
		t.Fatal(err)
	}

	svc := NewService(workspace, nil, nil, filepath.Join(root, "sessions.json"))
	rec := SessionRecord{
		ID:             "ses_tsc_inc",
		Runtime:        string(RuntimeNative),
		ProjectDir:     root,
		WorktreePath:   root,
		CommandSandbox: sandbox.Available(),
	}
	if rec.CommandSandbox {
		probeOut, probeCode, probeErr := svc.nativeCommandRunner(rec, nil)(context.Background(), workspace, "true")
		if probeErr != nil && probeCode == 71 && strings.Contains(probeOut, "sandbox_apply: Operation not permitted") {
			rec.CommandSandbox = false
		}
	}

	diagFn := svc.nativeDiagnostics(rec)
	got := diagFn(context.Background(), root, tsFile)
	if !strings.Contains(got, "[tsc --noEmit --project web/tsconfig.json]") || !strings.Contains(got, "TS2322") {
		t.Fatalf("expected incremental tsc diagnostic output, got:\n%s", got)
	}
	buildInfoPath := nativeTSCBuildInfoPath(rec.ID, filepath.Join(workspace, "tsconfig.json"))
	if _, err := os.Stat(buildInfoPath); err != nil {
		t.Fatalf("expected tsbuildinfo cache file to be written at %q: %v", buildInfoPath, err)
	}
	_ = os.Remove(buildInfoPath)
	if strings.HasPrefix(buildInfoPath, workspace) {
		t.Fatalf("expected tsbuildinfo cache outside workspace %q, got %q", workspace, buildInfoPath)
	}

	// Case 2: Legacy TS (<4.0) rejecting --incremental + --noEmit with TS5053 falls back cleanly.
	legacyScript := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "--incremental" ]; then
    echo "error TS5053: Option 'incremental' cannot be specified with option 'noEmit'."
    exit 1
  fi
done
echo "src/App.tsx(1,14): error TS2322: Fallback check succeeded."
exit 1
`
	if err := os.WriteFile(tscStub, []byte(legacyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	fallbackGot := diagFn(context.Background(), root, tsFile)
	if strings.Contains(fallbackGot, "TS5053") || !strings.Contains(fallbackGot, "Fallback check succeeded.") {
		t.Fatalf("expected automatic fallback without --incremental, got:\n%s", fallbackGot)
	}
	writeFakeExecutable(t, binDir, "tsc", `test "$1" = "--project" && test "$2" -ef "$PWD/tsconfig.json"`)
	if got := diagFn(context.Background(), root, tsFile); got != "Passed: tsc --noEmit --project web/tsconfig.json" {
		t.Fatalf("expected the checked nested tsconfig, got %q", got)
	}

}
