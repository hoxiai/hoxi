package runner

import (
	"path/filepath"
	"regexp"
	"strings"
)

// maskCommandDataArguments replaces literal "data" arguments (commit messages,
// search patterns, echo text) with a placeholder before the destructive
// patterns run, so `git commit -m "restore ..."` or `rg "Shutdown"` is not
// mistaken for `git restore` or a shutdown. It mirrors
// maskCommandDataArguments in .opencode/lib/command-data-args.js; the shared
// command fixture keeps both aligned.
//
// It only ever masks a whole literal word in a known data position. Anything
// it cannot read with certainty (command substitution, heredocs, escapes,
// subshells, a shell or interpreter anywhere in the chain) leaves the command
// untouched, so the full text is judged as before.

const maskedDataArgument = "Q"

type dataToken struct {
	start, end int
	value      string
	literal    bool // a whole word with no expansion, escape or glob
	separator  bool
}

// dataArgInterpreters run their arguments or input as shell commands;
// anywhere in a chain they could execute masked text (`echo ... | sh`,
// `echo ... > x; sh x`), so masking is skipped.
var dataArgInterpreters = setOf("sh", "bash", "zsh", "dash", "ksh", "fish", "eval", "exec", "source", ".",
	"xargs", "env", "sudo", "su", "doas", "nohup", "time", "command", "builtin", "ssh", "parallel",
	"watch", "osascript", "sandbox-exec")

// dataScriptInterpreters can execute text they are fed (awk system(), GNU
// sed `e`, python/node stdin). They only block masking when echo/printf text
// reaches them through a pipe or a written file.
var dataScriptInterpreters = setOf("python", "python3", "node", "perl", "ruby", "php", "awk", "sed")

var (
	rgValueFlags = setOf("-A", "-B", "-C", "-m", "-g", "-t", "-T", "-M", "-j", "-d", "-E", "-r",
		"--glob", "--iglob", "--type", "--type-not", "--type-add", "--type-clear", "--max-count",
		"--context", "--after-context", "--before-context", "--max-columns", "--threads",
		"--max-depth", "--maxdepth", "--sort", "--sortr", "--color", "--colors", "--encoding",
		"--replace", "--max-filesize", "--path-separator", "--context-separator", "--engine",
		"--pre", "--pre-glob", "--dfa-size-limit", "--regex-size-limit", "--field-match-separator")
	grepValueFlags = setOf("-A", "-B", "-C", "-m", "-d", "-D", "--include", "--exclude",
		"--exclude-dir", "--exclude-from", "--max-count", "--context", "--after-context",
		"--before-context", "--color", "--colour", "--label", "--binary-files", "--devices",
		"--directories", "--group-separator")
	echoOptionRe        = regexp.MustCompile(`^-[neE]+$`)
	dataEnvAssignRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	gitGlobalValueFlags = setOf("-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path")
)

func stripDataSegmentEnv(seg []dataToken) []dataToken {
	for len(seg) > 0 {
		k := 0
		for k < len(seg) && dataEnvAssignRe.MatchString(seg[k].value) && !strings.ContainsAny(seg[k].value, "<>") {
			k++
		}
		if k > 0 && k < len(seg) {
			seg = seg[k:]
		}
		if !seg[0].literal || len(seg) < 2 {
			return seg
		}
		switch filepath.Base(seg[0].value) {
		case "do", "then", "else", "elif", "{", "!":
			seg = seg[1:]
		case "nohup", "time":
			i := 1
			for i < len(seg) && strings.HasPrefix(seg[i].value, "-") {
				if !seg[i].literal {
					return seg
				}
				if seg[i].value == "--" {
					i++
					break
				}
				i++
			}
			if i >= len(seg) || !seg[i].literal {
				return seg
			}
			seg = seg[i:]
		case "command":
			i := 1
			for i < len(seg) && strings.HasPrefix(seg[i].value, "-") {
				if !seg[i].literal || seg[i].value == "-v" || seg[i].value == "-V" {
					return seg
				}
				if seg[i].value == "--" {
					i++
					break
				}
				i++
			}
			if i >= len(seg) || !seg[i].literal {
				return seg
			}
			seg = seg[i:]
		case "env":
			i := 1
			for i < len(seg) {
				v := seg[i].value
				if !seg[i].literal || v == "-S" || strings.HasPrefix(v, "--split-string") || (strings.HasPrefix(v, "-") && !strings.HasPrefix(v, "--") && strings.Contains(v, "S")) {
					return seg
				}
				if v == "-u" || v == "-P" || v == "-C" || v == "--unset" || v == "--chdir" {
					if i+1 >= len(seg) || !seg[i+1].literal {
						return seg
					}
					i += 2
					continue
				}
				if v == "--" {
					i++
					break
				}
				if strings.HasPrefix(v, "-") || (dataEnvAssignRe.MatchString(v) && !strings.ContainsAny(v, "<>")) {
					i++
					continue
				}
				break
			}
			if i >= len(seg) || !seg[i].literal {
				return seg
			}
			seg = seg[i:]
		case "timeout", "gtimeout":
			i := 1
			for i < len(seg) && strings.HasPrefix(seg[i].value, "-") {
				v := seg[i].value
				if !seg[i].literal {
					return seg
				}
				if v == "--" {
					i++
					break
				}
				if v == "-s" || v == "--signal" || v == "-k" || v == "--kill-after" {
					if i+1 >= len(seg) || !seg[i+1].literal {
						return seg
					}
					i++
				}
				i++
			}
			if i >= len(seg) || !seg[i].literal {
				return seg
			}
			i++ // duration positional arg
			if i >= len(seg) || !seg[i].literal {
				return seg
			}
			seg = seg[i:]
		default:
			return seg
		}
	}
	return seg
}

