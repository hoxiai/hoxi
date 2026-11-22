package agent

import "github.com/hoxiai/hoxi/internal/llm"

func pricedUsage(target llm.StreamTarget, u TokenUsage) TokenUsage {
	if u.CostUSD <= 0 {
		u.CostUSD = target.Cost.ComputeUSD(u.Input, u.Output, u.Reasoning, u.CacheRead, u.CacheWrite) * fastModeCostMultiplier(u.ServiceTier)
	}
	return u
}

// OpenAI includes cache reads in input and reasoning in output. Hoxi's
// canonical buckets are disjoint, matching Anthropic and the CLI translators.
func normalizeInclusiveUsage(u *TokenUsage) {
	u.Input = max(0, u.Input)
	u.Output = max(0, u.Output)
	u.CacheRead = min(u.Input, max(0, u.CacheRead))
	u.Reasoning = min(u.Output, max(0, u.Reasoning))
	u.Input -= u.CacheRead
	u.Output -= u.Reasoning
}
