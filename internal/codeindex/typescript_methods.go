package codeindex

import (
	"path/filepath"
	"strings"
	"text/scanner"
	"unicode"
)

func isTypeScript(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".tsx", ".mts", ".cts":
		return true
	}
	return false
}

type tsToken struct {
	text  string
	line  int
	ident bool
}

// Index named class methods from complete files so a chunk boundary or a
// multiline parameter list cannot split a declaration from its class.
func typeScriptMethods(path, source string) []symbolDecl {
	if !isTypeScript(path) || !strings.Contains(source, "class") {
		return nil
	}
	ts, pairs := tsMethodTokens(source)
	var methods []symbolDecl
	for i := 0; i+2 < len(ts); i++ {
		if ts[i].text != "class" || !ts[i+1].ident {
			continue
		}
		owner := ts[i+1].text
		body := i + 2
		for body < len(ts) && ts[body].text != "{" && ts[body].text != ";" {
			if ts[body].text == "<" {
				end := tsGenericEnd(ts, body, len(ts), pairs)
				if end < 0 {
					break
				}
				body = end + 1
			} else {
				body++
			}
		}
		end, ok := pairs[body]
		if !ok || ts[body].text != "{" {
			continue
		}
		for j := body + 1; j < end; j++ {
			if close, ok := pairs[j]; ok {
				j = close
				continue
			}
			if !ts[j].ident {
				continue
			}
			prev := ts[j-1].text
			if prev == "." || prev == "=" || prev == "@" {
				continue
			}
			pos := j + 1
			if pos < end && ts[pos].text == "<" {
				close := tsGenericEnd(ts, pos, end, pairs)
				if close < 0 {
					continue
				}
				pos = close + 1
			}
			if pos >= end || ts[pos].text != "(" || pairs[pos] <= pos {
				continue
			}
			pos = pairs[pos] + 1
			if pos < end && ts[pos].text == ":" {
				pos++
			returnType:
				for pos < end {
					switch ts[pos].text {
					case "{":
						prev := ts[pos-1].text
						if prev != ":" && prev != "|" && prev != "&" && prev != "?" {
							break returnType
						}
					case ";", "=":
						break returnType
					case "<":
						close := tsGenericEnd(ts, pos, end, pairs)
						if close < 0 {
							break returnType
						}
						pos = close + 1
						continue
					}
					if close, ok := pairs[pos]; ok {
						pos = close
					}
					pos++
				}
			}
			close, ok := pairs[pos]
			if !ok || ts[pos].text != "{" || close >= end {
				continue
			}
			methods = append(methods, symbolDecl{Path: path, Receiver: owner, Symbol: ts[j].text, StartLine: ts[j].line, EndLine: ts[close].line})
			j = close
		}
	}
	return methods
}

func tsMethodTokens(source string) ([]tsToken, map[int]int) {
	var lex scanner.Scanner
	lex.Init(strings.NewReader(source))
	lex.IsIdentRune = func(r rune, i int) bool {
		return r == '$' || r == '_' || unicode.IsLetter(r) || i > 0 && (unicode.IsDigit(r) || unicode.IsMark(r))
	}
	lex.Mode = scanner.ScanIdents | scanner.ScanInts | scanner.ScanStrings | scanner.ScanChars | scanner.ScanRawStrings | scanner.ScanComments | scanner.SkipComments
	// JavaScript single-quoted strings may contain more than one character.
	lex.Error = func(*scanner.Scanner, string) {}
	var ts []tsToken
	pairs := map[int]int{}
	var stack []int
	for tok := lex.Scan(); tok != scanner.EOF; tok = lex.Scan() {
		word := lex.TokenText()
		if tok == '/' && len(ts) > 0 && tsRegexFollows(ts[len(ts)-1].text) {
			// A brace inside a regular expression does not end a method.
			inClass := false
			for ch := lex.Peek(); ch != scanner.EOF && ch != '\n'; ch = lex.Peek() {
				lex.Next()
				switch ch {
				case '\\':
					lex.Next()
				case '[':
					inClass = true
				case ']':
					inClass = false
				}
				if ch == '/' && !inClass {
					break
				}
			}
		}
		i := len(ts)
		ts = append(ts, tsToken{word, lex.Position.Line, tok == scanner.Ident})
		switch word {
		case "{", "(", "[":
			stack = append(stack, i)
		case "}", ")", "]":
			if len(stack) > 0 {
				open := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if (ts[open].text == "{" && word == "}") || (ts[open].text == "(" && word == ")") || (ts[open].text == "[" && word == "]") {
					pairs[open] = i
				}
			}
		}
	}
	return ts, pairs
}

func tsRegexFollows(prev string) bool {
	switch prev {
	case "=", ":", ",", "(", "[", "return", "throw", "!", "?":
		return true
	}
	return false
}

func tsGenericEnd(tokens []tsToken, start, limit int, pairs map[int]int) int {
	depth := 0
	for i := start; i < limit; i++ {
		if close, ok := pairs[i]; ok {
			i = close
			continue
		}
		switch tokens[i].text {
		case "<":
			depth++
		case ">":
			depth--
			if depth == 0 {
				return i
			}
		case ";":
			return -1
		}
	}
	return -1
}

func findFileSymbolInLines(path string, lines []string, symbol string) (int, int, string, bool) {
	if isTypeScript(path) {
		qualified := strings.Contains(symbol, ".")
		var match *symbolDecl
		methods := typeScriptMethods(path, strings.Join(lines, "\n"))
		for i := range methods {
			m := &methods[i]
			if (!qualified && m.Symbol == symbol) || m.Receiver+"."+m.Symbol == symbol {
				if match != nil {
					return 0, 0, "", false
				}
				match = m
			}
		}
		if match != nil {
			return match.StartLine, match.EndLine, match.Receiver + "." + match.Symbol, true
		}
		if qualified {
			return 0, 0, "", false
		}
	}
	return findSymbolInLines(lines, symbol)
}
