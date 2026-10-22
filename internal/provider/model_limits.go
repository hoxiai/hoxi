package provider

import (
	"encoding/json"
	"strconv"
	"strings"
)

// OpenCode only compacts a conversation when the selected model declares a
// non-zero context window: its overflow check returns early on
// `limit.context == 0`. Providers configured through .hoxi/models.json often
// omit `limit`, and the engine's catalog does not know their model ids, so a
// long session silently grows past the real window and never compacts.
//
// Hoxi therefore fills the window for model families whose limits are public
// and stable. Unknown families are left untouched: inventing a window there
// could trigger compaction on a model that still has plenty of room.

type modelTokenLimit struct {
	Context int
	Output  int
}

// variantSuffixes are deployment labels Hoxi providers append to an upstream
// model id (e.g. `deepseek-v4.1-flash-solo`). They never change the context
// window, so the family lookup strips them.
var variantSuffixes = []string{
	"-solo",
	"-fast",
	"-free",
	"-flex",
	"-marathon",
	"-priority",
	"-latest",
	"-reasoning",
	"-build",
	"-preview",
}

// canonicalModelID normalizes a provider-scoped model id into a comparable
// family key: lowercase, provider prefix and channel suffixes removed, dots and
// underscores folded into dashes, dated snapshots stripped.
func canonicalModelID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	for _, prefix := range []string{"cn:", "intl:", "global:", "models/"} {
		s = strings.TrimPrefix(s, prefix)
	}
	if idx := strings.LastIndex(s, "/"); idx >= 0 && idx < len(s)-1 {
		s = s[idx+1:]
	}
	s = stripParenthesized(s)
	s = strings.TrimSpace(s)
	s = strings.NewReplacer(".", "-", "_", "-", " ", "-", "\t", "-").Replace(s)
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.TrimPrefix(s, "~")
	s = trimDateSuffix(s)
	s = strings.TrimSuffix(s, "-thinking")
	for _, suffix := range []string{"@eu", "@us", "@cn", ":thinking", ":free", ":fast"} {
		s = strings.TrimSuffix(s, suffix)
	}
	for {
		trimmed := false
		for _, suffix := range variantSuffixes {
			if strings.HasSuffix(s, suffix) && len(s) > len(suffix) {
				s = strings.TrimSuffix(s, suffix)
				trimmed = true
			}
		}
		if !trimmed {
			break
		}
	}
	return s
}

func stripParenthesized(s string) string {
	for {
		open := strings.IndexAny(s, "(（")
		if open < 0 {
			return s
		}
		closeIdx := strings.IndexAny(s[open:], ")）")
		if closeIdx < 0 {
			return s
		}
		s = s[:open] + s[open+closeIdx+1:]
	}
}

func trimDateSuffix(s string) string {
	idx := strings.LastIndex(s, "-")
	if idx < 0 {
		return s
	}
	tail := s[idx+1:]
	// Catalogs use both full dates (20250514) and month-day revisions (1017,
	// 0731, 0813). Only tails that actually parse as one of those are snapshots;
	// a real version number such as "grok-4-7" must survive.
	if len(tail) == 8 {
		if _, err := strconv.Atoi(tail); err == nil {
			return s[:idx]
		}
		return s
	}
	if len(tail) == 4 {
		n, err := strconv.Atoi(tail)
		if err != nil {
			return s
		}
		month, day := n/100, n%100
		if month >= 1 && month <= 12 && day >= 1 && day <= 31 {
			return s[:idx]
		}
	}
	return s
}

// hasToken reports whether the dash-delimited key contains token. Provider
// prefixes (`officeace-gpt-6-astra`) and bare ids (`gpt-6-astra`) therefore
// match the same family without treating `xstep` as `step`.
func hasToken(key, token string) bool {
	for _, part := range strings.Split(key, "-") {
		if part == token {
			return true
		}
	}
	return false
}

func hasAnyToken(key string, tokens ...string) bool {
	for _, token := range tokens {
		if hasToken(key, token) {
			return true
		}
	}
	return false
}

// versionAfter returns the first numeric token that follows family in a
// dash-delimited key, so `claude-opus-5-5`, `officeace-claude-sonnet-5-5` and
// `grok-4-7` all yield their family version.
func versionAfter(key, family string) int {
	parts := strings.Split(key, "-")
	for i, part := range parts {
		if part != family {
			continue
		}
		for _, next := range parts[i+1:] {
			if n := leadingNumber(next); n > 0 {
				return n
			}
		}
		return 0
	}
	return 0
}

func leadingNumber(token string) int {
	n := 0
	found := false
	for _, r := range token {
		if r < '0' || r > '9' {
			if found {
				break
			}
			continue
		}
		found = true
		n = n*10 + int(r-'0')
	}
	if !found {
		return 0
	}
	return n
}

