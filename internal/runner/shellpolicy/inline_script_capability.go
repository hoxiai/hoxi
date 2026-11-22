package shellpolicy

import (
	"regexp"
	"strings"
)

// Lexer-backed capability judge for inline Python and Node scripts in
// unattended mode and the on-demand sandbox. Mirrors
// web/src/policy/inline-script-capability.js; testdata/unattended.cases.json
// keeps both ports aligned.
//
// Plain string literals ("system ready", "http://localhost"), comments, and JS
// template literal backticks (`count=${n}`) are lexed as data instead of raw
// shell/Ruby backticks or identifier capabilities, while code identifiers,
// `${...}` template expressions, computed member calls, non-literal/blocked
// Node module imports, and decoded/folded secret literals still fail closed.

var (
	inlineCapabilityBypassRe = regexp.MustCompile(
		`(?i)\b(?:locals|setattr|delattr|vm|runin\w*context|constructor|createrequire|globalthis|reflect|proxy)\b|` +
			`\bmodule\s*\.\s*_|\brequire\s*\([^)]*\)\s*\[|\]\s*\(`,
	)

	// inlineGrantableSafeProcessRe extends inlineSafeProcessRe with process.exit
	// / process.exitCode for session-granted inline Node scripts.
	inlineGrantableSafeProcessRe = regexp.MustCompile(`\bprocess\s*\.\s*(?:exit|exitCode)\b`)

	// inlineGrantableBlockedCapRe blocks process spawning, dynamic code loading,
	// reflection, environment access and non-HTTP network protocols inside
	// session-granted inline Python/Node scripts while still allowing ordinary
	// HTTP probes and non-destructive file/data manipulation.
	inlineGrantableBlockedCapRe = regexp.MustCompile(
		`\b(?:subprocess|system|popen\w*|spawn\w*|exec\w*|fork\w*|pty|commands|shell_exec|passthru|proc_open|open3|startfile|ctypes|cffi|dlopen|eval|compile|importlib|getattr|vars|globals|pickle|marshal|environ\w*|getenv\w*|putenv|unsetenv|ENV|smtplib|ftplib|telnetlib|paramiko|LWP|ObjC|NSTask|Function)\b|` +
			`__\w+__|child_process|worker_threads|process\s*\.\s*(?:env|binding|dlopen|mainModule)|sys\s*\.\s*modules|\brequire\s*\(\s*[^'"\s)]|\bimport\s*\(|Deno\s*\.\s*(?:run|Command|env)|Bun\s*\.\s*(?:spawn|env|\$)|Net::|HTTP::|IO::Socket|%x|\bqx\b|[Dd]o\s+[Ss]hell\s+[Ss]cript`,
	)

	// inlineGrantableDestructiveRe blocks filesystem deletion, permission/owner
	// changes, symlinks, renames, process signaling and SQLite extension loading
	// inside session-granted inline Python/Node scripts.
	inlineGrantableDestructiveRe = regexp.MustCompile(
		`(?i)\b(?:unlink(?:sync)?|remove(?:dirs)?|rmdir(?:sync)?|rmtree|rm(?:sync)?|shutil|f?chmod(?:sync)?|lchmod(?:sync)?|f?chown(?:sync)?|lchown(?:sync)?|chflags|symlink(?:sync)?|hardlink|link_to|f?truncate(?:sync)?|rename(?:s|sync)?|kill\w*|signal|setuid|setgid|chroot|posix|nt|load_extension)\b|\bos\s*\.\s*replace\b`,
	)

	inlineGrantableEgressHeaderRe = regexp.MustCompile(`(?i)\b(?:authorization|proxy-authorization|x-api-key)\b|\bbearer\s+\S`)

	inlineGrantableBlockedNodeModules = setOf("child_process", "cluster", "worker_threads", "vm",
		"inspector", "repl", "v8", "wasi", "module", "process", "async_hooks", "trace_events")
)

