package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

const (
	defaultBuildMaxSteps    = 128
	defaultPlanMaxSteps     = 64
	compactionStepBonusCap  = 16
	identicalReadWarnCount  = 3
	identicalReadBlockCount = 4
)

func resolveEffectiveMaxSteps(requested int, effectiveMode string, subtaskDepth int, cfgDoc map[string]any) (maxSteps int, autoScaled bool) {
	if requested > 0 {
		return requested, false
	}
	if subtaskDepth == 0 && cfgDoc != nil {
		for _, k := range []string{"maxSteps", "max_steps"} {
			if n := configPositiveInt(cfgDoc[k]); n > 0 {
				return n, false
			}
		}
	}
	if subtaskDepth > 0 {
		return subtaskMaxSteps, false
	}
	if strings.EqualFold(strings.TrimSpace(effectiveMode), "plan") {
		return defaultPlanMaxSteps, true
	}
	return defaultBuildMaxSteps, true
}

func configPositiveInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	default:
		return 0
	}
}

func compactionRecoveryStepBonus(currentStep int) int {
	bonus := currentStep / 2
	if bonus < 8 {
		bonus = 8
	}
	if bonus > compactionStepBonusCap {
		bonus = compactionStepBonusCap
	}
	return bonus
}

// stepLoopGuard detects unproductive read-only thrashing (calling the exact
// same read/search tool with identical arguments repeatedly with no intervening
// workspace mutation) so a confused model stops burning steps in place.
type stepLoopGuard struct {
	mu                 sync.Mutex
	readsSinceMutation map[string]int
}

func newStepLoopGuard() *stepLoopGuard {
	return &stepLoopGuard{
		readsSinceMutation: make(map[string]int),
	}
}

func isLoopGuardedReadTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "grep", "glob", "hoxi_code_index", "code_search", "symbol_inspect", "impact_analysis":
		return true
	default:
		return false
	}
}

func isWorkspaceMutatingTool(call ToolCall, parentMode string, agentMap map[string]llm.AgentSpec) bool {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	switch name {
	case "todowrite", "todoread", "question", "plan_consent", "skill", "goal_report":
		return false
	case "bash":
		var args struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(call.Input, &args)
		act := strings.ToLower(strings.TrimSpace(args.Action))
		return act != "output" && act != "list"
	case "task":
		if strings.EqualFold(strings.TrimSpace(parentMode), "plan") {
			return false
		}
		var args struct {
			SubagentType string `json:"subagent_type"`
			Agent        string `json:"agent"`
		}
		_ = json.Unmarshal(call.Input, &args)
		sub := strings.ToLower(strings.TrimSpace(args.SubagentType))
		if sub == "" {
			sub = strings.ToLower(strings.TrimSpace(args.Agent))
		}
		if sub == "" {
			sub = "explore"
		}
		if spec, ok := agentMap[sub]; ok {
			return !spec.ReadOnly
		}
		return sub == "general"
	default:
		return true
	}
}

func canonicalToolSignature(call ToolCall) string {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	raw := strings.TrimSpace(string(call.Input))
	if raw == "" {
		return name + "\x00{}"
	}
	var normalized any
	if json.Unmarshal(call.Input, &normalized) == nil {
		if b, err := json.Marshal(normalized); err == nil {
			return name + "\x00" + string(b)
		}
	}
	return name + "\x00" + raw
}

func (g *stepLoopGuard) ResetOnCompaction() {
	if g != nil {
		g.mu.Lock()
		clear(g.readsSinceMutation)
		g.mu.Unlock()
	}
}

func (g *stepLoopGuard) CheckBeforeExecute(call ToolCall) (blocked bool, msg string) {
	if g == nil || !isLoopGuardedReadTool(call.Name) {
		return false, ""
	}
	key := canonicalToolSignature(call)
	g.mu.Lock()
	count := g.readsSinceMutation[key] + 1
	g.readsSinceMutation[key] = count
	g.mu.Unlock()
	if count >= identicalReadBlockCount {
		return true, fmt.Sprintf(
			"[loop guard] Blocked repeated %s call (#%d with identical arguments and no workspace edits or commands in between). Use the output already in context, read a different offset/file, or proceed with your edit/summary.",
			call.Name, count,
		)
	}
	return false, ""
}