func isDoubleQuoteDollarExpansion(next byte) bool {
	if (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') || (next >= '0' && next <= '9') || next == '_' {
		return true
	}
	switch next {
	case '(', '{', '@', '*', '#', '?', '$', '!', '-':
		return true
	}
	return false
}

func maskCommandDataArguments(command string) string {
	if strings.Contains(command, "`") || strings.Contains(command, "$(") || strings.Contains(command, "<<") {
		return command
	}
	tokens, ok := tokenizeDataCommand(command)
	if !ok {
		return command
	}
	var segments [][]dataToken
	var pipedIn []bool // whether segment i reads the previous segment's output
	var cur []dataToken
	lastSep := ""
	hasParen := false
	for _, tok := range tokens {
		if tok.separator {
			sepText := command[tok.start:tok.end]
			if sepText == "(" || sepText == ")" {
				hasParen = true
			}
			if len(cur) > 0 {
				segments = append(segments, stripDataSegmentEnv(cur))
				pipedIn = append(pipedIn, lastSep == "|")
				cur = nil
				lastSep = sepText
			} else if sepText != "(" && sepText != ")" {
				lastSep = sepText
			}
			continue
		}
		cur = append(cur, tok)
	}
	if len(cur) > 0 {
		segments = append(segments, stripDataSegmentEnv(cur))
		pipedIn = append(pipedIn, lastSep == "|")
	}
	hasEcho := false
	echoWritesFile := false
	for _, seg := range segments {
		if isEchoSegment(seg) {
			hasEcho = true
			for _, w := range seg[1:] {
				if strings.ContainsAny(w.value, "<>") {
					echoWritesFile = true
				}
			}
		}
	}
	echoInPipe := false
	for i, seg := range segments {
		if !pipedIn[i] {
			echoInPipe = false
		}
		name := filepath.Base(seg[0].value)
		if !seg[0].literal || dataArgInterpreters[name] {
			return command
		}
		if dataScriptInterpreters[name] && (echoWritesFile || echoInPipe || (hasEcho && hasParen)) {
			return command
		}
		if isEchoSegment(seg) {
			echoInPipe = true
		}
	}
	var masked []dataReplacement
	for _, seg := range segments {
		for _, rep := range dataArgumentReplacements(seg) {
			masked = append(masked, rep)
		}
	}
	if len(masked) == 0 {
		return command
	}
	var b strings.Builder
	last := 0
	for _, rep := range masked {
		b.WriteString(command[last:rep.start])
		b.WriteString(rep.text)
		last = rep.end
	}
	b.WriteString(command[last:])
	return b.String()
}

