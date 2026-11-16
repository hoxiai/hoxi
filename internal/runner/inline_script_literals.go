package runner

import (
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxInlineLexDepth   = 4
	maxInlinePathDepth  = 6
	maxInlineCandidates = 64
)

type inlineTok struct {
	kind  string // "id", "str", "tpl", "re", "num", "op"
	val   string
	start int
	end   int
	parts []string
	exprs [][]inlineTok
}

var (
	jsExprKeywords = setOf("return", "typeof", "void", "delete", "throw", "case", "in", "of",
		"instanceof", "new", "yield", "await", "else", "do")
	jsControlKeywords = setOf("if", "while", "for", "switch", "catch")
	inlineAssignOps   = setOf("=", "+=", "-=", "*=", "/=", "%=", "|=", "&=", "^=", "<<=", ">>=", ">>>=", "**=", "??=", "||=", "&&=", ":=")
	inlineReadModes   = setOf("r", "rt", "rb", "tr", "br", "U", "rU")
	inlineOpenKwSafe  = setOf("encoding", "errors", "newline", "buffering")
	inlineFileCalls   = setOf("open", "openSync", "Path", "PurePath", "PosixPath", "WindowsPath", "joinpath",
		"readFileSync", "readFile", "realpathSync", "realpath", "statSync", "lstatSync", "readdirSync",
		"accessSync", "existsSync", "createReadStream", "scandir", "listdir", "lstat", "readlink")
	inlineZeroArgPathCalls = setOf("Path", "PurePath", "PosixPath", "WindowsPath", "listdir", "scandir")
)

func isIdentStart(c byte, allowDollar bool) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (allowDollar && c == '$')
}

func isIdentPart(c byte, allowDollar bool) bool {
	return isIdentStart(c, allowDollar) || (c >= '0' && c <= '9')
}

// lexInlineScript tokenizes inline Python or Node code, returning its tokens,
// a masked code string (comments -> spaces, strings -> "_", templates -> tpl(...),
// JS regex literals -> /_/), and all decoded string literals (including folded
// adjacent string concatenations). Any unterminated construct returns ok=false.
func lexInlineScript(family, code string) ([]inlineTok, string, []string, bool) {
	isJS := family == "node"
	toks, mask, lits, end, ok := lexInlineSpan(isJS, code, 0, false, 0)
	if !ok || end != len(code) {
		return nil, "", nil, false
	}
	lits = appendFoldedStringLiterals(toks, lits)
	return toks, mask, lits, true
}

