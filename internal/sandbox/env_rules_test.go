package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxDeniesNestedDotEnvReads(t *testing.T) {
	if !Available() {
		t.Skip("seatbelt backend unavailable")
	}
	workspace := t.TempDir()
	write := func(rel string) string {
		path := filepath.Join(workspace, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	denied := []string{
		write(".env"),
		write("web/.env.local"),
		write("apay/.env"),
		write("plugins/app/one/.env.production"),
		write("a/b/c/d/.env.development"),
	}
	allowed := []string{
		write("apay/.env.example"),
		write("apay/readme.env"),
		write("apay/.envrc.sample"),
		write("tools/.env/bin/activate"),
	}
	profile, err := DefaultProfile(workspace, NetworkDeny)
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string) error {
		bin, args, err := WrapShell(profile, "head -c1 "+shellQuote(path)+" >/dev/null")
		if err != nil {
			t.Fatal(err)
		}
		return runCommand(bin, args...)
	}
	for _, path := range denied {
		if err := read(path); err == nil {
			t.Errorf("sandboxed read of %s succeeded", path)
		}
	}
	for _, path := range allowed {
		if err := read(path); err != nil {
			t.Errorf("sandboxed read of %s failed: %v", path, err)
		}
	}
}

func TestWorktreeEnvRulesFallBackForUnquotablePaths(t *testing.T) {
	rules := worktreeEnvRules(`/work/a"b`)
	if len(rules) == 0 {
		t.Fatal("no .env rules for a workspace with a quote in its path")
	}
	for _, rule := range rules {
		if rule.Kind != PathLiteral {
			t.Fatalf("unquotable workspace got a %s rule: %q", rule.Kind, rule.Path)
		}
	}
	if got := worktreeEnvRules("/work/repo"); len(got) != 1 || got[0].Kind != PathRegex {
		t.Fatalf("worktreeEnvRules(/work/repo) = %+v, want one regex rule", got)
	}
}
