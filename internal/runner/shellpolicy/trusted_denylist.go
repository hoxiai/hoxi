package shellpolicy

import (
	"path/filepath"
	"regexp"
	"strings"
)

// isTrustedExplicitHighRisk enforces the explicit high-risk denylist for
// ApprovalModeTrusted ("Full Trust") mode. Unlike auto mode, trusted mode skips
// static shell opacity checks (isOpaqueShellCommandWith) so complex routine
// bash (loops, command substitutions, variable expansions, subshells, local
// scripts) runs without confirmation popups or on-demand Seatbelt wrapping.
//
// Requests that are destructive (rm, chmod, git reset --hard, sudo, .env,
// protected writes, force push, non-owned kill, doom_loop, etc.) still flow
// through isDestructivePermissionRequest and modeAllowsControllableRequest.
// This file covers explicit high-risk shell patterns that isDestructiveCommand
// does not already match directly: su/doas, non-git-push publishing, credential
// egress, credential environment variable expansions, bare credential
// directories without trailing slashes, ~/.hoxi/* listings, sensitive cd
// targets, executing protected/attachment scripts, download-and-execute chains,
// BASH_ENV startup injection, ssh local-exec options, and brace-obfuscated
// secrets or destructive commands.
var (
	trustedPublishRe = regexp.MustCompile(`(?i)(?:^|[;&|(\n]|&&|\|\|)\s*(?:nohup\s+|time\s+|exec\s+|command\s+|env(?:\s+[A-Za-z_][A-Za-z0-9_]*=\S*)*\s+)*(?:gh\s+(?:release\s+create|pr\s+merge)|(?:npm|pnpm|yarn|bun)\s+(?:-r\s+|--recursive\s+)?publish|cargo\s+publish|twine\s+upload|gem\s+push|docker\s+push|terraform\s+(?:apply|destroy)|kubectl\s+(?:apply|delete)|helm\s+(?:install|upgrade)|(?:\./)?scripts/ai-release\.sh)\b`)

	trustedWrappedHighRiskRe = regexp.MustCompile(`(?i)(?:^|[;&|(\n]|&&|\|\|)\s*(?:nohup\s+|time\s+|exec\s+|command\s+|env(?:\s+[A-Za-z_][A-Za-z0-9_]*=\S*)*\s+)*(?:(?:ba|z|da)?sh|eval|xargs)\b[^\n]*\b(?:sudo|su|doas|gh\s+(?:release\s+create|pr\s+merge)|(?:npm|pnpm|yarn|bun)\s+(?:-r\s+|--recursive\s+)?publish|cargo\s+publish|twine\s+upload|gem\s+push|docker\s+push|terraform\s+(?:apply|destroy)|kubectl\s+(?:apply|delete)|helm\s+(?:install|upgrade)|(?:\./)?scripts/ai-release\.sh)\b`)

	trustedCredentialDirRe = regexp.MustCompile(`(?i)(?:^|[\s'"=(@<>:,/])(?:\.ssh|\.aws|\.gnupg|\.kube|\.azure|\.codex)(?:$|[\s;&|)'"/*?:,])`)

	trustedHomeHoxiWildcardRe = regexp.MustCompile(`(?i)(?:^|[\s'"=(@<>:,])(?:~|\$HOME|\$\{HOME\})/\.hoxi(?:/[*?]|/?(?:$|[\s;&|)'"]))`)

	trustedXDGSecretRe = regexp.MustCompile(`(?i)\$\{?XDG_CONFIG_HOME\}?/(?:gh|gcloud|opencode)(?:/|$|[\s;&|)'"*?])`)

	trustedBashEnvRe = regexp.MustCompile(`(?:^|[\s;&|(])(?:export\s+|env(?:\s+-\S+)*\s+)?BASH_ENV=`)

	trustedDownloaderRe = regexp.MustCompile(`(?i)(?:^|[\s;&|(])(?:\S*/)?(?:curl|wget)\b`)

	trustedSSHExecOptRe = regexp.MustCompile(`(?i)(?:proxycommand|localcommand|permitlocalcommand|knownhostscommand)`)

	trustedDotEnvGlobRe = regexp.MustCompile(`(?i)(?:^|[\s'"=(@<>:,/])[^\s;&|'"]*\*\.env(?:\.[A-Za-z0-9_.-]+)?(?:$|[\s;&|)'"*?])`)
)