func lexInlineSpan(isJS bool, code string, start int, stopAtBrace bool, depth int) ([]inlineTok, string, []string, int, bool) {
	if depth > maxInlineLexDepth {
		return nil, "", nil, 0, false
	}
	var toks []inlineTok
	var mask strings.Builder
	var lits []string
	prevSig := ""
	var prevIdent string
	var parenCtrl []bool
	braceDepth := 0
	i := start

	for i < len(code) {
		c := code[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			mask.WriteByte(c)
			i++
			continue
		}
		// Comments
		if !isJS && c == '#' {
			for i < len(code) && code[i] != '\n' {
				mask.WriteByte(' ')
				i++
			}
			continue
		}
		if isJS && c == '/' && i+1 < len(code) && code[i+1] == '/' {
			for i < len(code) && code[i] != '\n' {
				mask.WriteByte(' ')
				i++
			}
			continue
		}
		if isJS && c == '/' && i+1 < len(code) && code[i+1] == '*' {
			end := strings.Index(code[i+2:], "*/")
			if end < 0 {
				return nil, "", nil, 0, false
			}
			for k := 0; k < end+4; k++ {
				if code[i+k] == '\n' {
					mask.WriteByte('\n')
				} else {
					mask.WriteByte(' ')
				}
			}
			i += end + 4
			continue
		}
		// Brace stop for JS ${...}
		if stopAtBrace && c == '}' && braceDepth == 0 {
			return toks, mask.String(), lits, i, true
		}
		// Python prefixed strings (r/b/u/f)
		if !isJS && strings.IndexByte("rRbBuUfF", c) >= 0 && (i == start || !isIdentPart(code[i-1], false)) {
			pLen := 1
			if i+1 < len(code) && strings.IndexByte("rRbBuUfF", code[i+1]) >= 0 {
				pLen = 2
			}
			if i+pLen < len(code) && (code[i+pLen] == '\'' || code[i+pLen] == '"') {
				pfx := strings.ToLower(code[i : i+pLen])
				isRaw := strings.Contains(pfx, "r")
				isF := strings.Contains(pfx, "f")
				if isF && strings.Contains(pfx, "b") {
					return nil, "", nil, 0, false
				}
				if isF {
					tok, mStr, subLits, nextI, ok := scanPyFString(code, i, i+pLen, isRaw, depth)
					if !ok {
						return nil, "", nil, 0, false
					}
					toks = append(toks, tok)
					mask.WriteString(mStr)
					lits = append(lits, subLits...)
					prevSig, prevIdent, i = "str", "", nextI
					continue
				}
				val, nextI, ok := scanQuotedString(false, code, i+pLen, isRaw)
				if !ok {
					return nil, "", nil, 0, false
				}
				toks = append(toks, inlineTok{kind: "str", val: val, start: i, end: nextI})
				mask.WriteString(`"_"`)
				lits = append(lits, val)
				prevSig, prevIdent, i = "str", "", nextI
				continue
			}
		}
		// Plain single/double quoted strings
		if c == '\'' || c == '"' {
			val, nextI, ok := scanQuotedString(isJS, code, i, false)
			if !ok {
				return nil, "", nil, 0, false
			}
			toks = append(toks, inlineTok{kind: "str", val: val, start: i, end: nextI})
			mask.WriteString(`"_"`)
			lits = append(lits, val)
			prevSig, prevIdent, i = "str", "", nextI
			continue
		}
		// JS template literals
		if isJS && c == '`' {
			// Refuse tagged template literals (`fn`...``); cooperative read-only code never needs them.
			if prevSig == "id" || prevSig == "close_val" || prevSig == "str" || prevSig == "re" {
				return nil, "", nil, 0, false
			}
			tok, mStr, subLits, nextI, ok := scanJSTemplate(code, i, depth)
			if !ok {
				return nil, "", nil, 0, false
			}
			toks = append(toks, tok)
			mask.WriteString(mStr)
			lits = append(lits, subLits...)
			prevSig, prevIdent, i = "str", "", nextI
			continue
		}
		// JS regex literal vs division
		if isJS && c == '/' {
			isDiv := prevSig == "id" || prevSig == "num" || prevSig == "str" || prevSig == "re" || prevSig == "close_val" || prevSig == "inc"
			if !isDiv {
				nextI, ok := scanJSRegex(code, i)
				if !ok {
					return nil, "", nil, 0, false
				}
				toks = append(toks, inlineTok{kind: "re", val: "/_/", start: i, end: nextI})
				mask.WriteString("/_/")
				prevSig, prevIdent, i = "re", "", nextI
				continue
			}
		}
		// Identifiers
		if isIdentStart(c, isJS) {
			j := i + 1
			for j < len(code) && isIdentPart(code[j], isJS) {
				j++
			}
			word := code[i:j]
			isProp := len(toks) > 0 && toks[len(toks)-1].kind == "op" && toks[len(toks)-1].val == "."
			toks = append(toks, inlineTok{kind: "id", val: word, start: i, end: j})
			mask.WriteString(word)
			if isJS && !isProp && jsExprKeywords[word] {
				prevSig = "kw_expr"
			} else {
				prevSig = "id"
			}
			if !isProp {
				prevIdent = word
			} else {
				prevIdent = ""
			}
			i = j
			continue
		}
		// Numbers
		if c >= '0' && c <= '9' {
			j := i + 1
			for j < len(code) && (isIdentPart(code[j], false) || code[j] == '.') {
				j++
			}
			toks = append(toks, inlineTok{kind: "num", val: code[i:j], start: i, end: j})
			mask.WriteString(code[i:j])
			prevSig, prevIdent, i = "num", "", j
			continue
		}
		// Operators & punctuation
		op, nextI := scanInlineOperator(code, i)
		toks = append(toks, inlineTok{kind: "op", val: op, start: i, end: nextI})
		mask.WriteString(op)
		switch op {
		case "(":
			parenCtrl = append(parenCtrl, isJS && jsControlKeywords[prevIdent])
			prevSig = "punct"
		case ")":
			isCtrl := false
			if n := len(parenCtrl); n > 0 {
				isCtrl = parenCtrl[n-1]
				parenCtrl = parenCtrl[:n-1]
			}
			if isCtrl {
				prevSig = "close_ctrl"
			} else {
				prevSig = "close_val"
			}
		case "]":
			prevSig = "close_val"
		case "{":
			braceDepth++
			prevSig = "punct"
		case "}":
			if braceDepth > 0 {
				braceDepth--
			}
			prevSig = "punct"
		case "++", "--":
			prevSig = "inc"
		default:
			prevSig = "punct"
		}
		prevIdent = ""
		i = nextI
	}
	if stopAtBrace {
		return nil, "", nil, 0, false
	}
	return toks, mask.String(), lits, i, true
}

