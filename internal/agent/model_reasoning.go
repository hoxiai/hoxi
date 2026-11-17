package agent

import (
	"fmt"
	"strings"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

// reasoningVariant selects the same model-specific tier used by OpenCode.
// Empty variants explicitly mean that this model has no configured reasoning
// options; never add unsupported parameters just because the UI has an effort.
func reasoningVariant(req ModelRequest) map[string]any {
	effort := agentcli.NormalizeEffortName(req.Effort)
	if effort == "" || len(req.Target.Variants) == 0 {
		return nil
	}
	var supported []string
	for key, raw := range req.Target.Variants {
		if variant, ok := raw.(map[string]any); ok {
			if disabled, _ := variant["disabled"].(bool); !disabled {
				supported = append(supported, key)
			}
		}
	}
	if len(supported) == 0 {
		return nil
	}
	effort = agentcli.ClampEffortToSupported(effort, supported, "")
	variant, _ := req.Target.Variants[effort].(map[string]any)
	return variant
}

func anthropicThinking(req ModelRequest, maxTokens int) (map[string]any, string, error) {
	variant := reasoningVariant(req)
	thinking, _ := variant["thinking"].(map[string]any)
	kind, _ := thinking["type"].(string)
	switch kind {
	case "adaptive":
		effort, _ := variant["effort"].(string)
		return map[string]any{"type": "adaptive"}, strings.TrimSpace(effort), nil
	case "enabled":
		budget := intFromAny(thinking["budgetTokens"])
		if budget <= 0 {
			budget = intFromAny(thinking["budget_tokens"])
		}
		if budget <= 0 {
			return nil, "", fmt.Errorf("enabled thinking requires a positive token budget")
		}
		budget = min(budget, maxTokens-1)
		if budget < 1024 {
			return nil, "", fmt.Errorf("output limit %d cannot fit the minimum thinking budget of 1024 tokens", maxTokens)
		}
		return map[string]any{"type": "enabled", "budget_tokens": budget}, "", nil
	case "disabled", "":
		return nil, "", nil
	default:
		return nil, "", fmt.Errorf("unsupported thinking mode %q", kind)
	}
}
