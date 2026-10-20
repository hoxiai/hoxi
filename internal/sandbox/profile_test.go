package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runCommand(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &commandError{err: err, output: string(out)}
	}
	return nil
}

type commandError struct {
	err    error
	output string
}

func (e *commandError) Error() string { return e.err.Error() + ": " + e.output }

func TestProfileTextConfinesWritesAndNetwork(t *testing.T) {
	workspace := t.TempDir()
	profile, err := DefaultProfile(workspace, NetworkDeny)
	if err != nil {
		t.Fatal(err)
	}
	text, err := ProfileText(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"(deny default)",
		"(allow file-read*)",
		"(deny network-outbound",
		"(allow file-write*",
		workspace,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("profile missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "(allow network-outbound)\n") {
		t.Fatalf("deny mode must not allow outbound network:\n%s", text)
	}
}

func TestProfileTextDeniesSensitiveSubdirectories(t *testing.T) {
	profile, err := DefaultProfile(t.TempDir(), NetworkDeny)
	if err != nil {
		t.Fatal(err)
	}
	text, err := ProfileText(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{DefaultRoot(), filepath.Join(HoxiHome(), "bin")} {
		if !strings.Contains(text, dir) {
			t.Fatalf("profile does not mention %s:\n%s", dir, text)
		}
	}
	if !strings.Contains(text, "(require-not (require-any (literal") {
		t.Fatalf("deny rules missing from profile:\n%s", text)
	}
}

func TestProfileTextAllowsNetworkWhenRequested(t *testing.T) {
	profile, err := DefaultProfile(t.TempDir(), NetworkAllow)
	if err != nil {
		t.Fatal(err)
	}
	text, err := ProfileText(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "(allow network-outbound)") {
		t.Fatalf("allow mode missing outbound network:\n%s", text)
	}
}

func TestProfileTextRejectsControlCharacters(t *testing.T) {
	profile := Profile{
		Filesystem: FilesystemProfile{AllowWrite: []PathRule{{Path: "/tmp/\nbad", Kind: PathSubtree}}},
		Network:    NetworkProfile{Mode: NetworkDeny},
	}
	if _, err := ProfileText(profile); err == nil {
		t.Fatal("expected control characters to be rejected")
	}
}

func TestDefaultProfileRequiresWorkspace(t *testing.T) {
	if _, err := DefaultProfile("", NetworkDeny); err == nil {
		t.Fatal("expected an empty workspace to fail")
	}
	if _, err := DefaultProfile(filepath.Join(t.TempDir(), "missing"), NetworkDeny); err == nil {
		t.Fatal("expected a missing workspace to fail")
	}
}

func TestWrapExecKeepsArguments(t *testing.T) {
	profile, err := DefaultProfile(t.TempDir(), NetworkDeny)
	if err != nil {
		t.Fatal(err)
	}
	bin, args, err := WrapExec(profile, "/bin/echo", []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if bin != sandboxExec {
		t.Fatalf("bin = %q", bin)
	}
	if len(args) < 4 || args[0] != "-p" || args[len(args)-3] != "--" || args[len(args)-2] != "/bin/echo" || args[len(args)-1] != "hello" {
		t.Fatalf("unexpected wrapped args: %#v", args)
	}
}

func TestEnforcedWriteStaysInsideWorkspace(t *testing.T) {
	if !Available() {
		t.Skip("seatbelt backend unavailable")
	}
	workspace := t.TempDir()
	profile, err := DefaultProfile(workspace, NetworkDeny)
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(workspace, "inside.txt")
	home, _ := os.UserHomeDir()
	outside := filepath.Join(home, "hoxi-sandbox-outside-test.txt")
	_ = os.Remove(outside)
	bin, args, err := WrapShell(profile, "echo ok > "+shellQuote(inside))
	if err != nil {
		t.Fatal(err)
	}
	if err := runCommand(bin, args...); err != nil {
		t.Fatalf("workspace write was blocked: %v", err)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Fatalf("workspace write was blocked: %v", err)
	}
	bin, args, err = WrapShell(profile, "echo bad > "+shellQuote(outside))
	if err != nil {
		t.Fatal(err)
	}
	if err := runCommand(bin, args...); err == nil {
		_ = os.Remove(outside)
		t.Fatal("write outside the workspace was allowed")
	}
	if _, err := os.Stat(outside); err == nil {
		_ = os.Remove(outside)
		t.Fatal("outside file was created")
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
