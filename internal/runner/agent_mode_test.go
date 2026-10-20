package runner

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestReadOnlyModesMapOntoEveryRuntimePlanPolicy(t *testing.T) {
	for mode, want := range map[string]bool{
		"ask": true, " Ask ": true, "plan": true, "build": false, "debug": false, "": false,
	} {
		if got := isReadOnlyMode(mode); got != want {
			t.Fatalf("isReadOnlyMode(%q)=%v want %v", mode, got, want)
		}
	}

	claude := strings.Join(BuildClaudeArgs("ask", "", "", ""), " ")
	if !strings.Contains(claude, "--permission-mode plan") {
		t.Fatalf("claude ask must use plan permission mode, got %s", claude)
	}
	agy := strings.Join(BuildAgyArgs("ask", "", "", ""), " ")
	if !strings.Contains(agy, "--mode plan") {
		t.Fatalf("agy ask must use plan mode, got %s", agy)
	}
	codebuddy := strings.Join(BuildCodeBuddyArgs("ask", "", "", ""), " ")
	if !strings.Contains(codebuddy, "--permission-mode plan") {
		t.Fatalf("codebuddy ask must use plan permission mode, got %s", codebuddy)
	}
	qoder := strings.Join(BuildQoderArgs("ask", "", "", ""), " ")
	if !strings.Contains(qoder, "--disallowed-tools Edit,Write,MultiEdit") {
		t.Fatalf("qoder ask must disallow write tools, got %s", qoder)
	}

	build := strings.Join(BuildClaudeArgs("build", "", "", ""), " ")
	if strings.Contains(build, "--permission-mode plan") {
		t.Fatalf("build must keep accept-edits, got %s", build)
	}
}

func TestAskModeGetsNoClaudeHook(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.SetHookBaseURL("http://127.0.0.1:1")
	if args, env, revoke := svc.issueClaudeHook("ask", "ses_cc_ask", "msg"); args != nil || env != nil {
		revoke()
		t.Fatalf("ask mode keeps Claude's read-only policy and must get no hook, got %v %v", args, env)
	}
}
