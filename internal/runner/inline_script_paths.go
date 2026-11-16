package runner

import (
	"regexp"
	"strings"
)

func countInlineWritesAndParams(isJS bool, toks []inlineTok, writes, params map[string]int) {
	parenDepth := 0
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind == "tpl" {
			for _, sub := range t.exprs {
				countInlineWritesAndParams(isJS, sub, writes, params)
			}
			continue
		}
		if t.kind == "op" {
			switch t.val {
			case "(":
				if isJS && isArrowParamParen(toks, i) {
					end := matchingCloseParen(toks, i)
					for k := i + 1; k < end; k++ {
						if toks[k].kind == "id" {
							params[toks[k].val]++
						} else if toks[k].kind == "op" && toks[k].val != "," {
							markAllIdentWrites(toks[i+1:end], writes, 2)
							break
						}
					}
					i = end
					continue
				}
				parenDepth++
			case ")":
				if parenDepth > 0 {
					parenDepth--
				}
			}
			continue
		}
		if t.kind != "id" {
			continue
		}
		// Single-identifier arrow param: `x =>`
		if isJS && i+1 < len(toks) && toks[i+1].kind == "op" && toks[i+1].val == "=>" {
			params[t.val]++
			continue
		}
		// JS destructuring or uninitialized let/var
		if isJS && (t.val == "const" || t.val == "let" || t.val == "var") && i+1 < len(toks) {
			if toks[i+1].kind == "op" && (toks[i+1].val == "{" || toks[i+1].val == "[") {
				end := matchingCloseBracket(toks, i+1)
				markAllIdentWrites(toks[i+1:end], writes, 2)
				continue
			}
			if (t.val == "let" || t.val == "var") && toks[i+1].kind == "id" {
				if i+2 >= len(toks) || (toks[i+2].kind == "op" && (toks[i+2].val == ";" || toks[i+2].val == ",")) {
					writes[toks[i+1].val]++
				}
			}
		}
		// JS for (... of/in ...)
		if isJS && t.val == "for" && i+1 < len(toks) && toks[i+1].kind == "op" && toks[i+1].val == "(" {
			k := i + 2
			if k < len(toks) && toks[k].kind == "id" && (toks[k].val == "const" || toks[k].val == "let" || toks[k].val == "var") {
				k++
			}
			if k+1 < len(toks) && toks[k].kind == "id" && toks[k+1].kind == "id" && (toks[k+1].val == "of" || toks[k+1].val == "in") {
				writes[toks[k].val]++
			} else if k < len(toks) && toks[k].kind == "op" && (toks[k].val == "[" || toks[k].val == "{") {
				end := matchingCloseBracket(toks, k)
				markAllIdentWrites(toks[k:end], writes, 2)
			}
			continue
		}
		// Python `for x in ...`
		if !isJS && t.val == "for" {
			inIdx := -1
			for k := i + 1; k < len(toks) && k <= i+8; k++ {
				if toks[k].kind == "id" && toks[k].val == "in" {
					inIdx = k
					break
				}
			}
			if inIdx > i+1 {
				if inIdx == i+2 && toks[i+1].kind == "id" {
					writes[toks[i+1].val]++
				} else {
					markAllIdentWrites(toks[i+1:inIdx], writes, 2)
				}
			}
			continue
		}
		// Function / class / catch / Python `as`
		if (t.val == "function" || t.val == "def" || t.val == "class" || (!isJS && t.val == "as")) && i+1 < len(toks) && toks[i+1].kind == "id" {
			writes[toks[i+1].val] += 2
			continue
		}
		if isJS && t.val == "catch" && i+2 < len(toks) && toks[i+1].kind == "op" && toks[i+1].val == "(" && toks[i+2].kind == "id" {
			writes[toks[i+2].val] += 2
			continue
		}
		// Prefix/postfix ++ / --
		if (i > 0 && toks[i-1].kind == "op" && (toks[i-1].val == "++" || toks[i-1].val == "--")) ||
			(i+1 < len(toks) && toks[i+1].kind == "op" && (toks[i+1].val == "++" || toks[i+1].val == "--")) {
			writes[t.val] += 2
			continue
		}
		// Assignment operators
		if i+1 < len(toks) && toks[i+1].kind == "op" && inlineAssignOps[toks[i+1].val] {
			isProp := i > 0 && toks[i-1].kind == "op" && toks[i-1].val == "."
			if isProp {
				continue
			}
			if !isJS && parenDepth > 0 && toks[i+1].val == "=" && i > 0 && toks[i-1].kind == "op" && (toks[i-1].val == "(" || toks[i-1].val == ",") {
				continue // Python keyword argument
			}
			writes[t.val]++
		}
	}
}

