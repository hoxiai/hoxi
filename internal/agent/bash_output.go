package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	ansiEscapeSeqRe = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\))`)
	verificationRe  = regexp.MustCompile(`(?i)(?:\./verify\.sh\b|\b(?:go\s+(?:test|vet|build|mod\s+(?:tidy|download)|run\s+\./tools/contractcheck)|npm\s+(?:--prefix\s+\S+\s+)?(?:test|install|ci|i|run\s+(?:test|lint|build|check\S*))|pnpm\s+(?:test|install|i|lint|build|check\S*)|yarn\s+(?:test|install|lint|build)|bun\s+(?:test|run\s+(?:test|lint|build|check\S*))|vitest\b|jest\b|vite\s+build\b|esbuild\b|make\s+(?:test|build|check)\b|docker\s+build\b|pip3?\s+install|flutter\s+(?:test|analyze|pub\s+get)|pytest|cargo\s+(?:test|check|clippy|fetch)|tsc\b|node\s+--test)\b)`)
	failureMarkerRe = regexp.MustCompile(`(?m)(?:^|\s)(?:FAIL\b|--- FAIL:|FAIL\t|AssertionError|panic:|Error:|error:|error\[[A-Za-z0-9_-]+\]:|error TS\d+|ERR!|✖|✗|fatal:|undefined:\s+\S|syntax error:|SyntaxError:|TypeError:|ReferenceError:|ImportError:|ModuleNotFoundError:|IndentationError:|NameError:|ValueError:|RuntimeError:|Traceback \(most recent call last\):|\bcannot\s+(?:use|find|load|refer|assign|convert|call|take|index|slice|range|open|resolve|import)\b|\b(?:not enough|too many) arguments\b|\b(?:redeclared in this block|imported and not used|declared (?:and|but) not used|no new variables on left side|no required module provides package|package \S+ is not in std)\b|\[(?:build|setup) failed\]|\b(?:BUILD FAILED|build failed|compilation failed|compile error|Command failed|ELIFECYCLE)\b|\b(?:exit status|exit code|exited with code)\s+[1-9]\d*\b|[A-Za-z0-9_./\\-]+\.(?:go|ts|tsx|js|jsx|mjs|cjs|rs|py|c|cc|cpp|h|hpp|java|kt|swift|rb|cs|vue|svelte|dart|php):\d+(?::\d+)?:\s*\S)`)
)

// stripTerminalControlNoise removes ANSI color/cursor escape sequences and
// collapses bare carriage-return (`\r`) progress-bar redraws into the final
// visible line segment while preserving normal newlines.
func stripTerminalControlNoise(text string) string {
	if text == "" {
		return ""
	}
	out := ansiEscapeSeqRe.ReplaceAllString(text, "")
	out = strings.ReplaceAll(out, "\r\n", "\n")
	if !strings.ContainsRune(out, '\r') {
		return out
	}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if !strings.ContainsRune(line, '\r') {
			continue
		}
		segs := strings.Split(line, "\r")
		lastNonEmpty := ""
		for j := len(segs) - 1; j >= 0; j-- {
			if segs[j] != "" {
				lastNonEmpty = segs[j]
				break
			}
		}
		lines[i] = lastNonEmpty
	}
	return strings.Join(lines, "\n")
}

// compactBashToolOutput cleans terminal control noise, condenses verbose
// passing check logs, and preserves mid-log compiler/test failure windows when
// oversized command output exceeds maxBytes.
func compactBashToolOutput(rawInput json.RawMessage, rawOutput string, isError bool, maxBytes int) string {
	cleaned := ensureValidUTF8(stripTerminalControlNoise(rawOutput))
	if maxBytes <= 0 {
		return cleaned
	}

	var args struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(rawInput, &args)
	cmd := strings.TrimSpace(args.Command)

	hasFailure := isError || failureMarkerRe.MatchString(cleaned)

	// Condense noisy passing build/test/install logs (> 4 KiB) to head + tail summary.
	if !hasFailure && cmd != "" && verificationRe.MatchString(cmd) && len(cleaned) > 4096 {
		lines := strings.Split(cleaned, "\n")
		if len(lines) > 40 {
			const headN, tailN = 15, 20
			head := strings.Join(lines[:headN], "\n")
			tail := strings.Join(lines[len(lines)-tailN:], "\n")
			omitted := len(lines) - headN - tailN
			condensed := fmt.Sprintf("%s\n... [%d lines of passing check output omitted] ...\n%s", head, omitted, tail)
			if len(condensed) <= maxBytes {
				return condensed
			}
			cleaned = condensed
		}
	}

	if len(cleaned) <= maxBytes {
		return cleaned
	}

	lines := strings.Split(cleaned, "\n")
	const headLines = 30
	const tailLines = 40
	if len(lines) > headLines+tailLines+20 {
		midEnd := len(lines) - tailLines
		keepMidSet := make(map[int]bool)
		for i := headLines; i < midEnd; i++ {
			if failureMarkerRe.MatchString(lines[i]) {
				from := max(headLines, i-2)
				to := min(midEnd-1, i+8)
				for k := from; k <= to; k++ {
					keepMidSet[k] = true
				}
				if len(keepMidSet) >= 70 {
					break
				}
			}
		}
		if len(keepMidSet) > 0 {
			midIdx := make([]int, 0, len(keepMidSet))
			for idx := range keepMidSet {
				midIdx = append(midIdx, idx)
			}
			sort.Ints(midIdx)
			midSelected := make([]string, len(midIdx))
			for i, idx := range midIdx {
				midSelected[i] = lines[idx]
			}

			headBudget := maxBytes * 3 / 8
			midBudget := maxBytes / 4
			tailBudget := maxBytes - headBudget - midBudget - 160
			if headBudget > 0 && midBudget > 0 && tailBudget > 0 {
				headStr := utf8Prefix(strings.Join(lines[:headLines], "\n"), headBudget)
				midStr := utf8Prefix(strings.Join(midSelected, "\n"), midBudget)
				tailStr := utf8Suffix(strings.Join(lines[midEnd:], "\n"), tailBudget)
				omittedLines := len(lines) - headLines - tailLines - len(midIdx)
				combined := fmt.Sprintf(
					"%s\n\n... [failure excerpt from middle of log] ...\n%s\n\n... [%d log lines omitted for context budget] ...\n\n%s",
					headStr,
					midStr,
					max(1, omittedLines),
					tailStr,
				)
				if len(combined) <= maxBytes {
					return combined
				}
			}
		}
	}

	return truncateToolOutput(cleaned, maxBytes)
}
