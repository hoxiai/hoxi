package execstub

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	Main()
	os.Exit(m.Run())
}

func TestStubRunsScriptWithArguments(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "it's here")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := Write(t, dir, "tool", "echo \"ran $1 $2\"\nexit 3\n")
	out, err := exec.Command(stub, "a", "b c").Output()
	if strings.TrimSpace(string(out)) != "ran a b c" {
		t.Fatalf("stub output = %q", out)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 3 {
		t.Fatalf("stub exit = %v, want status 3", err)
	}
}

func TestStubIsFoundThroughPATH(t *testing.T) {
	dir := t.TempDir()
	Write(t, dir, "pathtool", "echo found\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := exec.Command("pathtool").Output()
	if err != nil || strings.TrimSpace(string(out)) != "found" {
		t.Fatalf("stub via PATH = %q, %v", out, err)
	}
}

func TestSidecarForIgnoresPlainBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sidecarFor(exe)); !os.IsNotExist(err) {
		t.Fatalf("the test binary itself must not look like a stub, stat err=%v", err)
	}
}
