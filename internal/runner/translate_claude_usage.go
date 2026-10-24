package runner

import (
	"math"
	"strings"
)

const qoderContextWindowTokens = 180000

type claudeUsage struct {
	InputTokens              int     `json:"input_tokens"`
	OutputTokens             int     `json:"output_tokens"`
	CacheReadInputTokens     int     `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int     `json:"cache_creation_input_tokens"`
	ContextUsageRatio        float64 `json:"context_usage_ratio"`
	OutputTokensDetails      *struct {
		ThinkingTokens int `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

func (u claudeUsage) effectiveInputTokens() int {
	if u.InputTokens > 0 || u.CacheReadInputTokens > 0 || u.CacheCreationInputTokens > 0 {
		return u.InputTokens
	}
	if u.ContextUsageRatio > 0 {
		return int(math.Round(u.ContextUsageRatio * qoderContextWindowTokens))
	}
	return 0
}

func (u claudeUsage) thinkingTokens() int {
	if u.OutputTokensDetails != nil {
		return u.OutputTokensDetails.ThinkingTokens
	}
	return 0
}

// normalizeForRuntime fixes CodeBuddy's stream-json usage mapping where OpenAI
// prompt_tokens is emitted as input_tokens and prompt_cache_miss_tokens is
// emitted as cache_creation_input_tokens (so input_tokens == cache_read + cache_creation).
func (u *claudeUsage) normalizeForRuntime(runtime string) *claudeUsage {
	if u == nil {
		return nil
	}
	cp := *u
	if runtime == string(RuntimeCodeBuddy) && cp.InputTokens > 0 &&
		cp.CacheCreationInputTokens > 0 &&
		cp.InputTokens == cp.CacheReadInputTokens+cp.CacheCreationInputTokens {
		cp.InputTokens = cp.CacheCreationInputTokens
		cp.CacheCreationInputTokens = 0
	}
	return &cp
}

type claudeModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	ThinkingTokens           int     `json:"thinkingTokens"`
	CostUSD                  float64 `json:"costUSD"`
}

func mergeClaudeUsage(prev claudeUsage, incoming *claudeUsage) claudeUsage {
	if incoming == nil {
		return prev
	}
	if in := incoming.effectiveInputTokens(); in > prev.InputTokens {
		prev.InputTokens = in
	}
	if incoming.OutputTokens > prev.OutputTokens {
		prev.OutputTokens = incoming.OutputTokens
	}
	if incoming.CacheReadInputTokens > prev.CacheReadInputTokens {
		prev.CacheReadInputTokens = incoming.CacheReadInputTokens
	}
	if incoming.CacheCreationInputTokens > prev.CacheCreationInputTokens {
		prev.CacheCreationInputTokens = incoming.CacheCreationInputTokens
	}
	if th := incoming.thinkingTokens(); th > prev.thinkingTokens() {
		prev.OutputTokensDetails = &struct {
			ThinkingTokens int `json:"thinking_tokens"`
		}{ThinkingTokens: th}
	}
	return prev
}

// normalizeClaudeOutputTokens subtracts thinking_tokens from Anthropic's
// output_tokens (which already includes thinking_tokens) so Hoxi's disjoint
// Output + Reasoning fields sum to total output tokens without double-counting.
func normalizeClaudeOutputTokens(rawOutput, thinking int) int {
	if thinking > 0 && rawOutput >= thinking {
		return rawOutput - thinking
	}
	return rawOutput
}

func sumClaudeUsageMap(m map[string]claudeUsage) *MessageTokens {
	var in, out, reasoning, cacheRead, cacheWrite int
	for _, u := range m {
		th := u.thinkingTokens()
		in += u.InputTokens
		out += normalizeClaudeOutputTokens(u.OutputTokens, th)
		reasoning += th
		cacheRead += u.CacheReadInputTokens
		cacheWrite += u.CacheCreationInputTokens
	}
	total := in + out + reasoning + cacheRead + cacheWrite
	if total == 0 {
		return nil
	}
	tokens := &MessageTokens{
		Total:     total,
		Input:     in,
		Output:    out,
		Reasoning: reasoning,
	}
	tokens.Cache.Read = cacheRead
	tokens.Cache.Write = cacheWrite
	return tokens
}