func markAllIdentWrites(toks []inlineTok, writes map[string]int, delta int) {
	for _, t := range toks {
		if t.kind == "id" {
			writes[t.val] += delta
		}
	}
}

func matchingCloseParen(toks []inlineTok, openIdx int) int {
	return matchingCloseDelim(toks, openIdx, "(", ")")
}

func matchingCloseBracket(toks []inlineTok, openIdx int) int {
	open := toks[openIdx].val
	close := ")"
	if open == "[" {
		close = "]"
	} else if open == "{" {
		close = "}"
	}
	return matchingCloseDelim(toks, openIdx, open, close)
}

func matchingCloseDelim(toks []inlineTok, openIdx int, open, close string) int {
	depth := 0
	for i := openIdx; i < len(toks); i++ {
		if toks[i].kind == "op" {
			if toks[i].val == open {
				depth++
			} else if toks[i].val == close {
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return len(toks)
}

func isArrowParamParen(toks []inlineTok, openIdx int) bool {
	if openIdx >= len(toks) || toks[openIdx].kind != "op" || toks[openIdx].val != "(" {
		return false
	}
	closeIdx := matchingCloseParen(toks, openIdx)
	return closeIdx+1 < len(toks) && toks[closeIdx+1].kind == "op" && toks[closeIdx+1].val == "=>"
}

// parseStringListTokens parses `["a", "b"]` or `("a", "b")` starting at openIdx.
func parseStringListTokens(toks []inlineTok, openIdx int, allowTuple bool) ([]string, int, bool) {
	if openIdx >= len(toks) || toks[openIdx].kind != "op" {
		return nil, 0, false
	}
	open := toks[openIdx].val
	if open != "[" && (!allowTuple || open != "(") {
		return nil, 0, false
	}
	close := "]"
	if open == "(" {
		close = ")"
	}
	var vals []string
	expectVal := true
	for i := openIdx + 1; i < len(toks); i++ {
		if toks[i].kind == "op" && toks[i].val == close {
			if len(vals) == 0 || len(vals) > maxInlineCandidates {
				return nil, 0, false
			}
			return vals, i, true
		}
		if expectVal {
			if toks[i].kind != "str" {
				return nil, 0, false
			}
			vals = append(vals, toks[i].val)
			expectVal = false
		} else {
			if toks[i].kind != "op" || toks[i].val != "," {
				return nil, 0, false
			}
			expectVal = true
		}
	}
	return nil, 0, false
}

func collectInlinePathVars(family, code string, toks []inlineTok) (map[string][]string, map[string]bool) {
	isJS := family == "node"
	writes := map[string]int{}
	params := map[string]int{}
	countInlineWritesAndParams(isJS, toks, writes, params)

	vars := map[string][]string{}
	arrVars := map[string][]string{}
	regexVars := map[string]bool{}
	canBindVar := func(name string) bool { return writes[name] == 1 && params[name] == 0 }
	canBindParam := func(name string) bool { return writes[name] == 0 && params[name] == 1 }

	for i := 0; i < len(toks); i++ {
		t := toks[i]
		// JS `for (const x of [...])` or `for (const x of arrVar)`
		if isJS && t.kind == "id" && t.val == "for" && i+4 < len(toks) && toks[i+1].kind == "op" && toks[i+1].val == "(" {
			k := i + 2
			if toks[k].kind == "id" && (toks[k].val == "const" || toks[k].val == "let") {
				k++
			}
			if k+2 < len(toks) && toks[k].kind == "id" && canBindVar(toks[k].val) && toks[k+1].kind == "id" && toks[k+1].val == "of" {
				varName := toks[k].val
				if list, endIdx, ok := parseStringListTokens(toks, k+2, false); ok && endIdx+1 < len(toks) && toks[endIdx+1].val == ")" {
					vars[varName] = list
				} else if toks[k+2].kind == "id" && k+3 < len(toks) && toks[k+3].val == ")" {
					if list, ok := arrVars[toks[k+2].val]; ok {
						vars[varName] = list
					}
				}
			}
			continue
		}
		// Python `for x in [...]:` / `for x in (...):` / `for x in arrVar:`
		if !isJS && t.kind == "id" && t.val == "for" && i+3 < len(toks) && toks[i+1].kind == "id" && canBindVar(toks[i+1].val) && toks[i+2].kind == "id" && toks[i+2].val == "in" {
			varName := toks[i+1].val
			if list, endIdx, ok := parseStringListTokens(toks, i+3, true); ok && endIdx+1 < len(toks) && toks[endIdx+1].val == ":" {
				vars[varName] = list
			} else if toks[i+3].kind == "id" && i+4 < len(toks) && toks[i+4].val == ":" {
				if list, ok := arrVars[toks[i+3].val]; ok {
					vars[varName] = list
				}
			}
			continue
		}
		// JS `[...].forEach(x => ...)` or `arrVar.forEach((x) => ...)` (and .map/.filter/.some/.every)
		if isJS && t.kind == "op" && t.val == "." && i+2 < len(toks) && toks[i+1].kind == "id" &&
			(toks[i+1].val == "forEach" || toks[i+1].val == "map" || toks[i+1].val == "filter" || toks[i+1].val == "some" || toks[i+1].val == "every") &&
			toks[i+2].kind == "op" && toks[i+2].val == "(" {
			var recvList []string
			if i > 0 && toks[i-1].kind == "id" {
				recvList = arrVars[toks[i-1].val]
			} else if i > 0 && toks[i-1].kind == "op" && toks[i-1].val == "]" {
				openBr := findMatchingOpenBracket(toks, i-1)
				if openBr >= 0 {
					if list, _, ok := parseStringListTokens(toks, openBr, false); ok {
						recvList = list
					}
				}
			}
			if len(recvList) > 0 {
				if pName := extractSingleArrowParam(toks, i+3); pName != "" && canBindParam(pName) {
					vars[pName] = recvList
				}
			}
			continue
		}
		// Single-assignment variable: JS `const x = <rhs>` (or `let x = <rhs>` with writeCount==1) / Python `x = <rhs>`
		var lhsName string
		rhsStart := -1
		if isJS && t.kind == "id" && (t.val == "const" || t.val == "let") && i+2 < len(toks) &&
			toks[i+1].kind == "id" && toks[i+2].kind == "op" && toks[i+2].val == "=" {
			lhsName = toks[i+1].val
			rhsStart = i + 3
		} else if !isJS && t.kind == "id" && i+1 < len(toks) && toks[i+1].kind == "op" && toks[i+1].val == "=" &&
			(i == 0 || !(toks[i-1].kind == "op" && (toks[i-1].val == "." || toks[i-1].val == "(" || toks[i-1].val == ","))) {
			lhsName = t.val
			rhsStart = i + 2
		}
		if lhsName != "" && canBindVar(lhsName) && rhsStart < len(toks) {
			rhsEnd := statementExprEnd(code, toks, rhsStart)
			rhs := toks[rhsStart:rhsEnd]
			if isJS && len(rhs) == 1 && rhs[0].kind == "re" {
				regexVars[lhsName] = true
			} else if list, endIdx, ok := parseStringListTokens(toks, rhsStart, !isJS); ok && rhsStart+endIdx-rhsStart+1 == rhsEnd {
				arrVars[lhsName] = list
			} else if resolved, ok := resolveInlinePathTokens(rhs, vars, 0); ok {
				vars[lhsName] = resolved
			}
		}
	}
	return vars, regexVars
}

// normalizeInlinePathAliases recognizes single-write `const p = require('path')`
// (including comma declarations). Only the path-expression tokens change, never
// the code being scanned for capabilities or executed. Unknown/rebound aliases
// remain unresolved. Mirrors normalizeInlinePathAliases in inline-script-paths.js.
func normalizeInlinePathAliases(code string, toks []inlineTok) []inlineTok {
	writes, params := map[string]int{}, map[string]int{}
	countInlineWritesAndParams(true, toks, writes, params)
	aliases := map[string][2]int{}
	for i, t := range toks {
		if t.kind != "id" || (t.val != "const" && t.val != "let") {
			continue
		}
		for j := i + 1; j+2 < len(toks) && toks[j].kind == "id" && toks[j+1].val == "="; {
			end := statementExprEnd(code, toks, j+2)
			rhs := toks[j+2 : end]
			if writes[toks[j].val] == 1 && params[toks[j].val] == 0 && len(rhs) == 4 &&
				rhs[0].kind == "id" && rhs[0].val == "require" && rhs[1].val == "(" &&
				rhs[2].kind == "str" && (rhs[2].val == "path" || rhs[2].val == "node:path") && rhs[3].val == ")" {
				aliases[toks[j].val] = [2]int{j, end}
			}
			if end >= len(toks) || toks[end].val != "," {
				break
			}
			j = end + 1
		}
	}
	for name, binding := range aliases {
		for i, t := range toks {
			if inlineTemplateUses(t, name) || (t.kind == "id" && t.val == name && i != binding[0] &&
				(i <= binding[1] || (i > 0 && toks[i-1].val == ".") || i+3 >= len(toks) ||
					toks[i+1].val != "." || toks[i+2].val != "join" || toks[i+3].val != "(")) {
				delete(aliases, name)
				break
			}
		}
	}
	out := append([]inlineTok(nil), toks...)
	for i := 0; i+3 < len(out); i++ {
		if binding, ok := aliases[out[i].val]; ok && i > binding[1] && out[i].kind == "id" &&
			out[i+1].val == "." && out[i+2].val == "join" && out[i+3].val == "(" {
			out[i].val = "path"
		}
	}
	return out
}

func inlineTemplateUses(t inlineTok, name string) bool {
	for _, expr := range t.exprs {
		for _, sub := range expr {
			if (sub.kind == "id" && sub.val == name) || inlineTemplateUses(sub, name) {
				return true
			}
		}
	}
	return false
}

func findMatchingOpenBracket(toks []inlineTok, closeIdx int) int {
	depth := 0
	for i := closeIdx; i >= 0; i-- {
		if toks[i].kind == "op" {
			if toks[i].val == "]" {
				depth++
			} else if toks[i].val == "[" {
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}

func extractSingleArrowParam(toks []inlineTok, idx int) string {
	if idx+1 < len(toks) && toks[idx].kind == "id" && toks[idx+1].kind == "op" && toks[idx+1].val == "=>" {
		return toks[idx].val
	}
	if idx+3 < len(toks) && toks[idx].kind == "op" && toks[idx].val == "(" &&
		toks[idx+1].kind == "id" && toks[idx+2].kind == "op" && toks[idx+2].val == ")" &&
		toks[idx+3].kind == "op" && toks[idx+3].val == "=>" {
		return toks[idx+1].val
	}
	return ""
}

func statementExprEnd(code string, toks []inlineTok, start int) int {
	depth := 0
	for k := start; k < len(toks); k++ {
		if k > start && depth == 0 && toks[k-1].end <= len(code) && toks[k].start <= len(code) && toks[k-1].end <= toks[k].start {
			if strings.Contains(code[toks[k-1].end:toks[k].start], "\n") {
				prevOp := toks[k-1].kind == "op" && (toks[k-1].val == "+" || toks[k-1].val == ".")
				curOp := toks[k].kind == "op" && (toks[k].val == "+" || toks[k].val == ".")
				if !prevOp && !curOp {
					return k
				}
			}
		}
		if toks[k].kind == "op" {
			switch toks[k].val {
			case "(", "[", "{":
				depth++
			case ")", "]":
				if depth == 0 {
					return k
				}
				depth--
			case "}":
				if depth == 0 {
					return k
				}
				depth--
			case ";", ",":
				if depth == 0 {
					return k
				}
			}
		}
	}
	return len(toks)
}

func resolveInlinePathTokens(expr []inlineTok, vars map[string][]string, depth int) ([]string, bool) {
	if len(expr) == 0 || depth > maxInlinePathDepth {
		return nil, false
	}
	// Strip outer parentheses `( ... )`
	if expr[0].kind == "op" && expr[0].val == "(" && matchingCloseParen(expr, 0) == len(expr)-1 {
		return resolveInlinePathTokens(expr[1:len(expr)-1], vars, depth+1)
	}
	// Split top-level `+`
	if parts, ok := splitTopLevelPlus(expr); ok && len(parts) > 1 {
		acc := []string{""}
		for _, part := range parts {
			vals, ok := resolveInlinePathTokens(part, vars, depth+1)
			if !ok {
				return nil, false
			}
			next, ok := cartesianConcat(acc, vals, false)
			if !ok {
				return nil, false
			}
			acc = next
		}
		return acc, true
	}
	if len(expr) == 1 {
		switch expr[0].kind {
		case "str":
			return []string{expr[0].val}, true
		case "id":
			vals, ok := vars[expr[0].val]
			return vals, ok && len(vals) > 0
		case "tpl":
			acc := []string{expr[0].parts[0]}
			for idx, sub := range expr[0].exprs {
				vals, ok := resolveInlinePathTokens(sub, vars, depth+1)
				if !ok {
					return nil, false
				}
				acc, ok = cartesianConcat(acc, vals, false)
				if !ok {
					return nil, false
				}
				suffix := expr[0].parts[idx+1]
				if suffix != "" {
					for m := range acc {
						acc[m] += suffix
					}
				}
			}
			return acc, true
		}
		return nil, false
	}
	// `path.join(...)`, `posixpath.join(...)`, or `os.path.join(...)`
	if args, ok := matchPathJoinCall(expr); ok {
		if len(args) == 0 {
			return nil, false
		}
		var acc []string
		for idx, arg := range args {
			vals, ok := resolveInlinePathTokens(arg, vars, depth+1)
			if !ok {
				return nil, false
			}
			if idx == 0 {
				acc = append([]string(nil), vals...)
			} else {
				acc, ok = cartesianConcat(acc, vals, true)
				if !ok {
					return nil, false
				}
			}
		}
		return acc, true
	}
	return nil, false
}

func splitTopLevelPlus(expr []inlineTok) ([][]inlineTok, bool) {
	var parts [][]inlineTok
	depth := 0
	start := 0
	for i, t := range expr {
		if t.kind == "op" {
			switch t.val {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				depth--
			case "+":
				if depth == 0 {
					if i == start {
						return nil, false
					}
					parts = append(parts, expr[start:i])
					start = i + 1
				}
			}
		}
	}
	if len(parts) == 0 {
		return nil, true
	}
	if start >= len(expr) {
		return nil, false
	}
	parts = append(parts, expr[start:])
	return parts, true
}

func cartesianConcat(left, right []string, asPathJoin bool) ([]string, bool) {
	if len(left) == 0 || len(right) == 0 || len(left)*len(right) > maxInlineCandidates {
		return nil, false
	}
	out := make([]string, 0, len(left)*len(right))
	for _, l := range left {
		for _, r := range right {
			if asPathJoin {
				out = append(out, joinInlinePosixPath(l, r))
			} else {
				out = append(out, l+r)
			}
		}
	}
	return out, true
}

func matchPathJoinCall(expr []inlineTok) ([][]inlineTok, bool) {
	callOpen := -1
	if len(expr) >= 5 && expr[0].kind == "id" && (expr[0].val == "path" || expr[0].val == "posixpath") &&
		expr[1].val == "." && expr[2].kind == "id" && expr[2].val == "join" && expr[3].val == "(" {
		callOpen = 3
	} else if len(expr) >= 7 && expr[0].kind == "id" && expr[0].val == "os" &&
		expr[1].val == "." && expr[2].kind == "id" && expr[2].val == "path" &&
		expr[3].val == "." && expr[4].kind == "id" && expr[4].val == "join" && expr[5].val == "(" {
		callOpen = 5
	}
	if callOpen < 0 || matchingCloseParen(expr, callOpen) != len(expr)-1 {
		return nil, false
	}
	return splitCallArgs(expr[callOpen+1 : len(expr)-1])
}

func splitCallArgs(inner []inlineTok) ([][]inlineTok, bool) {
	if len(inner) == 0 {
		return nil, true
	}
	var args [][]inlineTok
	depth := 0
	start := 0
	for i, t := range inner {
		if t.kind == "op" {
			switch t.val {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				if depth <= 0 {
					return nil, false
				}
				depth--
			case ",":
				if depth == 0 {
					if i == start {
						return nil, false
					}
					args = append(args, inner[start:i])
					start = i + 1
				}
			}
		}
	}
	if depth != 0 {
		return nil, false
	}
	if start < len(inner) {
		args = append(args, inner[start:])
	}
	for _, a := range args {
		if len(a) > 0 && a[0].kind == "op" && (a[0].val == "*" || a[0].val == "**" || a[0].val == "...") {
			return nil, false
		}
	}
	return args, true
}

func rewriteRegexExecCalls(isJS bool, regexVars map[string]bool, codeMask string) string {
	if !isJS {
		return codeMask
	}
	out := inlineLiteralRegexExecRe.ReplaceAllString(codeMask, "/_/.match(")
	for name := range regexVars {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*\.\s*exec\s*\(`)
		out = re.ReplaceAllString(out, name+".match(")
	}
	return out
}

var inlineLiteralRegexExecRe = regexp.MustCompile(`/_/\s*\.\s*exec\s*\(`)
