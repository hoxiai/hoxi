package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLiteralNarrowing(t *testing.T) {
	allow := []string{
		"D=\"/var/folders/ab/T/opencode\"\nrm -rf \"$D/stage\"\npython3 \"$D/stage.py\"",
		"B=/tmp/backup\nfor f in a.go b.go; do mkdir -p \"$B/$(dirname \"$f\")\"; cp \"$f\" \"$B/$f\"; done",
		"TREE=$(git write-tree) || exit 1\necho \"staged tree: $TREE\"",
		"ps -p 90591 -o pid,ppid,command && ps -p $(ps -p 90591 -o ppid=) -o pid,ppid,command",
		"ls \"$(git rev-parse --git-dir)/hooks\" | grep -v sample",
		"(cd web && npx tsc --noEmit 2>&1 | tail -5); npm --prefix web run check:i18n",
		"for p in 5410 5420; do lsof -nP -iTCP:$p -sTCP:LISTEN; done",
		"nohup node node_modules/.bin/vite --port 3777 > /tmp/vite.log 2>&1 & echo \"started pid $!\"; sleep 8; tail -30 /tmp/vite.log",
		"X=$(git rev-parse HEAD); echo $X",
	}
	for _, cmd := range allow {
		if isOpaqueShellCommand(cmd, "") {
			t.Errorf("still opaque: %q", cmd)
		}
	}
	deny := []string{
		"D=~/.ssh; cat \"$D/id_ed25519\"",
		"F=.env; cat \"$F\"",
		"for f in .env a.go; do cat \"$f\"; done",
		"D=/tmp/x; D=$HOME; rm -rf \"$D\"",
		"X=rm; $X -rf build",
		"D=${HOME:-/}; ls \"$D\"",
		"D=/tmp/x; read D; rm -rf \"$D\"",
		"for f in *.go; do rm \"$f\"; done",
		"cat $(cat paths.txt)",
		"cp $(git rev-parse --show-toplevel)/a /tmp",
		"$(git rev-parse HEAD) --help",
		"(cd / && rm -rf tmp)",
		"(cd .. && cat .env)",
		"( ( ( ls ) ) )",
		"kill $!",
		"set -f",
		"set -euo pipefail",
	}
	for _, cmd := range deny {
		if !isOpaqueShellCommand(cmd, "") {
			t.Errorf("became clear: %q", cmd)
		}
	}
	// Over the expansion cap, a read-only loop keeps the old allow and a
	// writing loop keeps the old ask. Nothing is silently truncated.
	items := make([]string, 0, 65)
	for i := 0; i < 65; i++ {
		items = append(items, "f"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	list := stringsJoin(items)
	if isOpaqueShellCommand("for f in "+list+"; do cat \"$f\"; done", "") {
		t.Error("65-item read-only loop became opaque")
	}
	if !isOpaqueShellCommand("for f in "+list+"; do rm \"$f\"; done", "") {
		t.Error("65-item rm loop was expanded and cleared")
	}
	sleepKill := "sleep 1 & kill %1"
	if isOpaqueShellCommand(sleepKill, "") {
		t.Errorf("background kill without a dollar became opaque: %q", sleepKill)
	}
	if !isDestructiveCommand(sleepKill) {
		t.Errorf("kill %%1 was not destructive")
	}
	if !isDestructiveCommand("D=~/.ssh; cat \"$D/id_ed25519\"") {
		t.Error("substituted ssh key read was not destructive")
	}
	if !isDestructiveCommand("F=.env; cat \"$F\"") {
		t.Error("substituted dotenv read was not destructive")
	}
	push := "D=/repo; git push --force \"$D\""
	if isOpaqueShellCommand(push, "") {
		t.Error("substituted git push stayed opaque; it should be judged as the expanded push")
	}
	if !isDestructiveCommand(push) || approvalAutoApproves(req("bash", push), ApprovalPolicy{Mode: ApprovalModeTrusted}, nil, "") {
		t.Error("substituted git push was allowed")
	}
	auto := ApprovalPolicy{Mode: ApprovalModeAuto}
	trusted := ApprovalPolicy{Mode: ApprovalModeTrusted}
	temp := "D=\"/var/folders/ab/T/opencode\"\nrm -rf \"$D/stage\"\npython3 \"$D/stage.py\""
	if approvalAutoApproves(req("bash", temp), auto, nil, "") {
		t.Error("auto approved a substituted temp rm")
	}
	if !approvalAutoApproves(req("bash", temp), trusted, nil, "") {
		t.Error("trusted asked for a substituted temp rm")
	}
}

func stringsJoin(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += " "
		}
		out += item
	}
	return out
}

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
