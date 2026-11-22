package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/llm"
)

func subtaskPolicyDecision(
	primaryMode, targetSubagent string,
	byKey map[string]llm.AgentSpec,
	cfgDoc map[string]any,
) string {
	return llm.EvalTaskPolicyDecision(primaryMode, targetSubagent, byKey, cfgDoc)
}

func evalTaskPolicyValue(raw any, targetSubagent string) string {
	return llm.EvalWildcardPolicy(raw, targetSubagent)
}

func (e *Engine) resolveSubtaskModel(
	parent TurnRequest,
	subType string,
	spec llm.AgentSpec,
	hasSpec bool,
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
	var resumed *savedSubtaskState
	subtaskID := strings.TrimSpace(req.ResumeID)
	if subtaskID != "" {
		saved, ok := loadSessionSubtask(parent.SessionID, subtaskID, parent.WorktreeDir)
		if !ok {
			err := fmt.Errorf("subagent %q is not available in this session; omit resume to start a fresh subagent", subtaskID)
			return SubtaskResult{Summary: err.Error(), SubagentType: strings.TrimSpace(req.SubagentType), ReadOnly: true}, err
		}
		resumed = &saved
	} else {
		subtaskID = nextSubtaskID()
	}

	subType := strings.TrimSpace(req.SubagentType)
	if resumed != nil && strings.TrimSpace(resumed.SubagentType) != "" {
		subType = resumed.SubagentType
	}
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
	if resumed != nil && resumed.ReadOnly {
		childReadOnly = true
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

	modelOverride := req.Model
	if strings.TrimSpace(modelOverride) == "" && resumed != nil && resumed.ProviderID != "" && resumed.ModelID != "" {
		modelOverride = resumed.ProviderID + "/" + resumed.ModelID
	}
	childProv, childModel, routeSource := e.resolveSubtaskModel(parent, subType, spec, hasSpec, modelOverride, cfgDoc)
	if resumed != nil && strings.TrimSpace(req.Model) == "" && routeSource == "call_override" {
		routeSource = "resumed"
	}
	res := SubtaskResult{
		SubagentType: subType,
		ProviderID:   childProv,
		ModelID:      childModel,
		Routing:      routeSource,
		ReadOnly:     childReadOnly,
	}

	// Inherit the parent's lifetime, including Stop and any caller deadline.
	// Tool/request timeouts and the step budget still bound individual work;
	// a wall-clock task timeout would also consume time awaiting user approval.
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	releaseSlot, slotErr := e.acquireSubtaskSlot(subCtx)
	if slotErr != nil {
		res.Summary = slotErr.Error()
		return res, slotErr
	}
	defer releaseSlot()

	childMode := "plan"
	childEffort := resolveSubtaskEffort(parent.Effort, childReadOnly)
	if !childReadOnly {
		childMode = "build"
	}
	childMaxSteps := subtaskMaxSteps
	if hasSpec && spec.Steps > 0 {
		childMaxSteps = spec.Steps
	}
	childSystem := buildSubtaskSystemPrompt(spec, childReadOnly, parent.System)
	childSystemSuffix := formatSubtaskRoleSuffix(subType, req.Description)
	childCacheKey := buildSubtaskCacheKey(parent, req, subType)

	startMeta := map[string]any{
		"subagent_type": subType,
		"providerID":    childProv,
		"modelID":       childModel,
		"routing":       routeSource,
		"readOnly":      childReadOnly,
		"mode":          childMode,
	}
	if childEffort != "" {
		startMeta["effort"] = childEffort
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
	var childMutated, completedMutation bool
	var childTruncated bool
	var capturedMsgs []Message
	var mu sync.Mutex
	var childHistory []Message
	if resumed != nil {
		childHistory = resumed.Messages
	}
	childRevisions := parent.FileRevisions.NewChildTracker()
	childReq := TurnRequest{
		SessionID:      parent.SessionID,
		CacheKey:       childCacheKey,
		WorktreeDir:    parent.WorktreeDir,
		Mode:           childMode,
		ProviderID:     childProv,
		ModelID:        childModel,
		Effort:         childEffort,
		System:         childSystem,
		SystemSuffix:   childSystemSuffix,
		History:        childHistory,
		UserPrompt:     req.Prompt,
		MaxSteps:       childMaxSteps,
		SubtaskDepth:   parent.SubtaskDepth + 1,
		FileRevisions:  childRevisions,
		PermissionHook: parent.PermissionHook,
		RepoMap:        parent.RepoMap,
		CodeSearch:     parent.CodeSearch,
		SymbolInspect:  parent.SymbolInspect,
		ImpactInspect:  parent.ImpactInspect,
		MCPConfig:      parent.MCPConfig,
		onFinalMessages: func(final []Message) {
			mu.Lock()
			capturedMsgs = cloneMessagesForSubtask(final)
			mu.Unlock()
		},
		HostInvoke: parent.HostInvoke,
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
					partial, _ := evt.Metadata["completedMutation"].(bool)
					completedMutation = completedMutation || evt.Kind == EngineToolCompleted || (evt.ToolName == "apply_patch" && partial)
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
			case EngineTurnUsage, EngineSummaryUsage:
				if evt.Usage != nil && parentEmit != nil {
					evt.ParentCallID = req.ParentCallID
					parentEmit(evt)
				}
			}
		}),
	}
	if !childReadOnly {
		childReq.CommandRunner = parent.CommandRunner
		childReq.StreamCommandRunner = parent.StreamCommandRunner
		childReq.BackgroundCommands = parent.BackgroundCommands
		childReq.Diagnostics = parent.Diagnostics
		childReq.CoordinateFileEdit = parent.CoordinateFileEdit
		childReq.RecordFileEdit = parent.RecordFileEdit
	}

	subErr := e.RunTurn(subCtx, childReq)
	if parent.FileRevisions != nil {
		parent.FileRevisions.MergeChildObservations(childRevisions)
	}
	mu.Lock()
	res.Mutated = childMutated
	res.CompletedMutation = completedMutation
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
	mu.Lock()
	savedCopy := capturedMsgs
	mu.Unlock()
	if saveSessionSubtask(parent.SessionID, savedSubtaskState{
		ID:           subtaskID,
		SubagentType: subType,
		ReadOnly:     childReadOnly,
		ProviderID:   childProv,
		ModelID:      childModel,
		WorktreeDir:  parent.WorktreeDir,
		Messages:     savedCopy,
	}) {
		res.SubtaskID = subtaskID
	}
	res.Summary = summary
	return res, nil
}

