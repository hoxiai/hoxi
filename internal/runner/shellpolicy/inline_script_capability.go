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

var inlineCapabilityBypassRe = regexp.MustCompile(
	`(?i)\b(?:locals|setattr|delattr|vm|runin\w*context|constructor|createrequire|globalthis|reflect|proxy)\b|` +
		`\bmodule\s*\.\s*_|\brequire\s*\([^)]*\)\s*\[|\]\s*\(`,
)

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
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind == "tpl" {
			for _, sub := range t.exprs {
				if !validateInlineRequiresOnly(sub) {
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
			if inlineBlockedNodeModules[strings.SplitN(mod, "/", 2)[0]] {
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
			if inlineBlockedNodeModules[strings.SplitN(mod, "/", 2)[0]] {
				return false
			}
		}
	}
	return true
}