func isTrustedExplicitHighRisk(item map[string]interface{}, worktree string) bool {
	req := parsePermissionRequest(item)
	if req.permission == "external_directory" {
		return false
	}
	category := permissionCategory(item)
	if category != permissionCategoryBash && !req.hasCommandField {
		return false
	}
	worktree = req.resolveWorktree(worktree)
	candidates := trustedBashCandidates(item)
	if len(candidates) == 0 {
		return true
	}
	for _, cmd := range candidates {
		if isTrustedHighRiskCommand(cmd, worktree) {
			return true
		}
	}
	// Dynamic execution is judged on the whole command: OpenCode's split
	// patterns lose the quoting and heredocs that tie pieces together.
	for _, cmd := range opacityCandidates(item) {
		if trustedDynamicHighRisk(cmd) {
			return true
		}
	}
	return false
}

func trustedBashCandidates(item map[string]interface{}) []string {
	seen := map[string]bool{}
	var out []string
	add := func(list []string) {
		for _, raw := range list {
			s := strings.TrimSpace(unwrapSandboxCommand(raw))
			if s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	add(opacityCandidates(item))
	add(commandCandidates(item))
	return out
}

func isTrustedHighRiskCommand(cmd, worktree string) bool {
	if trustedBashEnvRe.MatchString(maskCommandDataArguments(cmd)) {
		return true
	}
	forms := destructiveScanForms(cmd)
	for _, form := range forms {
		if matchesTrustedHighRiskForm(form, worktree) {
			return true
		}
	}
	return false
}

func matchesTrustedHighRiskForm(form, worktree string) bool {
	if hasProtectedGitOutput(form) {
		return true
	}
	deob := deobfuscator.Replace(form)
	if unattendedSudoRe.MatchString(form) ||
		unattendedEgressRe.MatchString(form) ||
		trustedPublishRe.MatchString(form) ||
		trustedWrappedHighRiskRe.MatchString(form) ||
		trustedBashEnvRe.MatchString(form) {
		return true
	}
	if unattendedSecretRe.MatchString(form) ||
		unattendedSecretRe.MatchString(deob) ||
		trustedCredentialDirRe.MatchString(form) ||
		trustedCredentialDirRe.MatchString(deob) ||
		trustedHomeHoxiWildcardRe.MatchString(form) ||
		trustedHomeHoxiWildcardRe.MatchString(deob) ||
		trustedXDGSecretRe.MatchString(form) ||
		trustedXDGSecretRe.MatchString(deob) ||
		trustedBrowserSecretRe.MatchString(form) ||
		trustedBrowserSecretRe.MatchString(deob) ||
		hasTrustedDotEnvGlob(form) ||
		hasTrustedDotEnvGlob(deob) {
		return true
	}
	if trustedDownloaderRe.MatchString(form) && runsScriptNamedElsewhere(form) {
		return true
	}
	if referencesCredentialEnvVar(form) {
		return true
	}
	if hasTrustedHighRiskSegments(form, worktree, 0) {
		return true
	}
	return hasBraceObfuscatedRisk(form)
}

func hasTrustedDotEnvGlob(s string) bool {
	for _, m := range trustedDotEnvGlobRe.FindAllString(s, -1) {
		lower := strings.ToLower(m)
		if strings.Contains(lower, ".env.example") || strings.Contains(lower, ".env.sample") || strings.Contains(lower, ".env.template") {
			continue
		}
		return true
	}
	return false
}

// referencesCredentialEnvVar reports whether s expands an environment variable
// outside single quotes whose name matches isCredentialEnvName (e.g. $KUBECONFIG,
// $OPENAI_API_KEY, ${GITHUB_TOKEN:-fallback}). Plain assignments without `$`
// and literal strings inside single quotes are ignored.
func referencesCredentialEnvVar(s string) bool {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if c == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
				continue
			}
		} else if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c != '$' || i+1 >= len(s) {
			continue
		}
		next := s[i+1]
		if next == '{' {
			j := i + 2
			if j < len(s) && (s[j] == '!' || s[j] == '#') {
				j++
			}
			start := j
			for j < len(s) && isEnvIdentChar(s[j], j == start) {
				j++
			}
			if j > start && isCredentialEnvName(s[start:j]) {
				return true
			}
			continue
		}
		if isEnvIdentChar(next, true) {
			j := i + 1
			for j < len(s) && isEnvIdentChar(s[j], j == i+1) {
				j++
			}
			if isCredentialEnvName(s[i+1 : j]) {
				return true
			}
			i = j - 1
		}
	}
	return false
}

