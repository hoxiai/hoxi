package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFakeExecutable(t *testing.T, dir, name, scriptBody string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+scriptBody+"\n"), 0o755); err != nil {
		t.Fatalf("write fake executable %s: %v", name, err)
	}
	return p
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

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

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