func (t *TurnTranslator) noteClaudeMsgUsage(msgID string, u *claudeUsage) bool {
	u = u.normalizeForRuntime(t.runtime)
	if u == nil {
		return false
	}
	key := strings.TrimSpace(msgID)
	if key == "" {
		key = "_default"
	}
	t.claudeMsgUsage[key] = mergeClaudeUsage(t.claudeMsgUsage[key], u)
	tokens := sumClaudeUsageMap(t.claudeMsgUsage)
	if tokens == nil || messageTokensEqual(t.msgInfo.Tokens, tokens) {
		return false
	}
	t.msgInfo.Tokens = tokens
	return true
}

func (t *TurnTranslator) applyClaudeResultUsage(env *claudeRawEvent) {
	if env == nil {
		return
	}
	if env.TotalCostUSD > 0 {
		t.msgInfo.Cost = env.TotalCostUSD
	}
	if len(env.ModelUsage) > 0 {
		var in, rawOut, reasoning, cacheRead, cacheWrite int
		var modelCost float64
		for _, mu := range env.ModelUsage {
			in += mu.InputTokens
			rawOut += mu.OutputTokens
			reasoning += mu.ThinkingTokens
			cacheRead += mu.CacheReadInputTokens
			cacheWrite += mu.CacheCreationInputTokens
			modelCost += mu.CostUSD
		}
		if env.Usage != nil && env.Usage.thinkingTokens() > reasoning {
			reasoning = env.Usage.thinkingTokens()
		}
		if streamed := sumClaudeUsageMap(t.claudeMsgUsage); streamed != nil && streamed.Reasoning > reasoning {
			reasoning = streamed.Reasoning
		}
		out := normalizeClaudeOutputTokens(rawOut, reasoning)
		if t.msgInfo.Cost == 0 && modelCost > 0 {
			t.msgInfo.Cost = modelCost
		}
		total := in + out + reasoning + cacheRead + cacheWrite
		if total > 0 {
			tokens := &MessageTokens{
				Total:     total,
				Input:     in,
				Output:    out,
				Reasoning: reasoning,
			}
			tokens.Cache.Read = cacheRead
			tokens.Cache.Write = cacheWrite
			t.msgInfo.Tokens = tokens
			return
		}
	}
	// CodeBuddy's result.usage aggregates session.history across resumed turns,
	// whereas t.claudeMsgUsage contains only the assistant messages of this turn.
	if t.runtime == string(RuntimeCodeBuddy) {
		if streamed := sumClaudeUsageMap(t.claudeMsgUsage); streamed != nil {
			t.msgInfo.Tokens = streamed
			return
		}
	}
	resUsage := env.Usage.normalizeForRuntime(t.runtime)
	if resUsage != nil {
		var parentStreamedTh int
		for k, cu := range t.claudeMsgUsage {
			if !strings.HasPrefix(k, "child:") {
				parentStreamedTh += cu.thinkingTokens()
			}
		}
		th := max(resUsage.thinkingTokens(), parentStreamedTh)
		in := resUsage.effectiveInputTokens()
		out := normalizeClaudeOutputTokens(resUsage.OutputTokens, th)
		reasoning := th
		cacheRead := resUsage.CacheReadInputTokens
		cacheWrite := resUsage.CacheCreationInputTokens
		// Fold in any child subagent messages when modelUsage was not emitted.
		for k, cu := range t.claudeMsgUsage {
			if strings.HasPrefix(k, "child:") {
				childTh := cu.thinkingTokens()
				in += cu.InputTokens
				out += normalizeClaudeOutputTokens(cu.OutputTokens, childTh)
				reasoning += childTh
				cacheRead += cu.CacheReadInputTokens
				cacheWrite += cu.CacheCreationInputTokens
			}
		}
		if streamed := sumClaudeUsageMap(t.claudeMsgUsage); streamed != nil {
			in = max(in, streamed.Input)
			out = max(out, streamed.Output)
			reasoning = max(reasoning, streamed.Reasoning)
			cacheRead = max(cacheRead, streamed.Cache.Read)
			cacheWrite = max(cacheWrite, streamed.Cache.Write)
		}
		total := in + out + reasoning + cacheRead + cacheWrite
		if total > 0 {
			tokens := &MessageTokens{
				Total:     total,
				Input:     in,
				Output:    out,
				Reasoning: reasoning,
			}
			tokens.Cache.Read = cacheRead
			tokens.Cache.Write = cacheWrite
			t.msgInfo.Tokens = tokens
		}
	}
}