func isSubtaskToolMutating(toolName string, input map[string]any) bool {
	lower := strings.ToLower(strings.TrimSpace(toolName))
	switch lower {
	case "", "read", "glob", "grep", "hoxi_code_index", "code_search", "symbol_inspect", "impact_analysis",
		"todoread", "todowrite", "question", "plan_consent", "webfetch", "websearch", "mcp_list_tools", "load_host_tool", "skill", "goal_report", "task":
		return false
	case "bash":
		act, _ := input["action"].(string)
		act = strings.ToLower(strings.TrimSpace(act))
		return act != "output" && act != "list"
	case "hoxi_browser":
		act, _ := input["action"].(string)
		act = strings.ToLower(strings.TrimSpace(act))
		return act != "snapshot" && act != "screenshot" && act != "diagnostics" && act != "wait"
	case "hoxi_computer":
		act, _ := input["action"].(string)
		act = strings.ToLower(strings.TrimSpace(act))
		app, _ := input["app"].(string)
		if act == "status" || act == "apps" {
			return false
		}
		if (act == "state" || act == "screenshot") && strings.TrimSpace(app) == "" {
			return false
		}
		return true
	case "hoxi_tools":
		act, _ := input["action"].(string)
		act = strings.ToLower(strings.TrimSpace(act))
		return act != "discover" && act != "inspect"
	case "hoxi_remote", "hoxi_data":
		act, _ := input["action"].(string)
		switch strings.ToLower(strings.TrimSpace(act)) {
		case "list", "test", "sql_query":
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

func resolveSubtaskEffort(parentEffort string, readOnly bool) string {
	eff := strings.TrimSpace(parentEffort)
	if !readOnly {
		return eff
	}
	switch strings.ToLower(eff) {
	case "xhigh", "max":
		return "high"
	default:
		return eff
	}
}

// buildSubtaskCacheKey scopes cache/session affinity per (parent session, subagent type)
// so sibling subagents of the same role share the same upstream KV-cache shard and
// relay sticky account (maximizing hit rate on shared tools + static system + AGENTS.md)
// while remaining isolated from the parent conversation and other subagent toolsets.
func buildSubtaskCacheKey(parent TurnRequest, _ SubtaskRequest, subType string) string {
	base := strings.TrimSpace(parent.CacheKey)
	if base == "" {
		base = strings.TrimSpace(parent.SessionID)
	}
	if base == "" {
		return ""
	}
	part := strings.ToLower(strings.TrimSpace(subType))
	if part == "" {
		part = "subagent"
	}
	candidate := base + ":sub:" + part
	if len(candidate) <= 64 {
		return candidate
	}
	sum := sha256.Sum256([]byte(candidate))
	return "hoxi_sub_" + hex.EncodeToString(sum[:24])
}

func formatSubtaskRoleSuffix(subType, description string) string {
	desc := strings.TrimSpace(description)
	if desc == "" {
		return ""
	}
	return fmt.Sprintf("Subagent role: %s (%s).", subType, desc)
}

func buildSubtaskSystemPrompt(spec llm.AgentSpec, readOnly bool, parentSystem string) string {
	base := strings.TrimSpace(spec.Prompt)
	if base == "" {
		if readOnly {
			base = "You are a focused read-only codebase exploration subagent. Use hoxi_code_index, code_search, symbol_inspect, impact_analysis, glob, grep, and read in parallel batches, then return a concise factual report citing exact file paths and line numbers."
		} else {
			base = "You are a focused general execution subagent. Complete the delegated subtask accurately, verify your work, and return a concise summary citing modified or inspected file paths."
		}
	}
	out := base
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
		Resume       string `json:"resume"`
	}
	_ = json.Unmarshal(call.Input, &args)
	// Always serialize resumed subagents to avoid concurrent writes to the same
	// saved transcript or accidental parallel execution of a mutating subagent.
	if strings.TrimSpace(args.Resume) != "" {
		return false
	}
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