// tokenizeDataCommand splits a command into words and separators, recording
// each word's raw span. It refuses (ok=false) unbalanced quotes and mid-word
// parentheses, while allowing balanced subshell grouping parentheses.
func tokenizeDataCommand(s string) ([]dataToken, bool) {
	var tokens []dataToken
	parenDepth := 0
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
			continue
		case c == '(':
			parenDepth++
			tokens = append(tokens, dataToken{start: i, end: i + 1, separator: true})
			i++
			continue
		case c == ')':
			if parenDepth <= 0 {
				return nil, false
			}
			parenDepth--
			tokens = append(tokens, dataToken{start: i, end: i + 1, separator: true})
			i++
			continue
		case c == ';' || c == '\n' || c == '|' || c == '&':
			j := i + 1
			if j < len(s) && (s[j] == '|' || s[j] == '&') {
				j++
			}
			tokens = append(tokens, dataToken{start: i, end: j, separator: true})
			i = j
			continue
		}
		start := i
		var value strings.Builder
		literal := true
		for i < len(s) {
			c = s[i]
			if c == ' ' || c == '\t' || c == '\r' || c == ';' || c == '\n' || c == '|' || c == '&' || (c == ')' && parenDepth > 0) {
				break
			}
			switch c {
			case '(', ')':
				return nil, false
			case '\'':
				end := strings.IndexByte(s[i+1:], '\'')
				if end < 0 {
					return nil, false
				}
				value.WriteString(s[i+1 : i+1+end])
				i += end + 2
				if strings.HasPrefix(s[i:], `\''`) {
					value.WriteByte('\'')
					i += 2
				}
				continue
			case '"':
				j := i + 1
				closed := false
				for j < len(s) {
					if s[j] == '\\' && j+1 < len(s) {
						value.WriteByte(s[j+1])
						j += 2
						continue
					}
					if s[j] == '"' {
						closed = true
						break
					}
					if s[j] == '$' && j+1 < len(s) && isDoubleQuoteDollarExpansion(s[j+1]) {
						literal = false
					}
					value.WriteByte(s[j])
					j++
				}
				if !closed {
					return nil, false
				}
				i = j + 1
				continue
			case '\\':
				literal = false
				if i+1 < len(s) {
					value.WriteByte(s[i+1])
					i += 2
					continue
				}
			case '$', '*', '?', '[', '<', '>', '~', '{', '}', '!':
				literal = false
			}
			value.WriteByte(c)
			i++
		}
		tokens = append(tokens, dataToken{start: start, end: i, value: value.String(), literal: literal})
	}
	if parenDepth != 0 {
		return nil, false
	}
	return tokens, true
}

func isEchoSegment(seg []dataToken) bool {
	name := filepath.Base(seg[0].value)
	return name == "echo" || name == "printf"
}

type dataReplacement struct {
	start int
	end   int
	text  string
}

func appendWholeWordMask(out []dataReplacement, seg []dataToken, idx int) []dataReplacement {
	if idx > 0 && idx < len(seg) && seg[idx].literal && !strings.ContainsAny(seg[idx-1].value, "<>") {
		out = append(out, dataReplacement{start: seg[idx].start, end: seg[idx].end, text: maskedDataArgument})
	}
	return out
}

// dataArgumentReplacements returns the literal data spans of seg to mask.
func dataArgumentReplacements(seg []dataToken) []dataReplacement {
	var idxs []int
	switch filepath.Base(seg[0].value) {
	case "echo", "printf":
		for i := 1; i < len(seg) && !strings.ContainsAny(seg[i].value, "<>"); i++ {
			if !echoOptionRe.MatchString(seg[i].value) {
				idxs = append(idxs, i)
			}
		}
	case "git":
		return gitDataArguments(seg)
	case "gh":
		return ghDataArguments(seg)
	case "go":
		if len(seg) >= 2 && seg[1].value == "test" {
			return flagValueReplacements(seg, 2, setOf("-run", "--run", "-bench", "--bench", "-skip", "--skip"), false)
		}
	case "pytest":
		return flagValueReplacements(seg, 1, setOf("-k"), false)
	case "vitest":
		return flagValueReplacements(seg, 1, setOf("-t", "--testNamePattern"), false)
	case "npx":
		if len(seg) >= 2 && seg[1].value == "vitest" {
			return flagValueReplacements(seg, 2, setOf("-t", "--testNamePattern"), false)
		}
	case "rg":
		idxs = searchPatternArguments(seg, rgValueFlags)
	case "grep", "egrep", "fgrep":
		idxs = searchPatternArguments(seg, grepValueFlags)
	}
	var out []dataReplacement
	for _, i := range idxs {
		out = appendWholeWordMask(out, seg, i)
	}
	return out
}

