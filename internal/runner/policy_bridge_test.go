package runner

import "github.com/hoxiai/hoxi/internal/runner/shellpolicy"

const (
	unattendedCategorySecret  = shellpolicy.UnattendedCategorySecret
	unattendedCategoryDynamic = shellpolicy.UnattendedCategoryDynamic
	seqLoopPlaceholder        = shellpolicy.SeqLoopPlaceholder
	maxLiteralLoopBytes       = shellpolicy.MaxLiteralLoopBytes

	localeNodeCountCommand = `cd admin/src/locale/lang && for d in en zh zh-HK; do echo -n "$d: "; node -e "const fs=require('fs'),p=require('path');let n=0;for(const f of fs.readdirSync('$d')){if(f.endsWith('.json')){const j=JSON.parse(fs.readFileSync(p.join('$d',f),'utf8'));const c=o=>{for(const v of Object.values(o)){if(v&&typeof v==='object'&&!Array.isArray(v))c(v);else n++}};c(j)}}console.log(n)"; done`
)

func unattendedHardDeny(item map[string]interface{}, worktree string) string {
	return shellpolicy.UnattendedHardDeny(item, worktree)
}
func unattendedDynamicCategory(command string) string {
	return shellpolicy.UnattendedDynamicCategory(command)
}
func isClaudeSkillReadPath(raw string, directoryScope bool) bool {
	return shellpolicy.IsClaudeSkillReadPath(raw, directoryScope)
}
func isAttributionPreferenceReadPath(raw string) bool {
	return shellpolicy.IsAttributionPreferenceReadPath(raw)
}
func numericLoopValues(expr string) ([]string, bool)  { return shellpolicy.NumericLoopValues(expr) }
func normalizeNumericForHeaders(s string) string      { return shellpolicy.NormalizeNumericForHeaders(s) }
func concretizeLiterals(command string) (string, int) { return shellpolicy.ConcretizeLiterals(command) }
func maskVerifiedProcessControl(cmd string, owned func(processControlTarget) bool) (string, bool) {
	return shellpolicy.MaskVerifiedProcessControl(cmd, owned)
}
func trustedProcessControlTool(name string) bool { return shellpolicy.TrustedProcessControlTool(name) }
func isTrustedWindowsKillPath(realPath string, getenv func(string) string) bool {
	return shellpolicy.IsTrustedWindowsKillPath(realPath, getenv)
}
