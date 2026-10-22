// Package execstub gives tests fake executables without writing new
// executable files.
//
// On macOS the first exec of a freshly written file is scanned by
// XProtect/Gatekeeper. Under a loaded `go test ./...` those scans queue up, so
// a one-line `#!/bin/sh` stub could take several seconds to start. A stub made
// here is a symlink to the test binary that is already running, plus a
// sidecar script that /bin/sh reads as data. Neither needs a new scan.
//
// A package that uses Write must call Main first in its TestMain.
package execstub

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

const sidecarSuffix = ".execstub.sh"

// Main runs the stub's script when the test binary was started through a
// stub symlink, and does not return in that case. Otherwise it returns at once.
func Main() {
	script := sidecarFor(os.Args[0])
	if script == "" {
		return
	}
	if _, err := os.Stat(script); err != nil {
		return
	}
	argv := append([]string{"/bin/sh", script}, os.Args[1:]...)
	// Exec (not a child process) keeps the PID, exit status and signal
	// behaviour of a real script.
	if err := syscall.Exec("/bin/sh", argv, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "execstub:", err)
		os.Exit(127)
	}
}

// Write creates dir/name. Running it runs script with /bin/sh and passes the
// stub's arguments as $1, $2, ...; $0 is the sidecar path, not the stub path.
func Write(t testing.TB, dir, name, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("execstub needs /bin/sh")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("execstub: resolve test binary: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(sidecarFor(path), []byte(script), 0o644); err != nil {
		t.Fatalf("execstub: write script: %v", err)
	}
	if err := os.Symlink(exe, path); err != nil {
		t.Fatalf("execstub: link stub: %v", err)
	}
	return path
}

// sidecarFor maps a stub path (as seen in argv[0]) to its script.
func sidecarFor(argv0 string) string {
	if argv0 == "" {
		return ""
	}
	if !strings.ContainsRune(argv0, filepath.Separator) {
		resolved, err := exec.LookPath(argv0)
		if err != nil {
			return ""
		}
		argv0 = resolved
	}
	abs, err := filepath.Abs(argv0)
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(abs), "."+filepath.Base(abs)+sidecarSuffix)
}