func flagValueReplacements(seg []dataToken, startIdx int, valueFlags map[string]bool, allowCommitShortM bool) []dataReplacement {
	var out []dataReplacement
	for j := startIdx; j < len(seg); j++ {
		v := seg[j].value
		if eq := strings.IndexByte(v, '='); eq > 0 && valueFlags[v[:eq]] {
			if len(v) > eq+1 && seg[j].literal && !strings.ContainsAny(seg[j-1].value, "<>") {
				out = append(out, dataReplacement{start: seg[j].start, end: seg[j].end, text: v[:eq] + "=" + maskedDataArgument})
			}
			continue
		}
		if allowCommitShortM && strings.HasPrefix(v, "-m") && len(v) > 2 && !strings.HasPrefix(v, "--") {
			if seg[j].literal && !strings.ContainsAny(seg[j-1].value, "<>") {
				out = append(out, dataReplacement{start: seg[j].start, end: seg[j].end, text: "-m " + maskedDataArgument})
			}
			continue
		}
		short := allowCommitShortM && len(v) > 2 && v[0] == '-' && v[1] != '-' && strings.HasSuffix(v, "m")
		if (valueFlags[v] || short) && j+1 < len(seg) {
			out = appendWholeWordMask(out, seg, j+1)
			j++
		}
	}
	return out
}

func gitDataArguments(seg []dataToken) []dataReplacement {
	i := 1
	for i < len(seg) && strings.HasPrefix(seg[i].value, "-") {
		if gitGlobalValueFlags[seg[i].value] {
			i++
		}
		i++
	}
	if i >= len(seg) {
		return nil
	}
	sub := seg[i].value
	switch sub {
	case "commit", "tag", "merge", "stash", "notes":
		return flagValueReplacements(seg, i+1, setOf("-m", "--message"), sub == "commit")
	case "log", "shortlog":
		return flagValueReplacements(seg, i+1, setOf("--grep", "-S", "-G"), false)
	}
	return nil
}

func ghDataArguments(seg []dataToken) []dataReplacement {
	i := 1
	for i < len(seg) && strings.HasPrefix(seg[i].value, "-") {
		if seg[i].value == "-R" || seg[i].value == "--repo" {
			i += 2
			continue
		}
		if strings.HasPrefix(seg[i].value, "--repo=") {
			i++
			continue
		}
		return nil
	}
	if i+1 >= len(seg) {
		return nil
	}
	entity, action := seg[i].value, seg[i+1].value
	if (entity != "pr" && entity != "issue" && entity != "release") ||
		(action != "create" && action != "edit" && action != "comment" && action != "close" && action != "reopen") {
		return nil
	}
	return flagValueReplacements(seg, i+2, setOf("-t", "--title", "-b", "--body", "-n", "--notes"), false)
}

func searchPatternArguments(seg []dataToken, valueFlags map[string]bool) []int {
	var explicit []int
	positional := -1
	usesPatternFile := false
	endOfOptions := false
	for j := 1; j < len(seg); j++ {
		v := seg[j].value
		if !endOfOptions && strings.HasPrefix(v, "-") && v != "-" {
			switch {
			case v == "--":
				endOfOptions = true
			case v == "-e" || v == "--regexp":
				if j+1 < len(seg) {
					explicit = append(explicit, j+1)
					j++
				}
			case v == "-f" || v == "--file" || strings.HasPrefix(v, "--file="):
				usesPatternFile = true
				if v == "-f" || v == "--file" {
					j++
				}
			case valueFlags[v]:
				j++
			}
			continue
		}
		if positional < 0 {
			positional = j
		}
	}
	if len(explicit) > 0 || usesPatternFile {
		return explicit
	}
	if positional > 0 {
		return []int{positional}
	}
	return nil
}

var (
	shellCFlagRe       = regexp.MustCompile(`^-[leuxf]*c$`)
	shellSafeOptFlagRe = regexp.MustCompile(`^-[leuxfo]+$`)
)

// unwrapLiteralShellCommand unwraps a top-level `sh/bash/zsh -c '<literal>'`
// (or double-quoted script without command substitutions) so data arguments
// inside the script are masked and literal loops/assignments are expanded
// before destructive regexes run.
func unwrapLiteralShellCommand(s string) string {
	unwrappedCount := 0
	validChain := true
	res := mapTopLevelSegments(s, func(seg string) string {
		if inner, ok := unwrapLiteralShellSegment(seg); ok {
			unwrappedCount++
			return inner
		}
		trimmed := strings.TrimSpace(seg)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "trap ") || cdSegmentRe.MatchString(stripCDNullRedirects(trimmed)) {
			return seg
		}
		validChain = false
		return seg
	})
	if unwrappedCount != 1 || !validChain {
		return s
	}
	return res
}

