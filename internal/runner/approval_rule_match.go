package runner

import (
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// Rule patterns come from the OpenCode `permission` block. On a long-lived
// install that block holds thousands of exact commands saved from approval
// cards, and ruleDecision checks every bash request against all of them. A
// pattern without glob characters is therefore compared directly, which is
// what its anchored, case-insensitive regexp would match, and glob patterns
// compile once instead of twice per pattern per request.

// maxCachedRuleRegexps bounds the compiled glob cache; the whole cache is
// dropped when it fills up.
const maxCachedRuleRegexps = 512

type ruleRegexpKey struct {
	pattern string
	loose   bool
}

var ruleRegexpCache struct {
	sync.Mutex
	entries map[ruleRegexpKey]*regexp.Regexp
}

func globToRegexp(pattern string, loose bool) *regexp.Regexp {
	var b strings.Builder
	if loose {
		b.WriteString("(?is)^")
	} else {
		b.WriteString("(?i)^")
	}
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			if loose {
				for i+1 < len(pattern) && pattern[i+1] == '*' {
					i++
				}
				b.WriteString(".*")
			} else if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				b.WriteString(".*")
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString(".")
		default:
			// Quote the whole literal run so multi-byte characters stay intact.
			end := i + 1
			for end < len(pattern) && pattern[end] != '*' && pattern[end] != '?' {
				end++
			}
			b.WriteString(regexp.QuoteMeta(pattern[i:end]))
			i = end - 1
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}

func cachedGlobRegexp(pattern string, loose bool) *regexp.Regexp {
	key := ruleRegexpKey{pattern: pattern, loose: loose}
	ruleRegexpCache.Lock()
	re, ok := ruleRegexpCache.entries[key]
	ruleRegexpCache.Unlock()
	if ok {
		return re
	}
	re = globToRegexp(pattern, loose)
	ruleRegexpCache.Lock()
	if ruleRegexpCache.entries == nil || len(ruleRegexpCache.entries) >= maxCachedRuleRegexps {
		ruleRegexpCache.entries = make(map[ruleRegexpKey]*regexp.Regexp)
	}
	ruleRegexpCache.entries[key] = re
	ruleRegexpCache.Unlock()
	return re
}

// globMatches reports whether s matches pattern as globToRegexp compiles it.
func globMatches(pattern string, loose bool, s string) bool {
	if !strings.ContainsAny(pattern, "*?") {
		return strings.EqualFold(pattern, s)
	}
	re := cachedGlobRegexp(pattern, loose)
	return re != nil && re.MatchString(s)
}

func matchesRulePattern(pattern, target, category string) bool {
	p, t := strings.TrimSpace(pattern), strings.TrimSpace(target)
	if p == "" || t == "" {
		return false
	}
	if p == "*" || p == "**" || strings.EqualFold(p, t) {
		return true
	}
	if globMatches(p, false, t) || globMatches(p, true, t) {
		return true
	}
	if category != permissionCategoryWebfetch {
		return false
	}
	raw := t
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	lowerP := strings.ToLower(p)
	if host == lowerP {
		return true
	}
	if strings.HasPrefix(lowerP, "*.") && (host == lowerP[2:] || strings.HasSuffix(host, lowerP[1:])) {
		return true
	}
	return globMatches(p, true, host) || globMatches(p, true, parsed.Scheme+"://"+parsed.Host)
}