func (g *stepLoopGuard) ObserveStepCalls(calls []ToolCall, results []ToolOutput, isReadOnly func(string) bool, parentMode string, agentMap map[string]llm.AgentSpec) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	mutated := false
	for i, c := range calls {
		if !isReadOnly(c.Name) && isWorkspaceMutatingTool(c, parentMode, agentMap) {
			mutated = true
			continue
		}
		if isLoopGuardedReadTool(c.Name) && i < len(results) && !results[i].IsError {
			key := canonicalToolSignature(c)
			if cnt := g.readsSinceMutation[key]; cnt == identicalReadWarnCount {
				results[i].Output = strings.TrimRight(results[i].Output, "\n") + fmt.Sprintf(
					"\n\n[loop guard notice: identical %s call executed %d times without intervening workspace changes; avoid repeating this exact call]",
					c.Name, cnt,
				)
			}
		}
	}
	if mutated {
		clear(g.readsSinceMutation)
	}
}

// runGracefulFinalizeStep runs one final tool-free synthesis turn when the agent
// exhausts its step budget while still requesting tools, streaming a clean
// progress & handoff summary instead of aborting the turn with an error.
func (e *Engine) runGracefulFinalizeStep(
	ctx context.Context,
	drv ModelDriver,
	target llm.StreamTarget,
	req TurnRequest,
	systemPrompt string,
	msgs []Message,
	maxOut int,
	stepBudget int,
	nextPartID func(string) string,
	emit func(EngineEvent),
) bool {
	if ctx.Err() != nil {
		return false
	}
	finalMsgs := make([]Message, 0, len(msgs)+1)
	finalMsgs = append(finalMsgs, msgs...)
	finalMsgs = append(finalMsgs, Message{
		Role: RoleUser,
		Content: []ContentBlock{{
			Type: ContentText,
			Text: fmt.Sprintf(
				"[System notice: step budget (%d steps) reached. Tool calls are disabled for this final response. Do NOT output tool calls or pseudo-tool markup. Summarize clearly for the user: (1) changes completed and verified so far, (2) current status of touched files, and (3) exact remaining steps to continue in the next turn.]",
				stepBudget,
			),
		}},
	})
	finalMsgs = e.applyCompact(finalMsgs, systemPrompt, req.WorktreeDir, target.ContextLimit)
	httpReq, err := drv.BuildRequest(ModelRequest{
		Target:    target,
		System:    systemPrompt,
		Messages:  finalMsgs,
		Tools:     nil,
		MaxTokens: maxOut,
		Effort:    req.Effort,
	})
	if err != nil {
		return false
	}
	openStarted := time.Now()
	resp, openErr := e.stream.OpenStream(ctx, httpReq)
	headerLatency := time.Since(openStarted)
	if openErr != nil {
		return false
	}

	step := stepBudget + 1
	streamed, parseErr := collectStepStream(ctx, drv, resp.Body, target, step, nextPartID, emit)
	_ = resp.Body.Close()
	if parseErr != nil {
		e.reportStreamOutcome(ctx, target, time.Since(openStarted), parseErr)
		return false
	}
	e.reportStreamOutcome(ctx, target, headerLatency, nil)
	if streamed.visibleTextLen == 0 {
		return false
	}
	outFinish := "stop"
	if isTruncatedFinishReason(streamed.stepFinish) {
		outFinish = "length"
	} else if strings.EqualFold(streamed.stepFinish, "content_filter") {
		outFinish = "content_filter"
	}
	emit(EngineEvent{Kind: EngineTurnFinish, Step: step, Finish: outFinish})
	return true
}