func unwrapLiteralShellSegment(seg string) (string, bool) {
	s := strings.TrimSpace(seg)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", false
	}
	spans := scanShellWordSpans(s)
	if len(spans) < 3 {
		return "", false
	}
	idx := 0
	for idx < len(spans) && envVarAssignmentWordRe.MatchString(spans[idx].word) && !strings.ContainsAny(spans[idx].word, "$`") {
		idx++
	}
	if idx >= len(spans) {
		return "", false
	}
	base := filepath.Base(normalizeAbsoluteTool(spans[idx].word))
	if base != "sh" && base != "bash" && base != "zsh" {
		return "", false
	}
	idx++
	foundC := false
	for idx < len(spans) {
		w := spans[idx].word
		if w == "-o" && idx+1 < len(spans) && (spans[idx+1].word == "pipefail" || spans[idx+1].word == "errexit") {
			idx += 2
			continue
		}
		if shellCFlagRe.MatchString(w) {
			foundC = true
			idx++
			break
		}
		if shellSafeOptFlagRe.MatchString(w) && !strings.Contains(w, "o") {
			idx++
			continue
		}
		return "", false
	}
	if !foundC || idx >= len(spans) {
		return "", false
	}
	rawScript := s[spans[idx].start:spans[idx].end]
	idx++
	for idx < len(spans) {
		w := spans[idx].word
		if devNullWordRe.MatchString(w) || dupStreamWordRe.MatchString(w) {
			idx++
			continue
		}
		if redirectWordRe.MatchString(w) && idx+1 < len(spans) && spans[idx+1].word == "/dev/null" {
			idx += 2
			continue
		}
		return "", false
	}
	inner, ok := unquoteLiteralShellScriptArg(rawScript)
	if !ok || strings.TrimSpace(inner) == "" {
		return "", false
	}
	return inner, true
}

type shellWordSpan struct {
	start, end int
	word       string
}

func scanShellWordSpans(s string) []shellWordSpan {
	var out []shellWordSpan
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		var b strings.Builder
		var quote byte
		for i < len(s) {
			c := s[i]
			if quote == 0 && (c == ' ' || c == '\t' || c == '\r' || c == '\n') {
				break
			}
			if quote == '\'' {
				if c == '\'' {
					quote = 0
				} else {
					b.WriteByte(c)
				}
				i++
				continue
			}
			if quote == '"' {
				if c == '\\' && i+1 < len(s) {
					b.WriteByte(s[i+1])
					i += 2
					continue
				}
				if c == '"' {
					quote = 0
					i++
					continue
				}
				b.WriteByte(c)
				i++
				continue
			}
			if c == '\'' || c == '"' {
				quote = c
				i++
				continue
			}
			if c == '\\' && i+1 < len(s) {
				b.WriteByte(s[i+1])
				i += 2
				continue
			}
			b.WriteByte(c)
			i++
		}
		if quote != 0 {
			return nil
		}
		out = append(out, shellWordSpan{start: start, end: i, word: b.String()})
	}
	return out
}

func unquoteLiteralShellScriptArg(raw string) (string, bool) {
	if len(raw) < 2 {
		return "", false
	}
	if raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		var b strings.Builder
		i := 0
		for i < len(raw) {
			if raw[i] != '\'' {
				return "", false
			}
			end := strings.IndexByte(raw[i+1:], '\'')
			if end < 0 {
				return "", false
			}
			b.WriteString(raw[i+1 : i+1+end])
			i += end + 2
			if i == len(raw) {
				return b.String(), true
			}
			if strings.HasPrefix(raw[i:], `\'`) {
				b.WriteByte('\'')
				i += 2
			} else {
				return "", false
			}
		}
		return "", false
	}
	if raw[0] == '"' && raw[len(raw)-1] == '"' {
		inner := raw[1 : len(raw)-1]
		var b strings.Builder
		for i := 0; i < len(inner); i++ {
			c := inner[i]
			if c == '"' || c == '`' {
				return "", false
			}
			if c == '$' && i+1 < len(inner) && inner[i+1] == '(' {
				return "", false
			}
			if c == '\\' && i+1 < len(inner) {
				next := inner[i+1]
				switch next {
				case '"', '\\', '$', '`':
					b.WriteByte(next)
					i++
					continue
				case '\n':
					i++
					continue
				}
			}
			b.WriteByte(c)
		}
		out := b.String()
		if strings.Contains(out, "`") || strings.Contains(out, "$(") {
			return "", false
		}
		return out, true
	}
	return "", false
}
