package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

var omissionPlaceholderPhrases = map[string]bool{
	"rest of":                    true,
	"rest of code":               true,
	"rest of file":               true,
	"rest of method":             true,
	"rest of methods":            true,
	"rest of function":           true,
	"rest of functions":          true,
	"rest of class":              true,
	"rest of implementation":     true,
	"rest of the code":           true,
	"rest of the file":           true,
	"rest of the methods":        true,
	"rest of the implementation": true,
	"unchanged code":             true,
	"unchanged method":           true,
	"unchanged methods":          true,
	"unchanged implementation":   true,
	"unchanged lines":            true,
	"code unchanged":             true,
	"same as before":             true,
	"existing code":              true,
	"existing method":            true,
	"existing methods":           true,
	"existing implementation":    true,
	"remaining code":             true,
	"remaining method":           true,
	"remaining methods":          true,
	"remaining implementation":   true,
	"omitted":                    true,
	"code omitted":               true,
	"implementation omitted":     true,
	"other methods":              true,
	"other functions":            true,
}

func isProseDocPath(filePath string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filePath))) {
	case ".md", ".mdx", ".markdown", ".txt", ".rst", ".adoc":
		return true
	default:
		return false
	}
}

func normalizeOmissionPlaceholderLine(rawLine string) (string, bool) {
	text := strings.TrimSpace(rawLine)
	if text == "" {
		return "", false
	}

	wrapped := false
	switch {
	case strings.HasPrefix(text, "/*") && strings.HasSuffix(text, "*/") && len(text) >= 4:
		text = strings.TrimSpace(text[2 : len(text)-2])
		wrapped = true
	case strings.HasPrefix(text, "<!--") && strings.HasSuffix(text, "-->") && len(text) >= 7:
		text = strings.TrimSpace(text[4 : len(text)-3])
		wrapped = true
	case strings.HasPrefix(text, "//"):
		text = strings.TrimSpace(text[2:])
		wrapped = true
	case strings.HasPrefix(text, "#"):
		text = strings.TrimSpace(text[1:])
		wrapped = true
	case strings.HasPrefix(text, "--"):
		text = strings.TrimSpace(text[2:])
		wrapped = true
	case strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") && len(text) >= 2:
		text = strings.TrimSpace(text[1 : len(text)-1])
		wrapped = true
	case strings.HasPrefix(text, "...") || strings.HasPrefix(text, "…"):
		wrapped = true
	}
	if !wrapped || text == "" {
		return "", false
	}

	if (strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")")) ||
		(strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]")) {
		text = strings.TrimSpace(text[1 : len(text)-1])
	}

	hasEllipsis := strings.Contains(text, "...") || strings.Contains(text, "…")
	if !hasEllipsis {
		return "", false
	}

	// Only accept leading/trailing ellipsis around the omission phrase so real
	// comments mentioning "..." inside prose sentences never false-positive.
	trimmed := strings.TrimSpace(strings.Trim(text, ".… \t"))
	if trimmed == "" || strings.Contains(trimmed, "...") || strings.Contains(trimmed, "…") {
		return "", false
	}
	phrase := strings.ToLower(strings.Join(strings.Fields(trimmed), " "))
	if !omissionPlaceholderPhrases[phrase] {
		return "", false
	}
	return phrase + " ...", true
}

func countOmissionPlaceholders(text string) (map[string]int, []string) {
	if text == "" {
		return nil, nil
	}
	norm := strings.ReplaceAll(text, "\r\n", "\n")
	counts := make(map[string]int)
	var order []string
	for _, line := range strings.Split(norm, "\n") {
		tok, ok := normalizeOmissionPlaceholderLine(line)
		if !ok {
			continue
		}
		if counts[tok] == 0 {
			order = append(order, tok)
		}
		counts[tok]++
	}
	return counts, order
}

// checkEditOmissionPlaceholders rejects newly introduced shorthand omission
// comments (e.g. "// ... rest of code ...", "# unchanged methods ...") so an
// edit/write/patch cannot silently delete existing source lines. Pre-existing
// placeholders in oldText and prose documentation files are exempt.
func checkEditOmissionPlaceholders(filePath, oldText, newText string) error {
	if isProseDocPath(filePath) || newText == "" {
		return nil
	}
	newCounts, order := countOmissionPlaceholders(newText)
	if len(newCounts) == 0 {
		return nil
	}
	oldCounts, _ := countOmissionPlaceholders(oldText)
	var introduced []string
	for _, tok := range order {
		if newCounts[tok] > oldCounts[tok] {
			introduced = append(introduced, tok)
		}
	}
	if len(introduced) == 0 {
		return nil
	}
	return fmt.Errorf(
		"refusing to write omission placeholder(s) (%s) into %s; provide the complete code or make a targeted edit without eliding lines",
		strings.Join(introduced, ", "),
		filePath,
	)
}