// grantableInlineCodeCategory judges one inline Python/Node script for the
// desktop-only `inline-script` session class grant. It returns "" only when
// the code lexes cleanly and avoids process execution, dynamic code loading,
// environment/home access, destructive filesystem/process APIs, credential
// egress headers, and secret/protected path literals.
func grantableInlineCodeCategory(family, code string) string {
	if family != "python" && family != "python3" && family != "node" {
		return unattendedCategoryDynamic
	}
	isJS := family == "node"
	rawToks, _, rawLits, ok := lexInlineScript(family, code)
	if !ok {
		return unattendedCategoryDynamic
	}
	if isJS {
		rawToks = normalizeInlinePathAliases(code, rawToks)
	}
	capabilityCode := code
	if isJS {
		capabilityCode = maskInlineDataFields(code, rawToks)
	}
	sqlCode := maskReadOnlySQL(capabilityCode)
	_, codeMask, _, ok := lexInlineScript(family, sqlCode)
	if !ok {
		return unattendedCategoryDynamic
	}
	_, regexVars := collectInlinePathVars(family, code, rawToks)
	masked := maskPrintIdioms(rewriteRegexExecCalls(isJS, regexVars, codeMask))
	if isJS {
		masked = inlineGrantableSafeProcessRe.ReplaceAllString(masked, "proc_ro")
	}
	if cat := scanUnattendedText(masked); cat != "" {
		return cat
	}
	if inlineGrantableBlockedCapRe.MatchString(masked) ||
		(!isJS && strings.Contains(masked, "`")) ||
		(isJS && inlineUnsafeProcessRe.MatchString(masked)) ||
		inlineCapabilityBypassRe.MatchString(masked) ||
		inlineDynamicStringRe.MatchString(masked) {
		return unattendedCategoryDynamic
	}
	if inlineGrantableDestructiveRe.MatchString(masked) ||
		(inlinePathlibRe.MatchString(masked) && inlineReplaceRe.MatchString(masked)) {
		return inlineCategoryWrites
	}
	if inlineReachRe.MatchString(masked) {
		return unattendedCategorySecret
	}
	if !validateInlineRequiresWith(rawToks, inlineGrantableBlockedNodeModules) {
		return unattendedCategoryDynamic
	}
	for _, lit := range rawLits {
		if isDisallowedInlineLiteral(lit) || isProtectedInlineWriteLiteral(lit) {
			return unattendedCategorySecret
		}
		if inlineGrantableEgressHeaderRe.MatchString(lit) {
			return unattendedCategoryEgress
		}
		if cat := scanUnattendedText(lit); cat != "" {
			return cat
		}
	}
	return ""
}

func isProtectedInlineWriteLiteral(raw string) bool {
	p := strings.TrimSpace(raw)
	if p == "" {
		return false
	}
	for _, cand := range []string{p, cleanInlinePosixPath(p)} {
		if cand == "" {
			continue
		}
		if isProtectedRelPath(cand) || isSensitiveExternalPath(cand) {
			return true
		}
	}
	return false
}

func unattendedInlineCodeCategory(family, code string) string {
	if family != "python" && family != "python3" && family != "node" {
		if unattendedScriptCapabilityRe.MatchString(code) {
			return unattendedCategoryDynamic
		}
		return scanUnattendedText(code)
	}
	isJS := family == "node"
	rawToks, _, rawLits, ok := lexInlineScript(family, code)
	if !ok {
		return unattendedCategoryDynamic
	}
	if isJS {
		rawToks = normalizeInlinePathAliases(code, rawToks)
	}
	capabilityCode := code
	if isJS {
		capabilityCode = maskInlineDataFields(code, rawToks)
	}
	_, codeMask, _, ok := lexInlineScript(family, capabilityCode)
	if !ok {
		return unattendedCategoryDynamic
	}
	_, regexVars := collectInlinePathVars(family, code, rawToks)
	masked := maskPrintIdioms(rewriteRegexExecCalls(isJS, regexVars, codeMask))
	if cat := scanUnattendedText(masked); cat != "" {
		return cat
	}
	if unattendedScriptCapabilityRe.MatchString(masked) ||
		(isJS && inlineUnsafeProcessRe.MatchString(masked)) ||
		inlineCapabilityBypassRe.MatchString(masked) {
		return unattendedCategoryDynamic
	}
	if !validateInlineRequiresOnly(rawToks) {
		return unattendedCategoryDynamic
	}
	for _, lit := range rawLits {
		if isDisallowedInlineLiteral(lit) {
			return unattendedCategorySecret
		}
	}
	return ""
}

func validateInlineRequiresOnly(toks []inlineTok) bool {
	return validateInlineRequiresWith(toks, inlineBlockedNodeModules)
}

func validateInlineRequiresWith(toks []inlineTok, blocked map[string]bool) bool {
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind == "tpl" {
			for _, sub := range t.exprs {
				if !validateInlineRequiresWith(sub, blocked) {
					return false
				}
			}
			continue
		}
		if t.kind != "id" {
			continue
		}
		isProp := i > 0 && toks[i-1].kind == "op" && toks[i-1].val == "."
		if t.val == "from" && !isProp && i+1 < len(toks) && toks[i+1].kind == "str" {
			mod := strings.TrimPrefix(toks[i+1].val, "node:")
			if blocked[strings.SplitN(mod, "/", 2)[0]] {
				return false
			}
		}
		if t.val == "require" && !isProp {
			if i+1 >= len(toks) || toks[i+1].kind != "op" || toks[i+1].val != "(" {
				return false
			}
			closeIdx := matchingCloseParen(toks, i+1)
			if closeIdx >= len(toks) || closeIdx != i+3 || toks[i+2].kind != "str" {
				return false
			}
			mod := strings.TrimPrefix(toks[i+2].val, "node:")
			if blocked[strings.SplitN(mod, "/", 2)[0]] {
				return false
			}
		}
	}
	return true
}
