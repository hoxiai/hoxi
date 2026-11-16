package runner

import (
	"path/filepath"
	"testing"
)

func TestAutoModeReadOnlySyntaxEquivalence(t *testing.T) {
	worktree := t.TempDir()
	for _, command := range []string{
		`D=en; node -e "console.log('$D')"`,
		`export D=en; node -e "console.log('$D')"`,
		`P=data.json; python3 -c "import json; print(json.load(open('$P')))"`,
		`export P=data.json; python3 -c "import json; print(json.load(open('$P')))"`,
		`node --eval="console.log('shutdown')"`,
		`node --print="'shutdown'"`,
		`node -e"console.log('shutdown')"`,
		`node -p"'shutdown'"`,
		`node -pe "'shutdown'"`,
		`node -e "console.log('shutdown')" arg`,
		`node -e "console.log('shutdown')" -- --format foo`,
		`python3 -c"print('shutdown')" arg`,
		`node -e "const x={system:'darwin',write:1};console.log(x.system,x.write)"`,
		`node -e "const x={'system':'darwin',write:false};console.log(x)"`,
		`node -e "const x={system:'}'};console.log(JSON.stringify(x))"`,
		`git log --format='shutdown %h %s' -5`,
		`git log --pretty='rm -rf %h %s' -5`,
		`git show --format='shutdown %h' HEAD`,
		`git log --format='shutdown %h' --grep='rm -rf' -5`,
		`timeout 30s node -e "console.log('shutdown')"`,
		`gtimeout --signal=TERM --kill-after=1s 30s node --eval="console.log('shutdown')"`,
		`timeout -s TERM -k 1s -- 30s git log --format='shutdown %h' -5`,
	} {
		if !approvalAutoApproves(bashRequest(command), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, worktree) {
			t.Errorf("read-only command asked: %s", command)
		}
	}
}

func TestAutoModeReadOnlySyntaxKeepsEffectfulVariantsGated(t *testing.T) {
	worktree := t.TempDir()
	for _, command := range []string{
		`D=en; D=$(cat value.txt); node -e "console.log('$D')"`,
		`export D=en; export D=zh; node -e "console.log('$D')"`,
		`D=en; read D; node -e "console.log('$D')"`,
		`D='"'; node -e "console.log('$D')"`,
		`P=.env; python3 -c "print(open('$P').read())"`,
		`export P=.env; node -e "console.log(require('fs').readFileSync('$P','utf8'))"`,
		`node --eval="require('child_process').execSync('ls')"`,
		`node -p"require('fs').writeFileSync('out.json','x')"`,
		`node -e "console.log('shutdown')" --import=./preload.js`,
		`node -e "console.log('shutdown')" --require ./preload.js`,
		`node -e "const x={system:1};x.system()"`,
		`node -e "const x={system:1};x.system=other;console.log(x.system)"`,
		`node -e "const x={system:1};x['system']=other;console.log(x.system)"`,
		`node -e "const x={system:1};other(x);console.log(x.system)"`,
		`node -e "const x={system:1};function f(x){console.log(x.system)}"`,
		`node -e "const x={get system(){return other()}};console.log(x.system)"`,
		`node -e "const x={system:'.env'};console.log(x.system)"`,
		`timeout 30s node -e "require('child_process').execSync('ls')"`,
		`timeout --signal=TERM -k1s 30s node --eval="require('fs').writeFileSync('out.json','x')"`,
		`timeout $SECONDS node -e "console.log(1)"`,
		`timeout --unknown 30s node -e "console.log(1)"`,
		`git log --format='rm -rf .' | sh`,
		`git log --format="$(cat .env)"`,
	} {
		if approvalAutoApproves(bashRequest(command), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, worktree) {
			t.Errorf("effectful/unresolved command ran directly: %s", command)
		}
	}
}

func TestGitReadCommandsCannotWriteProtectedOutputInAnyMode(t *testing.T) {
	worktree := t.TempDir()
	for _, command := range []string{
		`git log --output=.git/config`,
		`git log --output '--format=../.git/config'`,
		`git show --output .hoxi/approval-policy.json HEAD`,
		`git diff --out=.vscode/settings.json`,
		`git --no-pager log --pretty --output=.git/config`,
		`git log --format --output=.git/config`,
		`git -C .git log --output=config`,
		`git -C.git log --output=config`,
		`git -C .hoxi/skills log --output=run.sh`,
		`cd .git && git log --output=config`,
		`timeout 10s git log --format='shutdown' --output=.git/config`,
		`git show --output=src/../.git/config HEAD`,
		`git log --output=.env`,
	} {
		if !isDestructiveCommand(command) {
			t.Errorf("protected Git output was not destructive: %s", command)
		}
		for _, dir := range []string{worktree, filepath.Join(worktree, ".hoxi", "worktrees", "ses_fixture")} {
			for _, mode := range []string{ApprovalModeAuto, ApprovalModeSmart, ApprovalModeTrusted} {
				if approvalAutoApproves(bashRequest(command), ApprovalPolicy{Mode: mode}, map[string]interface{}{"bash": "allow"}, dir) {
					t.Errorf("protected Git output ran in %s (%s): %s", mode, dir, command)
				}
			}
		}
	}
	for _, command := range []string{
		`git log --output=report.txt`,
		`git log --format='--output=.git/config shutdown'`,
		`git diff -- --output=.git/config`,
	} {
		if isDestructiveCommand(command) {
			t.Errorf("ordinary data/output path marked protected: %s", command)
		}
	}
}

func TestClaudeHookAllowsReadOnlyNodeEquivalentForms(t *testing.T) {
	f := newClaudeGateFixture(t)
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	for _, command := range []string{
		`export D=en; node -e "console.log('$D')"`,
		`node --eval="console.log('shutdown')"`,
		`node -e "const x={system:'darwin',write:1};console.log(x.system,x.write)"`,
		`git log --format='shutdown %h %s' -5`,
		`timeout 30s node -e "console.log('shutdown')"`,
	} {
		if got := f.decide(t, f.payload("Bash", command, "", "cc-uuid-1")); got != "allow|" {
			t.Errorf("read-only hook command asked: %q (%s)", got, command)
		}
	}
}
