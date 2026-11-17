package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/llm"
)

func subtaskPolicyDecision(
	primaryMode, targetSubagent string,
	byKey map[string]llm.AgentSpec,
	cfgDoc map[string]any,
) string {
	targetLower := strings.ToLower(strings.TrimSpace(targetSubagent))
	if spec, ok := byKey[targetLower]; ok && spec.Disable {
		return "deny"
	}
	if pSpec, ok := byKey[strings.ToLower(strings.TrimSpace(primaryMode))]; ok && pSpec.TaskPolicy != nil {
		if dec := evalTaskPolicyValue(pSpec.TaskPolicy, targetSubagent); dec != "" {
			return dec
		}
	}
	if cfgDoc != nil {
		if perm, ok := cfgDoc["permission"].(map[string]any); ok {
			if dec := evalTaskPolicyValue(perm["task"], targetSubagent); dec != "" {
				return dec
			}
		}
	}
	return "allow"
}

func evalTaskPolicyValue(raw any, targetSubagent string) string {
	switch pol := raw.(type) {
	case string:
		return normalizePolicyAction(pol)
	case map[string]any:
		if v, ok := pol[targetSubagent].(string); ok {
			if act := normalizePolicyAction(v); act != "" {
				return act
			}
		}
		for k, v := range pol {
			if strings.EqualFold(k, targetSubagent) {
				if s, ok := v.(string); ok {
					if act := normalizePolicyAction(s); act != "" {
						return act
					}
				}
			}
		}
		for pattern, v := range pol {
			if pattern == "*" {
				continue
			}
			s, ok := v.(string)
			if !ok {
				continue
			}
			if matched, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(targetSubagent)); matched {
				if act := normalizePolicyAction(s); act != "" {
					return act
				}
			}
		}
		if star, ok := pol["*"].(string); ok {
			return normalizePolicyAction(star)
		}
	}
	return ""
}

func normalizePolicyAction(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "deny":
		return "deny"
	case "ask":
		return "ask"
	case "allow":
		return "allow"
	default:
		return ""
	}
}

func (e *Engine) resolveSubtaskModel(
	parent TurnRequest,
	subType string,
	spec llm.AgentSpec,
	hasSpec bool,
	childReadOnly bool,
	callModel string,
	cfgDoc map[string]any,
) (providerID, modelID, routing string) {
	tryRef := func(raw string, allowAlias bool) (llm.StreamTarget, string, bool) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || e.stream == nil {
			return llm.StreamTarget{}, "", false
		}
		if allowAlias && (strings.EqualFold(trimmed, "economy") || strings.EqualFold(trimmed, "fast") || strings.EqualFold(trimmed, "small")) {
			if eco, ok := e.stream.(llm.SubagentEconomyResolver); ok {
				if ecoMid, okEco := eco.ResolveEconomyModel(parent.ProviderID, parent.ModelID); okEco && ecoMid != "" {
					if t := e.stream.ResolveStreamTarget(parent.ProviderID, ecoMid); t.HasEndpoint && t.ProviderID != "" && t.ModelID != "" {
						return t, "call_economy", true
					}
				}
			}
			if strings.EqualFold(trimmed, "small") && cfgDoc != nil {
				if sm, ok := cfgDoc["small_model"].(string); ok && strings.TrimSpace(sm) != "" {
					sp, smid := llm.SplitModelRef(sm)
					if sp == "" {
						sp = parent.ProviderID
					}
					if smid != "" {
						if t := e.stream.ResolveStreamTarget(sp, smid); t.HasEndpoint && t.ProviderID != "" && t.ModelID != "" {
							return t, "call_small_role", true
						}
					}
				}
			}
			return llm.StreamTarget{}, "", false
		}
		prov, mod := llm.SplitModelRef(trimmed)
		if mod == "" {
			return llm.StreamTarget{}, "", false
		}
		if prov == "" {
			prov = parent.ProviderID
		}
		t := e.stream.ResolveStreamTarget(prov, mod)
		if !t.HasEndpoint || t.ProviderID == "" || t.ModelID == "" {
			return llm.StreamTarget{}, "", false
		}
		return t, "", true
	}

	if callModel != "" {
		if t, aliasSrc, ok := tryRef(callModel, true); ok {
			if aliasSrc != "" {
				return t.ProviderID, t.ModelID, aliasSrc
			}
			return t.ProviderID, t.ModelID, "call_override"
		}
	}
	if hasSpec && strings.TrimSpace(spec.Model) != "" {
		if t, _, ok := tryRef(spec.Model, false); ok {
			return t.ProviderID, t.ModelID, "agent_config"
		}
	}
	if strings.EqualFold(subType, "general") && cfgDoc != nil {
		if legacy, ok := cfgDoc["subagent_model"].(string); ok && strings.TrimSpace(legacy) != "" {
			if t, _, ok := tryRef(legacy, false); ok {
				return t.ProviderID, t.ModelID, "legacy_config"
			}
		}
	}
	if childReadOnly && e.stream != nil {
		if eco, ok := e.stream.(llm.SubagentEconomyResolver); ok {
			if ecoMid, okEco := eco.ResolveEconomyModel(parent.ProviderID, parent.ModelID); okEco && ecoMid != "" {
				if t := e.stream.ResolveStreamTarget(parent.ProviderID, ecoMid); t.HasEndpoint && t.ProviderID != "" && t.ModelID != "" {
					if t.ProviderID == parent.ProviderID && t.ModelID == parent.ModelID {
						return parent.ProviderID, parent.ModelID, "inherit_parent"
					}
					return t.ProviderID, t.ModelID, "economy_auto"
				}
			}
		}
	}
	return parent.ProviderID, parent.ModelID, "inherit_parent"
}