func isEnvIdentChar(c byte, first bool) bool {
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' {
		return true
	}
	return !first && c >= '0' && c <= '9'
}

func hasTrustedHighRiskSegments(command, worktree string, depth int) bool {
	if depth > 4 {
		return false
	}
	joined := joinLineContinuations(command)
	if spans, ok := findSubshellSpans(joined); ok && len(spans) > 0 {
		for _, sp := range spans {
			if hasTrustedHighRiskSegments(joined[sp[0]+1:sp[1]], worktree, depth+1) {
				return true
			}
		}
		joined = eraseSubshells(joined)
	}
	cwd := worktree
	segments, _ := splitCommandChainWithPipes(joined)
	for _, seg := range segments {
		words := stripShellKeywords(shellWords(seg))
		for len(words) > 0 && envVarAssignmentWordRe.MatchString(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		words, _, ok := unwrapCommandPrefixes(words)
		if !ok || len(words) == 0 {
			continue
		}
		if isGlobCommandWord(words[0]) {
			return true
		}
		base := strings.ToLower(filepath.Base(normalizeAbsoluteTool(words[0])))
		if cat := unattendedArgvCategory(words); cat == unattendedCategoryPriv || cat == unattendedCategoryEgress || (cat == unattendedCategoryPublish && base != "git") {
			return true
		}
		switch base {
		case "cd":
			nextCWD, dangerous := checkTrustedCD(words[1:], cwd)
			if dangerous {
				return true
			}
			cwd = nextCWD
		case "pushd", "popd":
			cwd = ""
		case "sh", "bash", "zsh", "dash":
			if isTrustedDangerousShellScript(words[1:]) {
				return true
			}
		case "ssh", "scp":
			if hasTrustedUnsafeSSHExecOpt(words[1:]) {
				return true
			}
		}
	}
	return false
}

func checkTrustedCD(args []string, cwd string) (nextCWD string, dangerous bool) {
	target := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 < len(args) {
				target = args[i+1]
			}
			break
		}
		if a == "-L" || a == "-P" || a == "-e" || a == "-@" {
			continue
		}
		target = a
		break
	}
	target = strings.TrimSpace(target)
	if target == "" || target == "-" || strings.ContainsAny(target, "$`") {
		return "", false
	}
	if isSensitiveExternalPath(target) || isProtectedShellReadPath(target) {
		return "", true
	}
	if filepath.IsAbs(target) || isWindowsDriveAbs(target) {
		return filepath.Clean(target), false
	}
	if strings.HasPrefix(target, "~") {
		return "", false
	}
	if cwd != "" {
		resolved := filepath.Clean(filepath.Join(cwd, target))
		if isSensitiveExternalPath(resolved) || isProtectedShellReadPath(resolved) {
			return "", true
		}
		return resolved, false
	}
	return "", false
}

func isTrustedDangerousShellScript(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 < len(args) {
				return isProtectedOrAttachmentScriptPath(args[i+1])
			}
			return false
		}
		if a == "-c" || a == "-s" || a == "-" {
			return false
		}
		if a == "-o" || a == "+o" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+") {
			if strings.Contains(a, "c") || strings.Contains(a, "s") {
				return false
			}
			continue
		}
		return isProtectedOrAttachmentScriptPath(a)
	}
	return false
}

func isProtectedOrAttachmentScriptPath(scriptPath string) bool {
	p := strings.TrimSpace(scriptPath)
	if p == "" || strings.ContainsAny(p, "$`") {
		return false
	}
	slash := strings.ToLower(filepath.ToSlash(filepath.Clean(p)))
	if isRoutineAttachmentSubpath(slash) {
		return true
	}
	if isRoutineSkillSubpath(slash) {
		return false
	}
	return isProtectedRelPath(p) || isSensitiveExternalPath(p)
}

