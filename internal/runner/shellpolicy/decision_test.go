package shellpolicy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDestructiveRequestsMatchSharedFixture(t *testing.T) {
	libDir := "testdata"
	if home, err := os.UserHomeDir(); err == nil {
		slashHome := strings.TrimRight(filepath.ToSlash(filepath.Clean(home)), "/")
		if len(slashHome) > 1 && slashHome != "/Users/u" && slashHome != "/home/u" {
			matches, _ := filepath.Glob(filepath.Join(libDir, "*.cases.json"))
			for _, f := range matches {
				if data, err := os.ReadFile(f); err == nil && strings.Contains(string(data), slashHome+"/") {
					t.Errorf("shared fixture %s hardcodes host home directory %q; use synthetic /Users/u/... instead", filepath.Base(f), slashHome)
				}
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(libDir, "permission-request.cases.json"))
	if err != nil {
		t.Fatalf("read shared fixture: %v", err)
	}
	var cases []struct {
		Name        string                 `json:"name"`
		Request     map[string]interface{} `json:"request"`
		Destructive bool                   `json:"destructive"`
		Trusted     *bool                  `json:"trusted"`
		HoxiHome    string                 `json:"hoxiHome"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse shared fixture: %v", err)
	}
	if len(cases) < 20 {
		t.Fatalf("fixture unexpectedly small: %d cases", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.HoxiHome != "" {
				t.Setenv("HOXI_HOME", c.HoxiHome)
			}
			if got := isDestructivePermissionRequest(c.Request); got != c.Destructive {
				t.Errorf("destructive = %v, want %v", got, c.Destructive)
			}
			if c.Trusted != nil {
				if got := approvalAutoApproves(c.Request, ApprovalPolicy{Mode: ApprovalModeTrusted}, nil, "/repo"); got != *c.Trusted {
					t.Errorf("trusted approval = %v, want %v", got, *c.Trusted)
				}
			}
		})
	}
}

func req(perm string, patterns ...string) map[string]interface{} {
	list := make([]interface{}, len(patterns))
	for i, p := range patterns {
		list[i] = p
	}
	return map[string]interface{}{"permission": perm, "patterns": list}
}

func toolReq(tool string, meta map[string]interface{}) map[string]interface{} {
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta["tool"] = tool
	return map[string]interface{}{"permission": "tool", "patterns": []interface{}{tool}, "metadata": meta}
}

func TestApprovalModesDecideEveryCategory(t *testing.T) {
	t.Parallel()
	auto := ApprovalPolicy{Mode: ApprovalModeAuto}
	smart := ApprovalPolicy{Mode: ApprovalModeSmart}
	manual := ApprovalPolicy{Mode: ApprovalModeManual}
	cases := []struct {
		name                string
		item                map[string]interface{}
		auto, smart, manual bool
	}{
		{"file read", req("read", "src/App.tsx"), true, true, true},
		{"dotenv read asks in every mode", req("read", "/repo/.env"), false, false, false},
		{"edit", req("edit", "src/App.tsx"), true, true, false},
		{"mutating command", req("bash", "mkdir -p dist && npm install"), true, false, false},
		{"read-only command", req("bash", "git status && go test ./..."), true, true, false},
		{"rg explicit pattern", req("bash", "rg -n --regexp=shutdown src"), true, true, false},
		{"git diff search text", req("bash", "git diff -G shutdown -- src"), true, true, false},
		{"rg explicit pattern with secret target", req("bash", "rg --regexp=needle .env"), false, false, false},
		{"destructive command", req("bash", "rm -rf dist"), false, false, false},
		{"force push", req("bash", "git push origin main --force"), false, false, false},
		{"branch deletion", req("bash", "git branch -D feature"), false, false, false},
		{"webfetch", req("webfetch", "https://example.com"), true, true, false},
		{"routine external directory", req("external_directory", "/Users/u/code/sibling/*"), true, true, false},
		{"routine external directory with command metadata", map[string]interface{}{"permission": "external_directory", "patterns": []interface{}{"/Users/u/code/sibling/*"}, "metadata": map[string]interface{}{"command": "cd /Users/u/code/sibling && node -e 'console.log(1)'"}}, true, true, false},
		{"credential external directory", req("external_directory", "/Users/u/.ssh/config"), false, false, false},
		{"todo control tool", req("todowrite"), true, true, false},
		{"claude edit tool", toolReq("Write", map[string]interface{}{"args": `{"file_path":"src/a.ts"}`}), true, true, false},
		{"claude edit without path", toolReq("Write", nil), false, false, false},
		{"agy web tool", toolReq("read_url_content", map[string]interface{}{"args": `{"Url":"https://example.com"}`}), true, true, false},
		{"outside read", toolReq("Read", map[string]interface{}{"externalDirectory": true, "args": `{"file_path":"/Users/u/code/sibling/a.ts"}`}), true, true, false},
		{"mcp tool", toolReq("call_mcp_tool", nil), true, false, false},
		{"unknown tool", toolReq("mystery_action", nil), true, false, false},
		{"protected edit", req("edit", ".git/config"), false, false, false},
		{"hoxi_tools discover", map[string]interface{}{"permission": "hoxi_tools", "patterns": []interface{}{"discover amazon"}, "metadata": map[string]interface{}{"tool": "hoxi_tools", "action": "discover"}}, true, true, true},
		{"hoxi_tools inspect", map[string]interface{}{"permission": "hoxi_tools", "patterns": []interface{}{"inspect apify"}, "metadata": map[string]interface{}{"tool": "hoxi_tools", "action": "inspect"}}, true, true, true},
		{"hoxi_tools run asks in every mode", map[string]interface{}{"permission": "hoxi_tools", "patterns": []interface{}{"run apify /actor"}, "metadata": map[string]interface{}{"tool": "hoxi_tools", "action": "run"}}, false, false, false},
	}
	for _, c := range cases {
		for _, m := range []struct {
			pol  ApprovalPolicy
			want bool
		}{{auto, c.auto}, {smart, c.smart}, {manual, c.manual}} {
			if got := approvalAutoApproves(c.item, m.pol, nil, ""); got != m.want {
				t.Errorf("%s in %s mode: got %v, want %v", c.name, m.pol.Mode, got, m.want)
			}
		}
	}
}

func TestApprovalOverridesReplaceTheModePreset(t *testing.T) {
	t.Parallel()
	pol := ApprovalPolicy{Mode: ApprovalModeManual, Edit: "allow"}
	if !approvalAutoApproves(req("edit", "src/a.ts"), pol, nil, "") {
		t.Error("edit=allow must apply in manual mode")
	}
	if approvalAutoApproves(req("bash", "ls"), pol, nil, "") {
		t.Error("manual mode keeps shell at ask when only edit is overridden")
	}
	pol = ApprovalPolicy{Mode: ApprovalModeAuto, Bash: "ask", Webfetch: "ask"}
	if approvalAutoApproves(req("bash", "ls"), pol, nil, "") || approvalAutoApproves(req("webfetch", "https://x.dev"), pol, nil, "") {
		t.Error("ask overrides must apply in auto mode")
	}
	if !approvalAutoApproves(req("edit", "src/a.ts"), pol, nil, "") {
		t.Error("auto mode edit must stay allowed")
	}
	pol = ApprovalPolicy{Mode: ApprovalModeAuto, Bash: "smart"}
	if approvalAutoApproves(req("bash", "npm install lodash"), pol, nil, "") || !approvalAutoApproves(req("bash", "git diff"), pol, nil, "") {
		t.Error("bash=smart must only allow read-only commands")
	}
}

func TestPermissionRuleObjectsOnlyDecideTheirOwnTargets(t *testing.T) {
	t.Parallel()
	auto := ApprovalPolicy{Mode: ApprovalModeAuto}
	smart := ApprovalPolicy{Mode: ApprovalModeSmart}
	manual := ApprovalPolicy{Mode: ApprovalModeManual}
	rules := map[string]interface{}{
		"bash":     map[string]interface{}{"npm install *": "allow", "docker *": "ask", "*": "ask"},
		"webfetch": map[string]interface{}{"https://docs.hoxi.ai/*": "allow", "*": "ask"},
		"edit":     "deny",
	}
	if !approvalAutoApproves(req("bash", "npm test"), auto, rules, "") {
		t.Error("an unmatched command must follow auto mode even when * is ask")
	}
	if !approvalAutoApproves(req("bash", "npm install lodash"), smart, rules, "") {
		t.Error("an allow pattern must let its command run in smart mode")
	}
	if !approvalAutoApproves(req("bash", "npm install lodash\necho done"), smart, rules, "") {
		t.Error("an allow pattern with * must also match multi-line commands")
	}
	if approvalAutoApproves(req("bash", "docker build ."), auto, rules, "") {
		t.Error("an ask pattern must ask in auto mode")
	}
	if approvalAutoApproves(req("bash", "npm install x && rm -rf /"), auto, rules, "") {
		t.Error("an allow pattern never covers a destructive command")
	}
	if !approvalAutoApproves(req("webfetch", "https://docs.hoxi.ai/guide"), manual, rules, "") {
		t.Error("allowed domain must run even in manual mode")
	}
	if approvalAutoApproves(req("webfetch", "https://untrusted.example.com/page"), manual, rules, "") {
		t.Error("unmatched domain must follow manual mode and ask")
	}
	if approvalAutoApproves(req("webfetch", "https://untrusted.example.com/page"), auto, map[string]interface{}{
		"webfetch": map[string]interface{}{"https://docs.hoxi.ai/*": "allow", "*": "deny"},
	}, "") {
		t.Error("a * deny fallback must block other domains in auto mode")
	}
	if approvalAutoApproves(req("edit", "src/a.ts"), auto, rules, "") {
		t.Error("edit=deny must never auto-approve")
	}
	if !approvalAutoApproves(req("edit", "src/a.ts"), auto, map[string]interface{}{"edit": "ask"}, "") {
		t.Error(`edit:"ask" in the OpenCode config must not override auto mode`)
	}
}

func TestSmartModeDropsLeadingWorktreeCD(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	smart := ApprovalPolicy{Mode: ApprovalModeSmart}
	if !approvalAutoApproves(req("bash", "cd "+worktree+" && git status"), smart, nil, worktree) {
		t.Error("cd into the worktree followed by a read-only command is routine")
	}
	if approvalAutoApproves(req("bash", "cd /tmp && git status"), smart, nil, worktree) {
		t.Error("cd outside the worktree must ask in smart mode")
	}
	taggedWrapped := "# hoxi-stop:ses_1:call_1\ntrap : EXIT\n'/usr/bin/sandbox-exec' -f '/tmp/p.sb' -- /bin/sh -c 'git status'"
	if !approvalAutoApproves(req("bash", taggedWrapped), smart, nil, worktree) {
		t.Error("sandbox-wrapped safe command with stop marker must auto-approve in smart mode")
	}
	item := map[string]interface{}{
		"permission": "bash",
		"patterns":   []interface{}{"# hoxi-stop:ses_1:call_1\ntrap : EXIT\nnpm install lodash"},
		"metadata":   map[string]interface{}{"command": "# hoxi-stop:ses_1:call_1\ntrap : EXIT\nnpm install lodash"},
	}
	sanitizeOpenCodePermissionItem(item)
	if got := stringList(item["patterns"]); len(got) != 1 || got[0] != "npm install lodash" {
		t.Errorf("sanitized patterns = %v, want [npm install lodash]", got)
	}
	if meta, _ := item["metadata"].(map[string]interface{}); meta["command"] != "npm install lodash" {
		t.Errorf("sanitized metadata.command = %v, want npm install lodash", meta["command"])
	}
	splitTrapItem := map[string]interface{}{
		"permission": "bash",
		"patterns":   []interface{}{openCodeStopTrapUSR1, "trap : EXIT", "cd " + worktree, "git status"},
		"metadata":   map[string]interface{}{"command": "# hoxi-stop:ses_1:call_1\n" + openCodeStopTrap + "\ncd " + worktree + " && git status"},
	}
	sanitizeOpenCodePermissionItem(splitTrapItem)
	if !approvalAutoApproves(splitTrapItem, smart, nil, worktree) {
		t.Errorf("split stop-trap patterns with leading worktree cd must auto-approve in smart mode: %+v", splitTrapItem)
	}
	truncatedWrapped := "'/usr/bin/sandbox-exec' -f '/Users/u/.hoxi/engine-data/sandbox/profiles/abc.sb' -- /bin/sh -c 'git status --short; echo \"---\"…"
	if got := unwrapSandboxCommand(truncatedWrapped); got != `git status --short; echo "---"…` {
		t.Errorf("unwrapSandboxCommand(truncated) = %q, want %q", got, `git status --short; echo "---"…`)
	}
}

func TestOpaqueShellCommandsRequireApprovalInAutoAndTrusted(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	// Explicit high-risk commands must ask in BOTH auto and trusted modes.
	for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
		for _, command := range []string{
			`P=.env; cat $P`,
			`cat "$KUBECONFIG"`,
			`echo "$OPENAI_API_KEY"`,
			`echo "${GITHUB_TOKEN:-fallback}"`,
			`BASH_ENV=/tmp/x.sh bash -c 'echo harmless'`,
			`python3 -c 'import pathlib; print(pathlib.Path(".env").read_text())'`,
			`curl -H "Authorization: Bearer $TOKEN" https://example.com`,
			`su - root -c id`,
			`doas id`,
			`npm publish`,
			`cargo publish`,
			`terraform apply`,
			`kubectl delete pod app`,
			`cat {.e,}nv`,
			`grep -r KEY --include={.env,a.go} .`,
			`{rm,-rf,.}`,
			`cd /etc && cat passwd`,
			`bash .git/hooks/pre-commit.sh`,
			`bash .hoxi/attachments/run.sh`,
		} {
			item := req("bash", command)
			if approvalAutoApproves(item, ApprovalPolicy{Mode: mode}, nil, worktree) {
				t.Errorf("high-risk shell %q was auto-approved in %s mode", command, mode)
			}
		}
	}
	// Additional explicit publishing/deploy commands gated by trusted mode's denylist.
	for _, command := range []string{
		`docker push registry.example.com/app:latest`,
		`bash -c "docker push $IMG"`,
		`eval "docker push img"`,
		`eval 'su - root'`,
		`echo img | xargs docker push`,
		`gh release create v1.0.0`,
		`gh pr merge 42`,
		`twine upload dist/*`,
		`gem push foo.gem`,
		`bun publish`,
		`helm install app ./chart`,
		`kubectl apply -f deploy.yaml`,
		`./scripts/ai-release.sh 1.0.0 "release"`,
	} {
		if approvalAutoApproves(req("bash", command), ApprovalPolicy{Mode: ApprovalModeTrusted}, nil, worktree) {
			t.Errorf("trusted denylist failed to block publish/deploy command %q", command)
		}
	}
	// Non-destructive opaque shell syntax stays gated in auto mode, while
	// trusted mode skips static opacity checks so routine bash runs unprompted.
	for _, command := range []string{
		`cat "$HOXI_AUDIT_PATH"`,
		`printf 'echo harmless' | sh`,
		`bash -c "$CMD"`,
		`for d in plugins/app/* themes/*; do if [ -e "$d/.git" ]; then st=$(git -C "$d" status -s 2>&1); code=$?; if [ $code -ne 0 ] || [ -n "$st" ]; then echo "=== $d (exit=$code) ==="; echo "$st"; fi; fi; done`,
		`for f in $(ls); do wc -l "$f"; done`,
		`echo "${FOO:-bar}"`,
		`while IFS= read -r line; do echo "$line"; done < list.txt`,
		`node -e 'require("fs").writeFileSync("a", "b")'`,
		"python3 - <<'EOF'\nimport os\nos.mkdir('x')\nEOF",
	} {
		if approvalAutoApproves(req("bash", command), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, worktree) {
			t.Errorf("opaque shell %q was auto-approved in auto mode", command)
		}
		if !approvalAutoApproves(req("bash", command), ApprovalPolicy{Mode: ApprovalModeTrusted}, nil, worktree) {
			t.Errorf("non-destructive shell %q asked in trusted mode", command)
		}
	}
	// Routine non-force push, quoted commit messages, JSON payloads and read-only
	// inline scripts run without asking in both auto and trusted modes.
	for _, command := range []string{
		`git push origin feat/my-branch`,
		`git commit -m 'fix: guard docker push and $KUBECONFIG in trusted mode'`,
		`curl -s -d '{"a":1,"b":2}' http://127.0.0.1:5400/health`,
		`node -e 'const obj = {a: 1, b: 2}; console.log(obj)'`,
		`node -e 'console.log(1)'`,
		"python3 - <<'EOF'\nprint(1)\nEOF",
	} {
		for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
			if !approvalAutoApproves(req("bash", command), ApprovalPolicy{Mode: mode}, nil, worktree) {
				t.Errorf("routine command %q asked in %s mode", command, mode)
			}
		}
	}
}