func scanInlineOperator(code string, i int) (string, int) {
	for _, three := range []string{"===", "!==", ">>>", "<<=", ">>=", "**=", "??=", "||=", "&&=", "..."} {
		if strings.HasPrefix(code[i:], three) {
			return three, i + 3
		}
	}
	if i+4 <= len(code) && code[i:i+4] == ">>>=" {
		return ">>>=", i + 4
	}
	for _, two := range []string{"==", "!=", "<=", ">=", "=>", "++", "--", "+=", "-=", "*=", "/=", "%=", "|=", "&=", "^=", ":=", "&&", "||", "??", "**", "<<", ">>", "?."} {
		if strings.HasPrefix(code[i:], two) {
			return two, i + 2
		}
	}
	return code[i : i+1], i + 1
}

func scanQuotedString(isJS bool, code string, quotePos int, isRaw bool) (string, int, bool) {
	q := code[quotePos : quotePos+1]
	triple := !isJS && strings.HasPrefix(code[quotePos:], q+q+q)
	if triple {
		q = q + q + q
	}
	j := quotePos + len(q)
	for j < len(code) {
		if !triple && (code[j] == '\n' || code[j] == '\r') {
			return "", 0, false
		}
		if code[j] == '\\' {
			if j+1 >= len(code) {
				return "", 0, false
			}
			j += 2
			continue
		}
		if strings.HasPrefix(code[j:], q) {
			decoded, ok := decodeInlineEscapes(isJS, code[quotePos+len(q):j], isRaw)
			if !ok {
				return "", 0, false
			}
			return decoded, j + len(q), true
		}
		j++
	}
	return "", 0, false
}

func scanJSTemplate(code string, tickPos int, depth int) (inlineTok, string, []string, int, bool) {
	var parts []string
	var exprs [][]inlineTok
	var maskExprs []string
	var lits []string
	chunkStart := tickPos + 1
	j := chunkStart
	for j < len(code) {
		if code[j] == '\\' {
			if j+1 >= len(code) {
				return inlineTok{}, "", nil, 0, false
			}
			j += 2
			continue
		}
		if code[j] == '`' {
			part, ok := decodeInlineEscapes(true, code[chunkStart:j], false)
			if !ok {
				return inlineTok{}, "", nil, 0, false
			}
			parts = append(parts, part)
			if part != "" || len(exprs) == 0 {
				lits = append(lits, part)
			}
			tok := inlineTok{kind: "tpl", start: tickPos, end: j + 1, parts: parts, exprs: exprs}
			return tok, "tpl(" + strings.Join(maskExprs, ", ") + ")", lits, j + 1, true
		}
		if code[j] == '$' && j+1 < len(code) && code[j+1] == '{' {
			part, ok := decodeInlineEscapes(true, code[chunkStart:j], false)
			if !ok {
				return inlineTok{}, "", nil, 0, false
			}
			parts = append(parts, part)
			if part != "" {
				lits = append(lits, part)
			}
			subToks, subMask, subLits, closeIdx, ok := lexInlineSpan(true, code, j+2, true, depth+1)
			if !ok {
				return inlineTok{}, "", nil, 0, false
			}
			exprs = append(exprs, subToks)
			maskExprs = append(maskExprs, subMask)
			lits = append(lits, subLits...)
			j = closeIdx + 1
			chunkStart = j
			continue
		}
		j++
	}
	return inlineTok{}, "", nil, 0, false
}

