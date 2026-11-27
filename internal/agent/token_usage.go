package agent

import (
	"math"

	"github.com/hoxiai/hoxi/internal/llm"
)

func pricedUsage(target llm.StreamTarget, u TokenUsage) TokenUsage {
	if u.CostUSD <= 0 {
		u.CostUSD = target.Cost.ComputeUSD(u.Input, u.Output, u.Reasoning, u.CacheRead, u.CacheWrite) * fastModeCostMultiplier(u.ServiceTier)
	}
	return u
}

// Provider event JSON is decoded into float64. Missing, null and malformed
// cache fields must not be interpreted as a measured zero.
func recordCacheCoverage(u *TokenUsage, value any) {
	total := u.Input + u.CacheRead + u.CacheWrite
	if validCacheTokenField(value, u.CacheRead, total) {
		u.ReportedCacheInput = total
	}
}

func validCacheTokenField(value any, expected, total int) bool {
	n, ok := value.(float64)
	return ok && n >= 0 && math.Trunc(n) == n && n == float64(expected) && n <= float64(total)
}

// recordCacheCoverageFromMap checks primaryKey first and falls back to explicit
// sibling cache fields only when primaryKey is absent (e.g. omitempty on a zero
// cache read). A present-but-malformed primaryKey is never overridden.
func recordCacheCoverageFromMap(u *TokenUsage, m map[string]any, primaryKey string, siblingKeys ...string) {
	if m == nil {
		return
	}
	if primaryVal, exists := m[primaryKey]; exists {
		recordCacheCoverage(u, primaryVal)
		return
	}
	total := u.Input + u.CacheRead + u.CacheWrite
	for _, k := range siblingKeys {
		val, exists := m[k]
		if !exists {
			continue
		}
		expected := u.CacheWrite
		if k == "prompt_cache_miss_tokens" {
			expected = u.Input
		}
		if validCacheTokenField(val, expected, total) {
			u.ReportedCacheInput = total
			return
		}
	}
}

// OpenAI and compatible gateways (including OpenRouter) include cache reads
// and cache writes in prompt_tokens/input_tokens, and reasoning in output.
// Hoxi's canonical buckets are disjoint, matching Anthropic and CLI translators.
func normalizeInclusiveUsage(u *TokenUsage) {
	u.Input = max(0, u.Input)
	u.Output = max(0, u.Output)
	u.CacheRead = min(u.Input, max(0, u.CacheRead))
	u.CacheWrite = min(u.Input-u.CacheRead, max(0, u.CacheWrite))
	u.Reasoning = min(u.Output, max(0, u.Reasoning))
	u.Input -= u.CacheRead + u.CacheWrite
	u.Output -= u.Reasoning
}
