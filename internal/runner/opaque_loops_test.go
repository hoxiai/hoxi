package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOpaqueCommandsMatchSharedFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".opencode", "lib", "opaque-command.cases.json"))
	if err != nil {
		t.Fatalf("read shared fixture: %v", err)
	}
	var cases []struct {
		Command       string `json:"command"`
		Opaque        bool   `json:"opaque"`
		TrustedOpaque *bool  `json:"trustedOpaque"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse shared fixture: %v", err)
	}
	if len(cases) < 40 {
		t.Fatalf("fixture unexpectedly small: %d cases", len(cases))
	}
	for _, c := range cases {
		if got := isOpaqueShellCommand(c.Command, ""); got != c.Opaque {
			t.Errorf("opaque(%q) = %v, want %v", c.Command, got, c.Opaque)
		}
		want := c.Opaque
		if c.TrustedOpaque != nil {
			want = *c.TrustedOpaque
		}
		if got := isOpaqueShellCommandWith(c.Command, "", true); got != want {
			t.Errorf("trusted opaque(%q) = %v, want %v", c.Command, got, want)
		}
	}
}

// The reported command must run without a prompt in auto and trusted mode,
// while the narrowing never lets a secret read or a destructive loop through.
func TestReadOnlyLoopsRunInAutoAndTrustedMode(t *testing.T) {
	worktree := t.TempDir()
	loop := `rg -l "watch\(" admin/src/views/design/ai-decor/ | while read f; do if ! rg -q "watch" <(rg "^import .* from 'vue'" "$f"); then echo "MISSING: $f"; fi; done`
	for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
		if !approvalAutoApproves(req("bash", loop), ApprovalPolicy{Mode: mode}, nil, worktree) {
			t.Errorf("read-only loop asked in %s mode", mode)
		}
		for _, cmd := range []string{
			`rg -l x src | while read f; do rm -rf "$f"; done`,
			`for f in .env; do cat "$f"; done`,
			`cat <(cat .env)`,
			`rg -l KEY --hidden . | while read f; do cat "$f"; done`,
		} {
			if approvalAutoApproves(req("bash", cmd), ApprovalPolicy{Mode: mode}, nil, worktree) {
				t.Errorf("%q was auto-approved in %s mode", cmd, mode)
			}
		}
	}
}

// The shape OpenCode really sends: the whole command in metadata.command and
// its own split in patterns. Judging `echo "MISSING: $f"` alone lost the
// binding `while read f`, so the read-only loop kept asking in trusted mode.
func TestOpenCodeSplitPatternsAreJudgedAsOneCommand(t *testing.T) {
	cmd := `rg -l "createSignal" web/src/components/timeline | while read f; do if ! rg -q "createSignal" <(rg "^import .* from .solid-js." "$f"); then echo "MISSING: $f"; fi; done; echo loop-done`
	item := map[string]interface{}{
		"permission": "bash",
		"metadata":   map[string]interface{}{"command": cmd},
		"patterns": []interface{}{"trap : EXIT", `rg -l "createSignal" web/src/components/timeline`, "read f",
			`rg -q "createSignal" <(rg "^import .* from .solid-js." "$f")`, `rg "^import .* from .solid-js." "$f"`,
			`echo "MISSING: $f"`, "echo loop-done"},
	}
	for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
		if !approvalAutoApproves(item, ApprovalPolicy{Mode: mode}, nil, t.TempDir()) {
			t.Errorf("OpenCode-shaped read-only loop asked in %s mode", mode)
		}
	}
	// The whole command still decides: an unbound variable in it keeps asking
	// even when every split piece looks harmless.
	item["metadata"] = map[string]interface{}{"command": `cat "$KUBECONFIG"`}
	item["patterns"] = []interface{}{"echo hi"}
	if approvalAutoApproves(item, ApprovalPolicy{Mode: ApprovalModeTrusted}, nil, t.TempDir()) {
		t.Error("an opaque whole command must ask even if its patterns look harmless")
	}
	// Danger in any split piece still asks.
	item["metadata"] = map[string]interface{}{"command": "echo ok"}
	item["patterns"] = []interface{}{"echo ok", "cat .env"}
	if approvalAutoApproves(item, ApprovalPolicy{Mode: ApprovalModeTrusted}, nil, t.TempDir()) {
		t.Error("a secret read in the patterns must still ask")
	}
}
