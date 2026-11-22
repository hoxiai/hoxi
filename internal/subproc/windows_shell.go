package subproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IsWindowsWSLStubBash reports whether p points to Windows' built-in WSL bash
// shim rather than a native Git Bash / MSYS2 shell. Launching the WSL stub
// fails when WSL is not installed or runs inside a Linux filesystem view.
func IsWindowsWSLStubBash(p string) bool {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), "/", `\`))
	return strings.HasSuffix(norm, `\system32\bash.exe`) ||
		strings.HasSuffix(norm, `\sysnative\bash.exe`) ||
		strings.Contains(norm, `\microsoft\windowsapps\bash.exe`)
}

// ResolveWindowsBash locates a native POSIX shell on Windows (preferring Git
// Bash / Scoop Git Bash and skipping WSL stubs). When allowShFallback is true,
// `sh.exe` and `LookPath("sh")` are also considered.
func ResolveWindowsBash(allowShFallback bool) string {
	exists := func(p string) bool {
		st, err := os.Stat(filepath.FromSlash(p))
		return err == nil && !st.IsDir()
	}
	return ResolveWindowsBashWith(allowShFallback, os.Getenv, exists, exec.LookPath)
}

// ResolveWindowsBashWith is the dependency-injected core of ResolveWindowsBash
// so Windows shell discovery can be tested deterministically on any OS.
func ResolveWindowsBashWith(
	allowShFallback bool,
	getenv func(string) string,
	fileExists func(string) bool,
	lookPath func(string) (string, error),
) string {
	for _, k := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		root := strings.TrimSpace(getenv(k))
		if root == "" {
			continue
		}
		candidates := []string{
			root + `\Git\bin\bash.exe`,
			root + `\Git\usr\bin\bash.exe`,
			root + `\Programs\Git\bin\bash.exe`,
		}
		if allowShFallback {
			candidates = append(candidates,
				root+`\Git\bin\sh.exe`,
				root+`\Git\usr\bin\sh.exe`,
				root+`\Programs\Git\bin\sh.exe`,
			)
		}
		for _, cand := range candidates {
			if fileExists(cand) {
				return cand
			}
		}
	}
	if up := strings.TrimSpace(getenv("USERPROFILE")); up != "" {
		cand := up + `\scoop\apps\git\current\bin\bash.exe`
		if fileExists(cand) {
			return cand
		}
	}
	if lookPath != nil {
		names := []string{"bash"}
		if allowShFallback {
			names = append(names, "sh")
		}
		for _, bin := range names {
			if p, err := lookPath(bin); err == nil && p != "" && !IsWindowsWSLStubBash(p) {
				return p
			}
		}
	}
	return ""
}
