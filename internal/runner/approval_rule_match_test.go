package runner

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// legacyGlobToRegexp is the matcher before the literal fast path: it compiled
// two regexps per pattern per request and quoted the pattern byte by byte.
func legacyGlobToRegexp(pattern string, loose bool) *regexp.Regexp {
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
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}

func legacyMatchesRulePattern(pattern, target, category string) bool {
	p, t := strings.TrimSpace(pattern), strings.TrimSpace(target)
	if p == "" || t == "" {
		return false
	}
	if p == "*" || p == "**" || strings.EqualFold(p, t) {
		return true
	}
	if re := legacyGlobToRegexp(p, false); re != nil && re.MatchString(t) {
		return true
	}
	loose := legacyGlobToRegexp(p, true)
	if loose != nil && loose.MatchString(t) {
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
	return loose != nil && (loose.MatchString(host) || loose.MatchString(parsed.Scheme+"://"+parsed.Host))
}

func TestMatchesRulePatternMatchesLegacyForASCII(t *testing.T) {
	patterns := []string{
		"git status", "GIT STATUS", " git status ", "npm install *", "npm install **", "docker ?s",
		"ls src/*", "ls src/**", "go test ./...", `echo "a.b" | grep (x)`, "rm -rf [abc]", "a+b",
		"https://docs.hoxi.ai/*", "docs.hoxi.ai", "*.hoxi.ai", "https://*.example.com", "*", "**", "",
		"python3 -c 'print(1)'", "make\tbuild", "x\ny",
	}
	targets := []string{
		"git status", "Git Status", "git status --short", "npm install left-pad", "npm install a/b",
		"docker ps", "docker pss", "ls src/a", "ls src/a/b", "go test ./...", "go test ./x..",
		`echo "a.b" | grep (x)`, `echo "aXb" | grep (x)`, "rm -rf [abc]", "rm -rf a", "a+b", "aab",
		"https://docs.hoxi.ai/guide", "docs.hoxi.ai/x", "https://api.hoxi.ai", "https://a.example.com",
		"http://docs.hoxi.ai", "python3 -c 'print(1)'", "make\tbuild", "x\ny", "",
	}
	for _, category := range []string{permissionCategoryBash, permissionCategoryEdit, permissionCategoryWebfetch} {
		for _, p := range patterns {
			for _, target := range targets {
				want := legacyMatchesRulePattern(p, target, category)
				if got := matchesRulePattern(p, target, category); got != want {
					t.Errorf("matchesRulePattern(%q, %q, %s) = %v, legacy %v", p, target, category, got, want)
				}
			}
		}
	}
}

func TestMatchesRulePatternKeepsMultiByteLiterals(t *testing.T) {
	cases := []struct {
		pattern, target string
		want            bool
	}{
		{"echo 你好", "echo 你好", true},
		{"echo 你好 *", "echo 你好 世界", true},
		{"echo 你*", "echo 你们", true},
		{"echo 你好 *", "echo 再见 世界", false},
		// Byte-wise quoting turned each UTF-8 byte into a Latin-1 rune, so the
		// old regexp for "echo 你*" matched this unrelated text.
		{"echo 你*", "echo ä½ x", false},
	}
	for _, tc := range cases {
		if got := matchesRulePattern(tc.pattern, tc.target, permissionCategoryBash); got != tc.want {
			t.Errorf("matchesRulePattern(%q, %q) = %v, want %v", tc.pattern, tc.target, got, tc.want)
		}
	}
}

func TestRuleDecisionLiteralRulesSkipRegexpCompile(t *testing.T) {
	rules := map[string]interface{}{"*": "ask"}
	for i := 0; i < 200; i++ {
		rules[fmt.Sprintf("literal-rule-cmd-%d --flag value", i)] = "allow"
	}
	ruleRegexpCache.Lock()
	ruleRegexpCache.entries = nil
	ruleRegexpCache.Unlock()

	if decision, ok := ruleDecision(rules, []string{"literal-rule-cmd-7 --flag value"}, permissionCategoryBash); !ok || decision != "allow" {
		t.Fatalf("saved command: got %q %v, want allow", decision, ok)
	}
	if decision, ok := ruleDecision(rules, []string{"LITERAL-RULE-CMD-7 --FLAG VALUE"}, permissionCategoryBash); !ok || decision != "allow" {
		t.Fatalf("case-folded saved command: got %q %v, want allow", decision, ok)
	}
	if _, ok := ruleDecision(rules, []string{"literal-rule-cmd-7 --flag other"}, permissionCategoryBash); ok {
		t.Fatal("unsaved command must fall back to the approval mode")
	}
	ruleRegexpCache.Lock()
	compiled := len(ruleRegexpCache.entries)
	ruleRegexpCache.Unlock()
	if compiled != 0 {
		t.Fatalf("literal rules compiled %d regexps, want 0", compiled)
	}

	rules["literal-rule-cmd-* --flag *"] = "allow"
	cached := func() int {
		ruleRegexpCache.Lock()
		defer ruleRegexpCache.Unlock()
		return len(ruleRegexpCache.entries)
	}
	firstCount := 0
	for i := 0; i < 3; i++ {
		if decision, ok := ruleDecision(rules, []string{"literal-rule-cmd-x --flag other"}, permissionCategoryBash); !ok || decision != "allow" {
			t.Fatalf("glob rule: got %q %v, want allow", decision, ok)
		}
		if i == 0 {
			firstCount = cached()
		}
	}
	if firstCount == 0 || cached() != firstCount {
		t.Fatalf("glob rule regexps: %d after first check, %d after repeats; want compiled once and reused", firstCount, cached())
	}
}

func BenchmarkRuleDecisionManyLiteralRules(b *testing.B) {
	rules := map[string]interface{}{"*": "ask"}
	for i := 0; i < 5000; i++ {
		rules[fmt.Sprintf("cd /Users/dev/project-%d && npm run build -- --mode %d", i, i)] = "allow"
	}
	targets := []string{"git status --short"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ruleDecision(rules, targets, permissionCategoryBash)
	}
}