func (e *Engine) runSubtask(
	ctx context.Context,
	parent TurnRequest,
	parentEffectiveMode string,
	agentMap map[string]llm.AgentSpec,
	cfgDoc map[string]any,
	req SubtaskRequest,
	parentEmit func(EngineEvent),
) (SubtaskResult, error) {
	subType := strings.TrimSpace(req.SubagentType)
	if subType == "" {
		subType = "explore"
	}
	subLower := strings.ToLower(subType)
	spec, hasSpec := agentMap[subLower]

	primaryMode := strings.TrimSpace(parent.Mode)
	if primaryMode == "" {
		primaryMode = "build"
	}
	decision := subtaskPolicyDecision(primaryMode, subType, agentMap, cfgDoc)
	if decision == "deny" {
		err := fmt.Errorf("subagent %q is disabled or denied by task permission policy", subType)
		return SubtaskResult{Summary: err.Error(), SubagentType: subType, ReadOnly: true}, err
	}

	childReadOnly := true
	if hasSpec {
		childReadOnly = spec.ReadOnly
	} else if strings.EqualFold(subType, "general") {
		childReadOnly = false
	}
	if strings.EqualFold(parentEffectiveMode, "plan") {
		childReadOnly = true
	}

	if decision == "ask" {
		if parent.PermissionHook == nil {
			err := fmt.Errorf("subagent %q requires explicit approval by task permission policy", subType)
			return SubtaskResult{Summary: err.Error(), SubagentType: subType, ReadOnly: childReadOnly}, err
		}
		rawAsk, _ := json.Marshal(map[string]any{
			"subagent_type": subType,
			"description":   req.Description,
			"prompt":        req.Prompt,
			"model":         req.Model,
		})
		if err := parent.PermissionHook(ctx, PermissionRequest{
			SessionID: parent.SessionID,
			CallID:    req.ParentCallID,
			ToolName:  "task",
			Input:     rawAsk,
			IsRead:    false,
			ForceAsk:  true,
		}); err != nil {
			return SubtaskResult{Summary: err.Error(), SubagentType: subType, ReadOnly: childReadOnly}, err
		}
	}

	childProv, childModel, routeSource := e.resolveSubtaskModel(parent, subType, spec, hasSpec, childReadOnly, req.Model, cfgDoc)
	res := SubtaskResult{
		SubagentType: subType,
		ProviderID:   childProv,
		ModelID:      childModel,
		Routing:      routeSource,
		ReadOnly:     childReadOnly,
	}

	subCtx, cancel := context.WithTimeout(ctx, subtaskTimeout)
	defer cancel()

	releaseSlot, slotErr := e.acquireSubtaskSlot(subCtx)
	if slotErr != nil {
		res.Summary = slotErr.Error()
		return res, slotErr
	}
	defer releaseSlot()

	childMode := "plan"
	childEffort := "low"
	if !childReadOnly {
		childMode = "build"
		if strings.TrimSpace(parent.Effort) != "" {
			childEffort = parent.Effort
		} else {
			childEffort = "medium"
		}
	}
	childMaxSteps := subtaskMaxSteps
	if hasSpec && spec.Steps > 0 {
		childMaxSteps = spec.Steps
	}
	childSystem := buildSubtaskSystemPrompt(subType, req.Description, spec, childReadOnly, parent.System)

	startMeta := map[string]any{
		"subagent_type": subType,
		"providerID":    childProv,
		"modelID":       childModel,
		"routing":       routeSource,
		"readOnly":      childReadOnly,
		"mode":          childMode,
	}
	if childProv != "" && childModel != "" {
		startMeta["model"] = childProv + "/" + childModel
	}
	if parentEmit != nil && req.ParentCallID != "" {
		parentEmit(EngineEvent{
			Kind:         EngineSubtaskStart,
			ParentCallID: req.ParentCallID,
			Title:        req.Description,
			Text:         req.Prompt,
			Metadata:     startMeta,
		})
	}

	var latestText string
	var childMutated bool
	var childTruncated bool
	var mu sync.Mutex
	childReq := TurnRequest{
		SessionID:      parent.SessionID,
		WorktreeDir:    parent.WorktreeDir,
		Mode:           childMode,
		ProviderID:     childProv,
		ModelID:        childModel,
		Effort:         childEffort,
		System:         childSystem,
		UserPrompt:     req.Prompt,
		MaxSteps:       childMaxSteps,
		SubtaskDepth:   parent.SubtaskDepth + 1,
		PermissionHook: parent.PermissionHook,
		CodeSearch:     parent.CodeSearch,
		SymbolInspect:  parent.SymbolInspect,
		ImpactInspect:  parent.ImpactInspect,
		MCPConfig:      parent.MCPConfig,
		HostInvoke:     parent.HostInvoke,
		Sink: EventSinkFunc(func(evt EngineEvent) {
			switch evt.Kind {
			case EngineTextEnd:
				mu.Lock()
				if strings.TrimSpace(evt.Text) != "" {
					latestText = evt.Text
				}
				mu.Unlock()
				if parentEmit != nil && req.ParentCallID != "" {
					evt.ParentCallID = req.ParentCallID
					parentEmit(evt)
				}
			case EngineThinkingEnd:
				if parentEmit != nil && req.ParentCallID != "" {
					evt.ParentCallID = req.ParentCallID
					parentEmit(evt)
				}
			case EngineToolRunning, EngineToolCompleted, EngineToolError:
				if isSubtaskToolMutating(evt.ToolName, evt.Input) {
					mu.Lock()
					childMutated = true
					mu.Unlock()
				}
				if parentEmit != nil && req.ParentCallID != "" {
					evt.ParentCallID = req.ParentCallID
					parentEmit(evt)
				}
			case EngineTurnFinish:
				if isTruncatedFinishReason(evt.Finish) {
					mu.Lock()
					childTruncated = true
					mu.Unlock()
				}
			case EngineTurnUsage:
				if evt.Usage != nil && parentEmit != nil {
					evt.ParentCallID = req.ParentCallID
					parentEmit(evt)
				}
			}
		}),
	}
	if !childReadOnly {
		childReq.CommandRunner = parent.CommandRunner
		childReq.Diagnostics = parent.Diagnostics
	}

	subErr := e.RunTurn(subCtx, childReq)
	mu.Lock()
	res.Mutated = childMutated
	wasTruncated := childTruncated
	summary := strings.TrimSpace(latestText)
	mu.Unlock()
	if parentEmit != nil && req.ParentCallID != "" {
		var errStr string
		if subErr != nil {
			errStr = subErr.Error()
		}
		endMeta := make(map[string]any, len(startMeta)+1)
		for k, v := range startMeta {
			endMeta[k] = v
		}
		endMeta["mutated"] = res.Mutated
		parentEmit(EngineEvent{
			Kind:         EngineSubtaskEnd,
			ParentCallID: req.ParentCallID,
			ErrorText:    errStr,
			Metadata:     endMeta,
		})
	}

	if subErr != nil {
		if summary != "" {
			res.Summary = summary
			return res, subErr
		}
		res.Summary = subErr.Error()
		return res, subErr
	}
	if summary == "" {
		summary = "(subagent completed with no text output)"
	} else if wasTruncated {
		summary += "\n\n[Note: subagent response reached output token limit and may be truncated]"
	}
	res.Summary = summary
	return res, nil
}

