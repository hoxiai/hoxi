package subproc

import (
	"os"
	"testing"
)

func TestResolveWindowsBashWithSkipsWSLStubAndPrefersGitBash(t *testing.T) {
	t.Parallel()
	gitBash := `C:\Program Files\Git\bin\bash.exe`
	got := ResolveWindowsBashWith(
		false,
		func(k string) string {
			if k == "ProgramFiles" {
				return `C:\Program Files`
			}
			return ""
		},
		func(p string) bool { return p == gitBash },
		func(name string) (string, error) {
			if name == "bash" {
				return `C:\Windows\System32\bash.exe`, nil
			}
			return "", os.ErrNotExist
		},
	)
	if got != gitBash {
		t.Fatalf("ResolveWindowsBashWith = %q, want %q", got, gitBash)
	}

	// WSL stubs must always be rejected.
	for _, stub := range []string{
		`C:\Windows\System32\bash.exe`,
		`C:\Windows\Sysnative\bash.exe`,
		`C:\Users\dev\AppData\Local\Microsoft\WindowsApps\bash.exe`,
	} {
		if !IsWindowsWSLStubBash(stub) {
			t.Fatalf("expected %q to be classified as WSL stub", stub)
		}
	}

	// allowShFallback=false ignores sh, allowShFallback=true falls back to sh.
	lookPath := func(name string) (string, error) {
		switch name {
		case "bash":
			return `C:\Windows\System32\bash.exe`, nil
		case "sh":
			return `C:\msys64\usr\bin\sh.exe`, nil
		default:
			return "", os.ErrNotExist
		}
	}
	if got := ResolveWindowsBashWith(false, func(string) string { return "" }, func(string) bool { return false }, lookPath); got != "" {
		t.Fatalf("expected empty when only WSL bash and sh exist with allowShFallback=false, got %q", got)
	}
	if got := ResolveWindowsBashWith(true, func(string) string { return "" }, func(string) bool { return false }, lookPath); got != `C:\msys64\usr\bin\sh.exe` {
		t.Fatalf("expected sh fallback with allowShFallback=true, got %q", got)
	}
}
