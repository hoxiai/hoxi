// Package textindex provides the shared tokenizer and BM25 scorer used by the
// codebase index and cross-session recall.
package textindex

import (
	"math"
	"strings"
	"unicode"
)

const (
	K1 = 1.2
	B  = 0.75
)

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "this": true,
	"that": true, "are": true, "was": true, "were": true, "has": true, "have": true,
	"not": true, "but": true, "you": true, "your": true, "into": true, "than": true,
	"func": true, "return": true, "const": true, "import": true, "export": true,
}

// Tokenize splits identifiers on casing, punctuation and whitespace, then
// lowercases and drops stopwords and short numbers. Numbers of three or more
// digits (status codes, ports, error numbers) stay searchable.
func Tokenize(text string) []string {
	var b strings.Builder
	b.Grow(len(text))
	var previous rune
	for _, r := range text {
		switch {
		case unicode.IsUpper(r) && (unicode.IsLower(previous) || unicode.IsDigit(previous)):
			b.WriteRune(' ')
			b.WriteRune(unicode.ToLower(r))
		case r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || unicode.IsSpace(r):
			b.WriteRune(' ')
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '$':
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(' ')
		}
		previous = r
	}
	fields := strings.Fields(b.String())
	out := fields[:0]
	for _, token := range fields {
		if len(token) < 2 || stopwords[token] || (len(token) < 3 && isNumber(token)) {
			continue
		}
		out = append(out, token)
	}
	return out
}

func Counts(text string) map[string]int {
	tokens := make(map[string]int)
	for _, token := range Tokenize(text) {
		tokens[token]++
	}
	return tokens
}

func isNumber(value string) bool {
	for _, r := range value {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// Score returns the BM25 score of one document against the query tokens.
func Score(query []string, tokens map[string]int, documentFrequency map[string]int, documents, length int, averageLength float64) float64 {
	if len(query) == 0 || len(tokens) == 0 {
		return 0
	}
	total := float64(documents)
	score := 0.0
	for _, token := range query {
		tf := tokens[token]
		if tf == 0 {
			continue
		}
		df := float64(documentFrequency[token])
		idf := math.Log(1 + (total-df+0.5)/(df+0.5))
		norm := 1 - B + B*float64(length)/math.Max(averageLength, 1)
		score += idf * (float64(tf) * (K1 + 1)) / (float64(tf) + K1*norm)
	}
	return score
}

// Snippet keeps the first non-empty lines up to the given bounds.
func Snippet(text string, maxLines, maxChars int) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder
	kept := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if kept > 0 {
			b.WriteString("\n")
		}
		b.WriteString(line)
		kept++
		if kept >= maxLines || b.Len() >= maxChars {
			break
		}
	}
	value := b.String()
	if len(value) > maxChars {
		value = strings.ToValidUTF8(value[:maxChars], "") + "…"
	}
	return value
}

// Clip bounds a string to max runes, appending an ellipsis when truncated.
func Clip(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}
