package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type policyCase struct {
	Command     string `json:"command"`
	Decision    string `json:"decision"`
	Destructive bool   `json:"destructive"`
}

// The same fixture drives web/src/__tests__/permissions.test.ts, so the Go
// port and .opencode/lib/permission-policy.js cannot drift apart silently.
func TestPermissionPolicyMatchesSharedFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".opencode", "lib", "permission-policy.cases.json"))
	if err != nil {
		t.Fatalf("read shared fixture: %v", err)
	}
	var cases []policyCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse shared fixture: %v", err)
	}
	if len(cases) < 20 {
		t.Fatalf("fixture unexpectedly small: %d cases", len(cases))
	}
	for _, c := range cases {
		if got := decideCommandPermission(c.Command); got != c.Decision {
			t.Errorf("decide(%q) = %q, want %q", c.Command, got, c.Decision)
		}
		if got := isDestructiveCommand(c.Command); got != c.Destructive {
			t.Errorf("destructive(%q) = %v, want %v", c.Command, got, c.Destructive)
		}
	}
}

func TestSplitCommandChainMatchesJSSeparators(t *testing.T) {
	cases := map[string][]string{
		"git status && git diff": {"git status", "git diff"},
		"a || b; c\nd":           {"a", "b", "c", "d"},
		"a | b":                  {"a", "b"},
		"go test ./... 2>&1":     {"go test ./... 2>&1"},
		"cmd &> out.log":         {"cmd &> out.log"},
		"a & b":                  {"a", "b"},
		"echo $(date)":           {"echo", "date"},
		"echo `date`":            {"echo", "date"},
		"(cd web && npm test)":   {"(cd web", "npm test"},
		"   ":                    nil,
	}
	for in, want := range cases {
		if got := splitCommandChain(in); !reflect.DeepEqual(got, want) {
			t.Errorf("split(%q) = %#v, want %#v", in, got, want)
		}
	}
}

func TestDecideCommandPermissionInNormalizesRunnerCommands(t *testing.T) {
	worktree := t.TempDir()
	cases := []struct {
		command string
		want    string
	}{
		{"cd " + filepath.Join(worktree, "go-fast") + " && /usr/local/go/bin/go test ./... -count=1 | tail -80", "allow"},
		{"cd " + worktree + " && /usr/local/go/bin/go vet ./...", "allow"},
		{"/opt/homebrew/bin/npm run build", "allow"},
		{"cd " + worktree + " && /usr/local/go/bin/go test ./... 2>&1", "allow"},
		{"cd /tmp && go test ./...", "ask"},
		{"/tmp/not-a-toolchain/go test ./...", "ask"},
		{"cd " + worktree + " && rm -rf build", "ask"},
		{"cd " + worktree + " && /usr/local/go/bin/go test ./... && cat /etc/hosts", "allow"},
	}
	for _, c := range cases {
		if got := decideCommandPermissionIn(c.command, worktree); got != c.want {
			t.Errorf("decideIn(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}

func TestNormalizeCommandForPolicyKeepsOutsideCD(t *testing.T) {
	worktree := t.TempDir()
	in := "cd /tmp && go test ./..."
	if got := normalizeCommandForPolicy(in, worktree); got != in {
		t.Fatalf("normalize(%q) = %q, want unchanged", in, got)
	}
}
