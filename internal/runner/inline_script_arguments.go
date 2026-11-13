package runner

import "strings"

// attachedInlineCode splits interpreter options without serializing argv back
// into shell text. The same helper feeds both the capability judge and masking.
func attachedInlineCode(family, word string) (prefix, code string, ok bool) {
	prefixes := []string{}
	switch family {
	case "node", "bun":
		if word == "-pe" || word == "-ep" {
			return "", "", false
		}
		prefixes = []string{"--eval=", "--print=", "-e", "-p"}
	case "python", "python3":
		prefixes = []string{"-c"}
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(word, prefix) && (len(word) > len(prefix) || strings.HasSuffix(prefix, "=")) {
			return prefix, word[len(prefix):], true
		}
	}
	return "", "", false
}

func inlineScriptTrailingArgsSafe(seg, family string, spans []shellWordSpan, start int) bool {
	positional := family != "node"
	for k := start; k < len(spans); k++ {
		w := spans[k].word
		if devNullWordRe.MatchString(w) || dupStreamWordRe.MatchString(w) {
			continue
		}
		if redirectWordRe.MatchString(w) && k+1 < len(spans) && spans[k+1].word == "/dev/null" {
			k++
			continue
		}
		if strings.ContainsAny(seg[spans[k].start:spans[k].end], "$`<>") {
			return false
		}
		if !positional && w == "--" {
			positional = true
			continue
		}
		if !positional && strings.HasPrefix(w, "-") {
			if !inlineSafeNodeFlags[w] {
				return false
			}
			continue
		}
		positional = true
	}
	return true
}