func scanPyFString(code string, startPos, quotePos int, isRaw bool, depth int) (inlineTok, string, []string, int, bool) {
	q := code[quotePos : quotePos+1]
	triple := strings.HasPrefix(code[quotePos:], q+q+q)
	if triple {
		q = q + q + q
	}
	var parts []string
	var exprs [][]inlineTok
	var maskExprs []string
	var lits []string
	var curRaw strings.Builder
	j := quotePos + len(q)

	flushPart := func() (string, bool) {
		p, ok := decodeInlineEscapes(false, curRaw.String(), isRaw)
		curRaw.Reset()
		if !ok {
			return "", false
		}
		parts = append(parts, p)
		if p != "" {
			lits = append(lits, p)
		}
		return p, true
	}

	for j < len(code) {
		if !triple && (code[j] == '\n' || code[j] == '\r') {
			return inlineTok{}, "", nil, 0, false
		}
		if strings.HasPrefix(code[j:], q) {
			if _, ok := flushPart(); !ok {
				return inlineTok{}, "", nil, 0, false
			}
			tok := inlineTok{kind: "tpl", start: startPos, end: j + len(q), parts: parts, exprs: exprs}
			return tok, "tpl(" + strings.Join(maskExprs, ", ") + ")", lits, j + len(q), true
		}
		if code[j] == '\\' {
			if j+1 >= len(code) {
				return inlineTok{}, "", nil, 0, false
			}
			curRaw.WriteString(code[j : j+2])
			j += 2
			continue
		}
		if code[j] == '{' {
			if j+1 < len(code) && code[j+1] == '{' {
				curRaw.WriteByte('{')
				j += 2
				continue
			}
			if _, ok := flushPart(); !ok {
				return inlineTok{}, "", nil, 0, false
			}
			exprText, closeIdx, ok := extractPyFStringField(code, j+1)
			if !ok {
				return inlineTok{}, "", nil, 0, false
			}
			subToks, subMask, subLits, subEnd, ok := lexInlineSpan(false, exprText, 0, false, depth+1)
			if !ok || subEnd != len(exprText) {
				return inlineTok{}, "", nil, 0, false
			}
			exprs = append(exprs, subToks)
			maskExprs = append(maskExprs, subMask)
			lits = append(lits, subLits...)
			j = closeIdx + 1
			continue
		}
		if code[j] == '}' {
			if j+1 < len(code) && code[j+1] == '}' {
				curRaw.WriteByte('}')
				j += 2
				continue
			}
			return inlineTok{}, "", nil, 0, false
		}
		curRaw.WriteByte(code[j])
		j++
	}
	return inlineTok{}, "", nil, 0, false
}

// extractPyFStringField extracts the expression inside `{...}`, stripping
// top-level `!r`/`!s`/`!a` conversion and `:format` spec (refusing nested `{` in format spec).
func extractPyFStringField(code string, start int) (string, int, bool) {
	depth := 0
	exprEnd := -1
	inFmt := false
	for j := start; j < len(code); j++ {
		c := code[j]
		if c == '\'' || c == '"' {
			_, nextJ, ok := scanQuotedString(false, code, j, false)
			if !ok {
				return "", 0, false
			}
			j = nextJ - 1
			continue
		}
		if inFmt {
			if c == '{' {
				return "", 0, false
			}
			if c == '}' {
				return strings.TrimSpace(code[start:exprEnd]), j, true
			}
			continue
		}
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']':
			if depth <= 0 {
				return "", 0, false
			}
			depth--
		case '}':
			if depth == 0 {
				if exprEnd < 0 {
					exprEnd = j
				}
				expr := strings.TrimSpace(code[start:exprEnd])
				if expr == "" {
					return "", 0, false
				}
				return expr, j, true
			}
			depth--
		case '!':
			if depth == 0 && j+1 < len(code) && code[j+1] != '=' && exprEnd < 0 {
				exprEnd = j
			}
		case ':':
			if depth == 0 {
				if exprEnd < 0 {
					exprEnd = j
				}
				inFmt = true
			}
		}
	}
	return "", 0, false
}

