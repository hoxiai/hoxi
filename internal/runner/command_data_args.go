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
	"watch", "osascript")

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
	gitGlobalValueFlags = setOf("-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path")
)

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
	for _, tok := range tokens {
		if tok.separator {
			if len(cur) > 0 {
				segments = append(segments, cur)
				pipedIn = append(pipedIn, lastSep == "|")
			}
			cur = nil
			lastSep = command[tok.start:tok.end]
			continue
		}
		cur = append(cur, tok)
	}
	if len(cur) > 0 {
		segments = append(segments, cur)
		pipedIn = append(pipedIn, lastSep == "|")
	}
	echoWritesFile := false
	for _, seg := range segments {
		if isEchoSegment(seg) {
			for _, w := range seg[1:] {
				if strings.ContainsAny(w.value, "<>") {
					echoWritesFile = true
				}
			}
		}
	}
	for i, seg := range segments {
		name := filepath.Base(seg[0].value)
		if dataArgInterpreters[name] {
			return command
		}
		if dataScriptInterpreters[name] && (echoWritesFile || (pipedIn[i] && i > 0 && isEchoSegment(segments[i-1]))) {
			return command
		}
	}
	var masked []dataToken
	for _, seg := range segments {
		for _, i := range dataArgumentIndexes(seg) {
			// A redirect target is a path, never data.
			if seg[i].literal && !strings.ContainsAny(seg[i-1].value, "<>") {
				masked = append(masked, seg[i])
			}
		}
	}
	if len(masked) == 0 {
		return command
	}
	var b strings.Builder
	last := 0
	for _, tok := range masked {
		b.WriteString(command[last:tok.start])
		b.WriteString(maskedDataArgument)
		last = tok.end
	}
	b.WriteString(command[last:])
	return b.String()
}

// tokenizeDataCommand splits a command into words and separators, recording
// each word's raw span. It refuses (ok=false) unbalanced quotes and grouping
// parentheses, which it does not model.
func tokenizeDataCommand(s string) ([]dataToken, bool) {
	var tokens []dataToken
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
			continue
		case c == '(' || c == ')':
			return nil, false
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
			if c == ' ' || c == '\t' || c == '\r' || c == ';' || c == '\n' || c == '|' || c == '&' {
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
					if s[j] == '$' {
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
			case '\\', '$', '*', '?', '[', '<', '>', '~', '{', '}', '!':
				literal = false
			}
			value.WriteByte(c)
			i++
		}
		tokens = append(tokens, dataToken{start: start, end: i, value: value.String(), literal: literal})
	}
	return tokens, true
}

func isEchoSegment(seg []dataToken) bool {
	name := filepath.Base(seg[0].value)
	return name == "echo" || name == "printf"
}

// dataArgumentIndexes returns the word indexes of seg that only carry data.
func dataArgumentIndexes(seg []dataToken) []int {
	switch filepath.Base(seg[0].value) {
	case "echo", "printf":
		out := make([]int, 0, len(seg)-1)
		for i := 1; i < len(seg) && !strings.ContainsAny(seg[i].value, "<>"); i++ {
			if !echoOptionRe.MatchString(seg[i].value) {
				out = append(out, i)
			}
		}
		return out
	case "git":
		return gitDataArguments(seg)
	case "rg":
		return searchPatternArguments(seg, rgValueFlags)
	case "grep", "egrep", "fgrep":
		return searchPatternArguments(seg, grepValueFlags)
	}
	return nil
}

func gitDataArguments(seg []dataToken) []int {
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
	var valueFlags map[string]bool
	switch sub {
	case "commit", "tag", "merge", "stash":
		valueFlags = setOf("-m", "--message")
	case "log", "shortlog":
		valueFlags = setOf("--grep", "-S", "-G")
	default:
		return nil
	}
	var out []int
	for j := i + 1; j < len(seg); j++ {
		v := seg[j].value
		if eq := strings.IndexByte(v, '='); eq > 0 && valueFlags[v[:eq]] {
			continue // `--message=text` keeps its flag; only whole words are masked
		}
		short := sub == "commit" && len(v) > 2 && v[0] == '-' && v[1] != '-' && strings.HasSuffix(v, "m")
		if (valueFlags[v] || short) && j+1 < len(seg) {
			out = append(out, j+1)
			j++
		}
	}
	return out
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
