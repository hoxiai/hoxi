package llm

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	thinkBlockRegex = regexp.MustCompile(`(?is)<think>.*?</think>`)
	codeFenceRegex  = regexp.MustCompile("(?s)```.*?```")
)

// StripThinking removes <think>…</think> reasoning blocks from model output.
func StripThinking(s string) string {
	return thinkBlockRegex.ReplaceAllString(s, "")
}

// StripCodeFences replaces fenced code blocks with repl.
func StripCodeFences(s, repl string) string {
	return codeFenceRegex.ReplaceAllString(s, repl)
}

// LooksLikeRawJSON reports a provider payload leaking through as "text".
func LooksLikeRawJSON(s string) bool {
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") || strings.Contains(s, `"choices"`)
}

// HasMeaningfulRune reports whether s contains a letter or digit.
func HasMeaningfulRune(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// TruncateRunes trims s and cuts it to at most maxRunes runes.
func TruncateRunes(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxRunes]))
}