// knownModelTokenLimit maps a canonical family key to the published window.
func knownModelTokenLimit(key string) (modelTokenLimit, bool) {
	if key == "" {
		return modelTokenLimit{}, false
	}

	switch {
	case hasToken(key, "claude"):
		if hasAnyToken(key, "opus", "sonnet", "fable") {
			if versionAfter(key, "claude") >= 5 {
				return modelTokenLimit{Context: 1_000_000, Output: 128_000}, true
			}
		}
		return modelTokenLimit{Context: 200_000, Output: 64_000}, true

	case hasToken(key, "gemini"):
		return modelTokenLimit{Context: 1_048_576, Output: 65_536}, true

	case hasToken(key, "gpt") || hasToken(key, "codex") || isOSeriesModel(key):
		if hasAnyToken(key, "astra") || hasToken(key, "6") || hasAnyToken(key, "5-6") {
			return modelTokenLimit{Context: 1_050_000, Output: 128_000}, true
		}
		if hasToken(key, "sol") || hasAnyToken(key, "5-5") {
			return modelTokenLimit{Context: 1_000_000, Output: 128_000}, true
		}
		return modelTokenLimit{Context: 400_000, Output: 128_000}, true

	case hasToken(key, "deepseek"):
		switch {
		case hasToken(key, "v3"):
			return modelTokenLimit{Context: 163_840, Output: 65_536}, true
		case hasAnyToken(key, "r1", "chat"):
			return modelTokenLimit{Context: 128_000, Output: 64_000}, true
		default:
			return modelTokenLimit{Context: 1_048_576, Output: 384_000}, true
		}

	case hasToken(key, "qwen") || hasToken(key, "qwq"):
		return modelTokenLimit{Context: 1_000_000, Output: 131_072}, true

	case hasAnyToken(key, "glm", "codegeex"):
		if hasToken(key, "4") || hasAnyToken(key, "4-7", "4-6", "4-5") {
			return modelTokenLimit{Context: 200_000, Output: 131_072}, true
		}
		return modelTokenLimit{Context: 1_000_000, Output: 131_072}, true

	case hasAnyToken(key, "kimi", "moonshot"):
		if hasToken(key, "k2") {
			return modelTokenLimit{Context: 131_072, Output: 98_304}, true
		}
		return modelTokenLimit{Context: 1_048_576, Output: 131_072}, true

	case hasToken(key, "mimo"):
		return modelTokenLimit{Context: 1_048_576, Output: 131_072}, true

	case hasToken(key, "minimax"):
		return modelTokenLimit{Context: 1_000_000, Output: 131_072}, true

	case hasToken(key, "step"):
		return modelTokenLimit{Context: 1_000_000, Output: 65_536}, true

	case hasToken(key, "grok"):
		if hasAnyToken(key, "mini", "fast") {
			return modelTokenLimit{Context: 131_072, Output: 32_000}, true
		}
		if versionAfter(key, "grok") >= 4 {
			return modelTokenLimit{Context: 500_000, Output: 131_072}, true
		}
		return modelTokenLimit{Context: 256_000, Output: 64_000}, true

	case hasAnyToken(key, "doubao", "seed"):
		return modelTokenLimit{Context: 262_144, Output: 131_072}, true

	case hasToken(key, "hunyuan") || hasToken(key, "hy3") || hasToken(key, "hy4"):
		if hasToken(key, "hy3") {
			return modelTokenLimit{Context: 192_000, Output: 64_000}, true
		}
		return modelTokenLimit{Context: 1_000_000, Output: 64_000}, true

	case hasAnyToken(key, "mistral", "codestral", "pixtral"):
		return modelTokenLimit{Context: 256_000, Output: 32_000}, true
	}
	return modelTokenLimit{}, false
}

func isOSeriesModel(key string) bool {
	for _, part := range strings.Split(key, "-") {
		for _, prefix := range []string{"o1", "o3", "o4"} {
			if part == prefix {
				return true
			}
			if strings.HasPrefix(part, prefix) && len(part) > len(prefix) {
				tail := part[len(prefix):]
				if _, err := strconv.Atoi(tail); err == nil {
					return true
				}
			}
		}
	}
	return false
}

func positiveNumber(value interface{}) int {
	switch v := value.(type) {
	case int:
		if v > 0 {
			return v
		}
	case int64:
		if v > 0 {
			return int(v)
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			return int(n)
		}
	}
	return 0
}

// ensureModelTokenLimit fills a missing context/output window on one model
// entry. Explicit positive values are never overwritten. The model id is tried
// first, then the display name, because providers such as `subapi` expose
// vendor-prefixed ids (`OfficeAce-deepseek-v4.1-flash`) and some ids are opaque.
func ensureModelTokenLimit(modelObj map[string]interface{}, modelID string) {
	limit, ok := knownModelTokenLimit(canonicalModelID(modelID))
	if !ok {
		name, _ := modelObj["name"].(string)
		limit, ok = knownModelTokenLimit(canonicalModelID(name))
	}
	if !ok {
		return
	}
	cur, _ := modelObj["limit"].(map[string]interface{})
	if cur == nil {
		cur = map[string]interface{}{}
	}
	if positiveNumber(cur["context"]) == 0 {
		cur["context"] = limit.Context
	}
	if positiveNumber(cur["output"]) == 0 {
		cur["output"] = limit.Output
	}
	// OpenCode reserves the output window before deciding that the context is
	// full (`threshold = context - output`). A catalog entry whose output equals
	// its context would therefore compact on every single turn, so keep at least
	// a quarter of the window free for the conversation itself.
	if context := positiveNumber(cur["context"]); context > 0 {
		if output := positiveNumber(cur["output"]); output == 0 || output > context*3/4 {
			cur["output"] = max(context/4, 8192)
		}
	}
	modelObj["limit"] = cur
}