func scanJSRegex(code string, slashPos int) (int, bool) {
	inClass := false
	for j := slashPos + 1; j < len(code); j++ {
		c := code[j]
		if c == '\n' || c == '\r' {
			return 0, false
		}
		if c == '\\' {
			if j+1 >= len(code) || code[j+1] == '\n' || code[j+1] == '\r' {
				return 0, false
			}
			j++
			continue
		}
		if c == '[' {
			inClass = true
		} else if c == ']' {
			inClass = false
		} else if c == '/' && !inClass {
			k := j + 1
			for k < len(code) && strings.IndexByte("dgimsuvy", code[k]) >= 0 {
				k++
			}
			if k < len(code) && isIdentPart(code[k], true) {
				return 0, false
			}
			return k, true
		}
	}
	return 0, false
}

func decodeInlineEscapes(isJS bool, raw string, isRaw bool) (string, bool) {
	if isRaw || strings.IndexByte(raw, '\\') < 0 {
		return raw, true
	}
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			b.WriteByte(raw[i])
			continue
		}
		if i+1 >= len(raw) {
			return "", false
		}
		i++
		c := raw[i]
		switch c {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		case '\\', '\'', '"', '`', '/':
			b.WriteByte(c)
		case '\n':
			// escaped newline
		case '\r':
			if i+1 < len(raw) && raw[i+1] == '\n' {
				i++
			}
		case 'x':
			if i+2 >= len(raw) {
				return "", false
			}
			v, err := strconv.ParseUint(raw[i+1:i+3], 16, 8)
			if err != nil {
				return "", false
			}
			b.WriteRune(rune(v))
			i += 2
		case 'u':
			if isJS && i+1 < len(raw) && raw[i+1] == '{' {
				end := strings.IndexByte(raw[i+2:], '}')
				if end <= 0 || end > 6 {
					return "", false
				}
				v, err := strconv.ParseUint(raw[i+2:i+2+end], 16, 32)
				if err != nil || v > utf8.MaxRune {
					return "", false
				}
				b.WriteRune(rune(v))
				i += 2 + end
			} else {
				if i+4 >= len(raw) {
					return "", false
				}
				v, err := strconv.ParseUint(raw[i+1:i+5], 16, 32)
				if err != nil {
					return "", false
				}
				b.WriteRune(rune(v))
				i += 4
			}
		case 'U':
			if !isJS {
				if i+8 >= len(raw) {
					return "", false
				}
				v, err := strconv.ParseUint(raw[i+1:i+9], 16, 32)
				if err != nil || v > utf8.MaxRune {
					return "", false
				}
				b.WriteRune(rune(v))
				i += 8
			} else {
				b.WriteByte('U')
			}
		case 'N':
			if !isJS && i+1 < len(raw) && raw[i+1] == '{' {
				return "", false
			}
			if isJS {
				b.WriteByte('N')
			} else {
				b.WriteString(`\N`)
			}
		default:
			if c >= '0' && c <= '7' {
				octEnd := i + 1
				for octEnd < len(raw) && octEnd < i+3 && raw[octEnd] >= '0' && raw[octEnd] <= '7' {
					octEnd++
				}
				v, _ := strconv.ParseUint(raw[i:octEnd], 8, 16)
				b.WriteRune(rune(v))
				i = octEnd - 1
			} else if isJS {
				b.WriteByte(c)
			} else {
				b.WriteByte('\\')
				b.WriteByte(c)
			}
		}
	}
	return b.String(), true
}

func appendFoldedStringLiterals(toks []inlineTok, lits []string) []string {
	for i := 0; i < len(toks); i++ {
		if toks[i].kind == "tpl" {
			for _, sub := range toks[i].exprs {
				lits = appendFoldedStringLiterals(sub, lits)
			}
			continue
		}
		if toks[i].kind != "str" {
			continue
		}
		var combined strings.Builder
		combined.WriteString(toks[i].val)
		j := i
		count := 1
		for j+2 < len(toks) && toks[j+1].kind == "op" && toks[j+1].val == "+" && toks[j+2].kind == "str" {
			combined.WriteString(toks[j+2].val)
			j += 2
			count++
		}
		if count > 1 {
			lits = append(lits, combined.String())
			i = j
		}
	}
	return lits
}

func cleanInlinePosixPath(p string) string {
	s := strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	if s == "" {
		return ""
	}
	return path.Clean(s)
}

func joinInlinePosixPath(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	bs := strings.ReplaceAll(b, `\`, "/")
	if strings.HasPrefix(bs, "/") {
		return bs
	}
	return strings.TrimRight(strings.ReplaceAll(a, `\`, "/"), "/") + "/" + bs
}