func hasTrustedUnsafeSSHExecOpt(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-o" && i+1 < len(args) {
			if trustedSSHExecOptRe.MatchString(args[i+1]) {
				return true
			}
			i++
		} else if strings.HasPrefix(a, "-o") && len(a) > 2 && trustedSSHExecOptRe.MatchString(a[2:]) {
			return true
		}
	}
	return false
}

// hasBraceObfuscatedRisk catches unquoted brace expansions that hide a
// destructive command (`{rm,-rf,.}`, `{r,m}`) or a secret path (`cat {.e,}nv`,
// `grep --include={.env,a.go} .`, `cat ~/.{ssh,aws}/id_rsa`). Quoted braces
// (JSON payloads, JS object literals, jq filters) are never expanded by the
// shell and are skipped.
func hasBraceObfuscatedRisk(form string) bool {
	segments, _ := splitCommandChainWithPipes(joinLineContinuations(form))
	for _, seg := range segments {
		tokens := unquotedBraceTokens(seg)
		for idx, tok := range tokens {
			if tok.raw == "" || !tok.hasUnquotedBrace || strings.Contains(tok.raw, "$") {
				continue
			}
			if strings.HasPrefix(tok.raw, "--exclude=") || strings.HasPrefix(tok.raw, "--exclude-dir=") {
				continue
			}
			expanded, ok := expandSingleBraceToken(tok.raw)
			if !ok {
				if idx == 0 {
					return true
				}
				continue
			}
			for _, item := range expanded {
				if idx == 0 {
					if destructiveRe.MatchString(item) || unattendedSudoRe.MatchString(item) {
						return true
					}
				}
				check := item
				if eq := strings.IndexByte(check, '='); eq > 0 && strings.HasPrefix(check, "-") {
					check = check[eq+1:]
				}
				if isSecretFilePath(check) || trustedCredentialDirRe.MatchString(check) || unattendedSecretRe.MatchString(check) {
					return true
				}
			}
		}
	}
	return false
}

type braceScanToken struct {
	raw              string
	hasUnquotedBrace bool
}

func unquotedBraceTokens(seg string) []braceScanToken {
	var out []braceScanToken
	var cur strings.Builder
	var quote byte
	inWord := false
	hasBrace := false
	flush := func() {
		if !inWord {
			return
		}
		w := cur.String()
		if len(out) == 0 && (shellKeywords[w] || envVarAssignmentWordRe.MatchString(w)) {
			cur.Reset()
			inWord = false
			hasBrace = false
			return
		}
		out = append(out, braceScanToken{raw: w, hasUnquotedBrace: hasBrace})
		cur.Reset()
		inWord = false
		hasBrace = false
	}
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteByte(c)
				inWord = true
			}
			continue
		}
		if c == '\\' && i+1 < len(seg) {
			cur.WriteByte(seg[i+1])
			inWord = true
			i++
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
			} else {
				cur.WriteByte(c)
				inWord = true
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			inWord = true
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			flush()
			continue
		}
		if c == '{' || c == '}' {
			hasBrace = true
		}
		cur.WriteByte(c)
		inWord = true
	}
	flush()
	return out
}

func expandSingleBraceToken(tok string) ([]string, bool) {
	open := strings.IndexByte(tok, '{')
	if open < 0 {
		return nil, true
	}
	closeIdx := strings.IndexByte(tok[open+1:], '}')
	if closeIdx < 0 {
		return nil, false
	}
	closeIdx += open + 1
	inner := tok[open+1 : closeIdx]
	if strings.ContainsAny(inner, "{}") {
		return nil, false
	}
	if !strings.Contains(inner, ",") {
		return nil, true
	}
	alts := strings.Split(inner, ",")
	if len(alts) > maxLiteralLoopItems {
		return nil, false
	}
	prefix := tok[:open]
	suffix := tok[closeIdx+1:]
	out := make([]string, 0, len(alts))
	for _, alt := range alts {
		out = append(out, prefix+alt+suffix)
	}
	return out, true
}
