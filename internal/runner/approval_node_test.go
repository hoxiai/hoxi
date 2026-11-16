package runner

import "testing"

const localeNodeCountCommand = `cd admin/src/locale/lang && for d in en zh zh-HK; do echo -n "$d: "; node -e "const fs=require('fs'),p=require('path');let n=0;for(const f of fs.readdirSync('$d')){if(f.endsWith('.json')){const j=JSON.parse(fs.readFileSync(p.join('$d',f),'utf8'));const c=o=>{for(const v of Object.values(o)){if(v&&typeof v==='object'&&!Array.isArray(v))c(v);else n++}};c(j)}}console.log(n)"; done`

func TestNodeCommandsFollowCapabilitiesRatherThanInterpreterName(t *testing.T) {
	worktree := t.TempDir()
	auto := ApprovalPolicy{Mode: ApprovalModeAuto}
	for _, command := range []string{
		`node scripts/check.js`,
		`node --check src/index.js`,
		`node --test scripts/check.test.js`,
		`node --version`,
		`node -p 'JSON.stringify({ok:true})'`,
		`nodejs -e 'console.log(1)'`,
		`node20 -e 'console.log(1)'`,
		`node -e "const fs=require('fs'),p=require('path');console.log(fs.readFileSync(p.join('en','app.json'),'utf8'))"`,
		`node -e "const fs=require('fs');const p=require('node:path');const f=p.join('en','app.json');console.log(fs.readFileSync(f,'utf8'))"`,
		`for d in en zh zh-HK; do node -e "let n=0;for(const x of [1,2])n++;console.log('$d',n)"; done`,
		`for d in en zh; do for f in app common; do node -e "const fs=require('fs'),p=require('path');console.log(fs.readFileSync(p.join('$d','$f.json'),'utf8'))"; done; done`,
	} {
		if !approvalAutoApproves(bashRequest(command), auto, nil, worktree) {
			t.Errorf("routine Node command asked: %s", command)
		}
	}
	for _, command := range []string{
		`for d in en zh; do node -e "require('fs').writeFileSync('$d/out.json','x')"; done`,
		`for d in en zh; do node -e "require('child_process').execSync('ls')"; done`,
		`for d in en zh; do node -e "console.log(process.env)"; done`,
		`for d in en zh; do node -e "fetch('https://example.com/'+ '$d')"; done`,
		`for d in en .env; do node -e "console.log(require('fs').readFileSync('$d','utf8'))"; done`,
		`for d in $DIRS; do node -e "console.log('$d')"; done`,
		`for d in en zh; do node -e "console.log('$UNKNOWN')"; done`,
		`for d in en zh; do d=other; node -e "console.log('$d')"; done`,
		`for d in en zh; do d+=other; node -e "console.log('$d')"; done`,
		`for d in en zh; do d[0]=other; node -e "console.log('$d')"; done`,
		`for d in en zh; do d=$(cat code.txt); node -e "console.log('$d')"; done`,
		`for d in en zh; do for d in other; do node -e "console.log('$d')"; done; done`,
		`node -e "const fs=require('fs'),p=require('path');console.log(fs.readFileSync(p.join('en','../.env'),'utf8'))"`,
		`node -e "const fs=require('fs');let p=require('path');p=other;console.log(fs.readFileSync(p.join('en','app.json'),'utf8'))"`,
		`node -e "const fs=require('fs'),p=require('path');p.join=other;console.log(fs.readFileSync(p.join('en','app.json'),'utf8'))"`,
		`node -e "const fs=require('fs'),p=require('path');other(p);console.log(fs.readFileSync(p.join('en','app.json'),'utf8'))"`,
		`node -e "const fs=require('fs'),p=require('path');function run(p){console.log(fs.readFileSync(p.join('en','app.json'),'utf8'))}"`,
		`node -e "const fs=require('fs'),p=require('path');const run=(p)=>fs.readFileSync(p.join('en','app.json'),'utf8')"`,
	} {
		if approvalAutoApproves(bashRequest(command), auto, nil, worktree) {
			t.Errorf("unverified Node command ran without approval: %s", command)
		}
	}
	if !approvalAutoApproves(bashRequest(localeNodeCountCommand), ApprovalPolicy{Mode: ApprovalModeTrusted}, nil, worktree) {
		t.Error("literal locale counting loop should run in trusted mode")
	}
	if approvalAutoApproves(bashRequest(localeNodeCountCommand), auto, nil, worktree) {
		t.Error("runtime directory entries need confinement before auto approval")
	}
}

func TestQuotedNodeLoopKeywordsDoNotChangeShellLoopStructure(t *testing.T) {
	for _, command := range []string{
		`for d in en zh; do node -e "let n=0;for(const x of [1])n++;console.log('$d',n)"; done`,
		`for d in en zh; do node -e "console.log('for x in .env; do cat $d; done')"; done`,
	} {
		_, status := concretizeLiterals(command)
		if status != constApplied {
			t.Errorf("quoted JavaScript changed shell loop matching: status=%d, command=%s", status, command)
		}
	}
	command := `node -e "console.log('for d in en zh; do echo $d; done')"`
	if expanded, status := concretizeLiterals(command); status != constNoop || expanded != command {
		t.Error("text inside a quoted argument was expanded as a shell loop")
	}
}
