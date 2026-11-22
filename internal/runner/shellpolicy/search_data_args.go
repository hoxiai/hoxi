package shellpolicy

import (
	"path/filepath"
	"strings"
)

// A short option in these sets never consumes the following word. The two
// tools differ (e.g. grep -r is recursive, rg -r takes a replacement).
const (
	rgSearchSwitches   = ".0abcFhHiIlLnNopPqsSuUvVwxz"
	grepSearchSwitches = "abcEFGHiIhlLnoPqRrsvVwxZ"
)

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
)

var searchSwitches = setOf("--line-number", "--ignore-case", "--case-sensitive", "--smart-case",
	"--fixed-strings", "--word-regexp", "--line-regexp", "--invert-match", "--only-matching",
	"--with-filename", "--no-filename", "--hidden", "--no-ignore", "--no-heading",
	"--heading", "--count", "--files-with-matches", "--files-without-match", "--multiline", "--no-messages")

var diffSearchSwitches = setOf("--cached", "--staged", "--stat", "--name-only", "--name-status", "--patch", "--pickaxe-all", "--pickaxe-regex")

func gitDiffSearchArguments(seg []dataToken, start int) []dataReplacement {
	var out []dataReplacement
	for i := start; i < len(seg); i++ {
		word := seg[i].value
		if !seg[i].literal {
			break
		}
		if diffSearchSwitches[word] {
			continue
		}
		if !strings.HasPrefix(word, "-G") && !strings.HasPrefix(word, "-S") {
			break
		}
		if len(word) == 2 {
			i++
		}
		out = appendWholeWordMask(out, seg, i)
	}
	return out
}

// Identify a positional pattern only if all options have known arity.
// After -e or -f, positional operands always name files.
func searchDataArguments(seg []dataToken) []dataReplacement {
	grep := filepath.Base(seg[0].value) != "rg"
	valueFlags, shortSwitches := rgValueFlags, rgSearchSwitches
	if grep {
		valueFlags, shortSwitches = grepValueFlags, grepSearchSwitches
	}
	var out []dataReplacement
	explicit := false
	positional := -1
	for i := 1; i < len(seg); i++ {
		word := seg[i].value
		if word == "--" && seg[i].literal {
			if positional == -1 {
				positional = i + 1
			}
			break
		}
		if !strings.HasPrefix(word, "-") || word == "-" {
			if positional == -1 {
				positional = i
			}
			continue
		}
		if !seg[i].literal || positional >= 0 {
			return out
		}
		flag, _, attached := strings.Cut(word, "=")
		if !strings.HasPrefix(word, "--") {
			j := 1
			for j < len(word) && strings.IndexByte(shortSwitches, word[j]) >= 0 {
				j++
			}
			if j == len(word) {
				continue
			}
			flag, attached = "-"+word[j:j+1], j+1 < len(word)
		}
		if searchSwitches[flag] {
			continue
		}
		// Some grep implementations have optional values for context and color.
		if grep && !attached && (flag == "-C" || flag == "--context" || flag == "--color" || flag == "--colour") {
			return out
		}
		pattern := flag == "-e" || flag == "--regexp"
		file := flag == "-f" || flag == "--file"
		if !pattern && !file && !valueFlags[flag] {
			return out
		}
		if !attached {
			i++
			if i == len(seg) || !seg[i].literal {
				return out
			}
		}
		if pattern || file {
			explicit = true
		}
		if pattern {
			out = appendWholeWordMask(out, seg, i)
		}
	}
	if !explicit {
		out = appendWholeWordMask(out, seg, positional)
	}
	return out
}
