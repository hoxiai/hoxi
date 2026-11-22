package shellpolicy

import (
	"path/filepath"
	"strings"
)

// InlineDiscoveryDirectory identifies a directory the agent can list with a
// separate file tool after a rejected Node read. It grants no access.
func InlineDiscoveryDirectory(command string, bashRules interface{}) string {
	if !strings.Contains(command, "readdirSync") || !strings.Contains(command, "readFileSync") {
		return ""
	}
	spans := scanShellWordSpans(command)
	if len(spans) != 3 || normalizeAbsoluteTool(spans[0].word) != "node" ||
		(spans[1].word != "-e" && spans[1].word != "--eval") {
		return ""
	}
	if strings.ContainsAny(command[:spans[2].start], "\r\n") {
		return ""
	}
	if decision, matched := ruleDecision(bashRules, []string{command}, permissionCategoryBash); matched && decision != "allow" {
		return ""
	}
	code, ok := unquoteStrictInlineCodeArg(command[spans[2].start:spans[2].end])
	if !ok {
		return ""
	}
	var dir string
	classifyReadOnlyScript("node", code, &dir)
	return dir
}

func listedReadDirectory(toks []inlineTok) string {
	// Recognize one literal listing and reads assembled from that directory
	// and the selected basename. Never enumerate or guess the basename here.
	var dir, name string
	sawRead := false
	for i, t := range toks {
		if t.kind == "tpl" && len(t.exprs) != 0 {
			return ""
		}
		if t.kind != "id" || !inlineFileCalls[t.val] {
			continue
		}
		switch t.val {
		case "readdirSync":
			// const name = fs.readdirSync("dir").find(...)
			if dir != "" || i < 5 || i+6 >= len(toks) ||
				toks[i-5].val != "const" || toks[i-4].kind != "id" || toks[i-3].val != "=" ||
				toks[i-1].val != "." || toks[i+1].val != "(" || toks[i+2].kind != "str" ||
				toks[i+3].val != ")" || toks[i+4].val != "." || toks[i+5].val != "find" || toks[i+6].val != "(" {
				return ""
			}
			dir = discoveryDirectoryName(toks[i+2].val)
			if !filepath.IsLocal(dir) || filepath.ToSlash(filepath.Clean(dir)) != dir || isDisallowedFileCallTarget(dir) {
				return ""
			}
			name = toks[i-4].val
		case "readFileSync":
			if dir == "" || i+1 >= len(toks) || toks[i+1].val != "(" {
				return ""
			}
			close := matchingCloseParen(toks, i+1)
			if close >= len(toks) {
				return ""
			}
			args, ok := splitCallArgs(toks[i+2 : close])
			if !ok || len(args) == 0 || !readsListedName(args[0], dir, name) {
				return ""
			}
			sawRead = true
		default:
			return ""
		}
	}
	if sawRead {
		return dir
	}
	return ""
}

// Accept ordinary relative spellings without cleaning away any ".." component.
func discoveryDirectoryName(raw string) string {
	return strings.TrimPrefix(strings.TrimSuffix(raw, "/"), "./")
}

func readsListedName(expr []inlineTok, dir, name string) bool {
	if len(expr) == 3 && expr[0].kind == "str" && strings.HasSuffix(expr[0].val, "/") && discoveryDirectoryName(expr[0].val) == dir &&
		expr[1].val == "+" && expr[2].kind == "id" && expr[2].val == name {
		return true
	}
	args, ok := matchPathJoinCall(expr)
	return ok && len(args) == 2 && len(args[0]) == 1 && args[0][0].kind == "str" && discoveryDirectoryName(args[0][0].val) == dir &&
		len(args[1]) == 1 && args[1][0].kind == "id" && args[1][0].val == name
}
