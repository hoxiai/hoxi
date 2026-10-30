package runner

import (
	"regexp"
	"strconv"
	"strings"
)

// Process control a session may run without asking: `kill` and `pkill`
// segments whose targets Hoxi has verified are processes the same session
// started (see owned_process.go). Such a segment is replaced in place by
// `true`, and the rest of the command still goes through the full policy,
// so the mask only ever removes a verified kill and never hides anything
// else. Everything the strict grammar below does not read with certainty
// stays in the text and keeps asking.

// processControlTarget is what one kill/pkill segment would signal.
type processControlTarget struct {
	// pgrepArgs are the matching flags and pattern of a pkill, passed to
	// pgrep unchanged so the match is the one pkill itself would make.
	pgrepArgs []string
	// pids are the literal targets of a kill.
	pids []int
}

// processControlRawRe accepts the raw text of a segment only when it holds
// plain words: no redirect, expansion, escape or glob anywhere between the
// command name and its last argument.
var processControlRawRe = regexp.MustCompile(`^(?:pkill|kill)(?:[ \t]+(?:[A-Za-z0-9_./:@%+,=^-]+|'[^'\n]*'|"[^"$` + "`" + `\\!\n]*"))+$`)

// processControlWordRe finds commands that may mention process control at
// all; anything else skips the parse.
var processControlWordRe = regexp.MustCompile(`\b(?:pkill|kill)\b`)

// processControlRefusals change what `kill` or `pkill` resolves to (aliases,
// functions, sourced files, PATH, hashed paths, builtins); a command that
// touches them is never masked.
var processControlRefusalRe = regexp.MustCompile(`\b(?:alias|unalias|function|hash|enable|builtin|disable|source)\b|\(\s*\)|\bPATH\s*[+]?=|\bpath\s*[+]?=|\bpath\[|(?:^|[;&|\n(])\s*\.\s`)

var processControlSignals = setOf("1", "2", "3", "9", "15", "HUP", "INT", "QUIT", "KILL", "TERM",
	"USR1", "USR2", "STOP", "CONT", "SIGHUP", "SIGINT", "SIGQUIT", "SIGKILL", "SIGTERM", "SIGUSR1",
	"SIGUSR2", "SIGSTOP", "SIGCONT")

// parseProcessControlSegment reads one simple command as a kill or pkill in
// the narrow form this file supports.
func parseProcessControlSegment(seg *uSeg) (processControlTarget, bool) {
	if seg == nil || len(seg.words) < 2 || seg.pipedIn || seg.stdin != "" {
		return processControlTarget{}, false
	}
	for _, w := range seg.words {
		if w.dynamic || w.procSub {
			return processControlTarget{}, false
		}
	}
	switch seg.words[0].text {
	case "pkill":
		return parsePkillWords(seg.words[1:])
	case "kill":
		return parseKillWords(seg.words[1:])
	}
	return processControlTarget{}, false
}

// parsePkillWords accepts `pkill [-SIGNAL] [-f] [-x] [-i] PATTERN`, with the
// letter flags in any grouping. Flags that widen or move the match (-u, -g,
// -P, -t, -v, -F …) are refused.
func parsePkillWords(words []uWord) (processControlTarget, bool) {
	var flags []string
	signalSeen := false
	pattern := ""
	for i, w := range words {
		v := w.text
		if i == len(words)-1 {
			if v == "" || strings.HasPrefix(v, "-") || strings.TrimSpace(v) != v {
				return processControlTarget{}, false
			}
			pattern = v
			break
		}
		if !strings.HasPrefix(v, "-") || len(v) < 2 {
			return processControlTarget{}, false
		}
		body := v[1:]
		if processControlSignals[strings.ToUpper(body)] {
			if signalSeen {
				return processControlTarget{}, false
			}
			signalSeen = true
			continue
		}
		for _, r := range body {
			switch r {
			case 'f', 'x', 'i':
			default:
				return processControlTarget{}, false
			}
		}
		flags = append(flags, v)
	}
	if pattern == "" {
		return processControlTarget{}, false
	}
	return processControlTarget{pgrepArgs: append(flags, pattern)}, true
}

// parseKillWords accepts `kill [-SIGNAL | -s SIGNAL] PID…` with literal
// positive PIDs. Process groups (`kill -- -123`), job specs and `kill -l`
// are refused.
func parseKillWords(words []uWord) (processControlTarget, bool) {
	var pids []int
	i := 0
	if v := words[0].text; strings.HasPrefix(v, "-") {
		switch {
		case v == "-s" && len(words) > 1 && processControlSignals[strings.ToUpper(words[1].text)]:
			i = 2
		case processControlSignals[strings.ToUpper(v[1:])]:
			i = 1
		default:
			return processControlTarget{}, false
		}
	}
	if i >= len(words) {
		return processControlTarget{}, false
	}
	for _, w := range words[i:] {
		if len(w.text) > 7 || !isAllDigits(w.text) {
			return processControlTarget{}, false
		}
		pid, err := strconv.Atoi(w.text)
		if err != nil || pid <= 1 {
			return processControlTarget{}, false
		}
		pids = append(pids, pid)
	}
	return processControlTarget{pids: pids}, true
}

// maskVerifiedProcessControl replaces each top-level kill/pkill segment of
// cmd that owned approves with `true`. It returns cmd unchanged when there is
// nothing it can verify. Segments inside `$(…)`, after a keyword (`do kill`,
// `if pkill`) or behind a wrapper (`xargs kill`, `sudo pkill`) are not
// top-level command words and stay in the text.
func maskVerifiedProcessControl(cmd string, owned func(processControlTarget) bool) (string, bool) {
	if owned == nil || !processControlWordRe.MatchString(cmd) || processControlRefusalRe.MatchString(cmd) {
		return cmd, false
	}
	script, ok := parseUnattendedScript(cmd)
	if !ok {
		return cmd, false
	}
	type span struct{ start, end int }
	var spans []span
	for _, seg := range script.segs {
		target, ok := parseProcessControlSegment(seg)
		if !ok {
			continue
		}
		first, last := seg.words[0], seg.words[len(seg.words)-1]
		if first.start < 0 || last.end > len(cmd) || first.start >= last.end {
			continue
		}
		if !processControlRawRe.MatchString(cmd[first.start:last.end]) {
			continue
		}
		if !owned(target) {
			continue
		}
		spans = append(spans, span{first.start, last.end})
	}
	if len(spans) == 0 {
		return cmd, false
	}
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		if sp.start < last {
			return cmd, false
		}
		b.WriteString(cmd[last:sp.start])
		b.WriteString("true")
		last = sp.end
	}
	b.WriteString(cmd[last:])
	return b.String(), true
}