func isSubtaskToolMutating(toolName string, input map[string]any) bool {
	lower := strings.ToLower(strings.TrimSpace(toolName))
	switch lower {
	case "", "read", "glob", "grep", "code_search", "symbol_inspect", "impact_analysis",
		"todoread", "todowrite", "question", "webfetch", "mcp_list_tools", "skill", "goal_report", "task":
		return false
	case "hoxi_browser":
		act, _ := input["action"].(string)
		act = strings.ToLower(strings.TrimSpace(act))
		return act != "snapshot" && act != "screenshot"
	case "hoxi_tools":
		act, _ := input["action"].(string)
		act = strings.ToLower(strings.TrimSpace(act))
		return act != "discover" && act != "inspect"
	case "hoxi_remote", "hoxi_data":
		act, _ := input["action"].(string)
		switch strings.ToLower(strings.TrimSpace(act)) {
		case "list", "sql_query":
			return false
		case "http_request":
			m, _ := input["method"].(string)
			m = strings.ToUpper(strings.TrimSpace(m))
			return m != "" && m != "GET" && m != "HEAD"
		default:
			return true
		}
	default:
		return true
	}
}

func buildSubtaskSystemPrompt(subType, description string, spec llm.AgentSpec, readOnly bool, parentSystem string) string {
	base := strings.TrimSpace(spec.Prompt)
	if base == "" {
		if readOnly {
			base = "You are a focused read-only codebase exploration subagent. Use code_search, symbol_inspect, impact_analysis, glob, grep, and read in parallel batches, then return a concise factual report citing exact file paths and line numbers."
		} else {
			base = "You are a focused general execution subagent. Complete the delegated subtask accurately, verify your work, and return a concise summary citing modified or inspected file paths."
		}
	}
	out := base
	if strings.TrimSpace(description) != "" {
		out = fmt.Sprintf("%s\n\nSubagent role: %s (%s).", out, subType, strings.TrimSpace(description))
	}
	if inherited := extractInheritedSubtaskConstraints(parentSystem, readOnly); inherited != "" {
		out += "\n\n" + inherited
	}
	return out
}

func (e *Engine) isParallelSafeCall(call ToolCall, parentMode string, agentMap map[string]llm.AgentSpec) bool {
	if call.Name != "task" {
		return e.isReadOnlyCall(call.Name)
	}
	if strings.EqualFold(strings.TrimSpace(parentMode), "plan") {
		return true
	}
	var args struct {
		SubagentType string `json:"subagent_type"`
		Agent        string `json:"agent"`
	}
	_ = json.Unmarshal(call.Input, &args)
	subType := strings.ToLower(strings.TrimSpace(args.SubagentType))
	if subType == "" {
		subType = strings.ToLower(strings.TrimSpace(args.Agent))
	}
	if subType == "" {
		subType = "explore"
	}
	if spec, ok := agentMap[subType]; ok {
		return spec.ReadOnly
	}
	return subType != "general"
}
