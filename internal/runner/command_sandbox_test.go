package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/sandbox"
)

func TestConfineRuntimeCommandDisabled(t *testing.T) {
	svc := &Service{}
	bin, args, err := svc.confineRuntimeCommand(SessionRecord{WorktreePath: t.TempDir()}, "/bin/echo", []string{"hi"})
	if err != nil {
		t.Fatal(err)
	}
	if bin != "/bin/echo" || len(args) != 1 || args[0] != "hi" {
		t.Fatalf("disabled sandbox must not wrap: %s %#v", bin, args)
	}
}

func TestConfineRuntimeCommandWrapsWorkspace(t *testing.T) {
	if !sandbox.Available() {
		t.Skip("seatbelt backend unavailable")
	}
	workspace := t.TempDir()
	project := filepath.Dir(workspace)
	svc := &Service{}
	bin, args, err := svc.confineRuntimeCommand(
		SessionRecord{WorktreePath: workspace, ProjectDir: project, CommandSandbox: true},
		"/bin/echo",
		[]string{"hi"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bin != "/usr/bin/sandbox-exec" {
		t.Fatalf("expected seatbelt binary, got %q", bin)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, workspace) {
		t.Fatalf("profile must include the workspace: %s", joined)
	}
	if !strings.Contains(joined, "(deny default)") {
		t.Fatalf("profile must deny by default: %s", joined)
	}
	if args[len(args)-2] != "/bin/echo" || args[len(args)-1] != "hi" {
		t.Fatalf("wrapped command lost its arguments: %#v", args)
	}
}

func TestReadableGitDir(t *testing.T) {
	project := t.TempDir()
	if got := writableGitDir(project); got != "" {
		t.Fatalf("expected no git dir, got %q", got)
	}
	git := filepath.Join(project, ".git")
	if err := os.Mkdir(git, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := writableGitDir(project); got != git {
		t.Fatalf("expected %q, got %q", git, got)
	}
}
