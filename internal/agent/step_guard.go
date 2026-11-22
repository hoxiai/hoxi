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
	identicalFailBlockCount = 3
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
// workspace mutation) and repeated identical failing tool calls so a confused
// model stops burning steps in place.
type stepLoopGuard struct {
	mu                      sync.Mutex
	readsSinceMutation      map[string]int
	failedCallsSinceSuccess map[string]int
}

func newStepLoopGuard() *stepLoopGuard {
	return &stepLoopGuard{
		readsSinceMutation:      make(map[string]int),
		failedCallsSinceSuccess: make(map[string]int),
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

func isWorkspaceMutatingTool(call ToolCall) bool {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	switch name {
	case "task", "load_host_tool", "todowrite", "todoread", "question", "plan_consent", "skill", "goal_report":
		return false
	case "bash":
		var args struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(call.Input, &args)
		act := strings.ToLower(strings.TrimSpace(args.Action))
		return act != "output" && act != "list"
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
		clear(g.failedCallsSinceSuccess)
		g.mu.Unlock()
	}
}

func (g *stepLoopGuard) CheckBeforeExecute(call ToolCall) (blocked bool, msg string) {
	if g == nil {
		return false, ""
	}
	key := canonicalToolSignature(call)
	g.mu.Lock()
	failCount := g.failedCallsSinceSuccess[key]
	if failCount >= identicalFailBlockCount {
		g.mu.Unlock()
		return true, fmt.Sprintf(
			"[loop guard] Blocked repeated failing %s call (already failed %d consecutive times with identical arguments and no intervening successful workspace change). Inspect the prior error and adjust your arguments or approach.",
			call.Name, failCount,
		)
	}
	if isLoopGuardedReadTool(call.Name) {
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
	g.mu.Unlock()
	return false, ""
}

func (g *stepLoopGuard) ObserveStepCalls(calls []ToolCall, results []ToolOutput, isReadOnly func(string) bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	mutated := false
	failedMutationKey := ""
	for i, c := range calls {
		succeeded := i < len(results) && !results[i].IsError
		mutating := !isReadOnly(c.Name) && isWorkspaceMutatingTool(c)
		if i < len(results) && (c.Name == "apply_patch" || strings.EqualFold(strings.TrimSpace(c.Name), "task")) {
			// A partial patch or child edit invalidates reads. The failed call
			// itself still counts toward the identical-failure retry limit.
			changed, _ := results[i].Metadata["completedMutation"].(bool)
			mutated = mutated || changed
			if changed && !succeeded {
				failedMutationKey = canonicalToolSignature(c)
			}
		}
		if mutating {
			if succeeded {
				mutated = true
				delete(g.failedCallsSinceSuccess, canonicalToolSignature(c))
				continue
			}
			if i < len(results) && results[i].IsError {
				g.failedCallsSinceSuccess[canonicalToolSignature(c)]++
			}
			continue
		}
		if isLoopGuardedReadTool(c.Name) && i < len(results) {
			key := canonicalToolSignature(c)
			if succeeded {
				delete(g.failedCallsSinceSuccess, key)
				if cnt := g.readsSinceMutation[key]; cnt == identicalReadWarnCount {
					results[i].Output = strings.TrimRight(results[i].Output, "\n") + fmt.Sprintf(
						"\n\n[loop guard notice: identical %s call executed %d times without intervening workspace changes; avoid repeating this exact call]",
						c.Name, cnt,
					)
				}
			} else if !strings.HasPrefix(results[i].Output, "[loop guard] Blocked ") {
				if g.readsSinceMutation[key] > 0 {
					g.readsSinceMutation[key]--
				}
				g.failedCallsSinceSuccess[key]++
			}
		} else if !isLoopGuardedReadTool(c.Name) && i < len(results) {
			key := canonicalToolSignature(c)
			if results[i].IsError {
				g.failedCallsSinceSuccess[key]++
			} else {
				delete(g.failedCallsSinceSuccess, key)
			}
		}
	}
	if mutated {
		failures := g.failedCallsSinceSuccess[failedMutationKey]
		clear(g.readsSinceMutation)
		clear(g.failedCallsSinceSuccess)
		if failures > 0 {
			g.failedCallsSinceSuccess[failedMutationKey] = failures
		}
	}
}

func stepBudgetNoticeLead(maxSteps int) int {
	if maxSteps < 8 {
		return 0
	}
	lead := maxSteps / 5
	if lead < 2 {
		lead = 2
	}
	if lead > 8 {
		lead = 8
	}
	return lead
}

// maybeAppendStepBudgetNotice injects a one-time wind-down hint into the last
// tool output when a turn approaches its step budget, avoiding mid-task abrupt
// tool cutoff without mutating the cached system prompt prefix.
func maybeAppendStepBudgetNotice(step, maxSteps int, results []ToolOutput) {
	if len(results) == 0 {
		return
	}
	lead := stepBudgetNoticeLead(maxSteps)
	if lead <= 0 || step != maxSteps-lead {
		return
	}
	remaining := maxSteps - step
	notice := fmt.Sprintf(
		"[step budget notice: %d/%d steps used (%d remaining). Stop broad exploration now; complete edits, run targeted verification, and prepare your concise final summary.]",
		step, maxSteps, remaining,
	)
	last := len(results) - 1
	trimmed := strings.TrimRight(results[last].Output, "\n")
	if trimmed == "" {
		results[last].Output = notice
	} else {
		results[last].Output = trimmed + "\n\n" + notice
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
	compactLimit int,
	stepBudget int,
	effectiveEffort string,
	foldUnsignedToolSteps bool,
	nextPartID func(string) string,
	emit func(EngineEvent),
) ([]ContentBlock, bool) {
	if ctx.Err() != nil {
		return nil, false
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
	if foldUnsignedToolSteps && !tailToolExchangeHasSignedThinking(finalMsgs) {
		finalMsgs, _ = recoverMessagesFromThinkingSignatureError(finalMsgs)
	}
	systemSuffix := strings.TrimSpace(req.SystemSuffix)
	finalMsgs = e.applyCompact(finalMsgs, appendSystemPromptSuffix(systemPrompt, systemSuffix), req.WorktreeDir, compactLimit)
	httpReq, err := drv.BuildRequest(ModelRequest{
		SessionID:    req.SessionID,
		CacheKey:     req.CacheKey,
		Target:       target,
		System:       systemPrompt,
		SystemSuffix: systemSuffix,
		Messages:     finalMsgs,
		Tools:        nil,
		MaxTokens:    maxOut,
		Effort:       effectiveEffort,
	})
	if err != nil {
		return nil, false
	}
	openStarted := time.Now()
	resp, openErr := e.stream.OpenStream(ctx, httpReq)
	headerLatency := time.Since(openStarted)
	if openErr != nil {
		return nil, false
	}

	step := stepBudget + 1
	streamed, parseErr := collectStepStream(ctx, drv, resp.Body, target, step, nextPartID, emit)
	_ = resp.Body.Close()
	if parseErr != nil {
		e.reportStreamOutcome(ctx, target, time.Since(openStarted), parseErr)
		return nil, false
	}
	e.reportStreamOutcome(ctx, target, headerLatency, nil)
	if streamed.visibleTextLen == 0 {
		return nil, false
	}
	outFinish := "stop"
	if isTruncatedFinishReason(streamed.stepFinish) {
		outFinish = "length"
	} else if strings.EqualFold(streamed.stepFinish, "content_filter") {
		outFinish = "content_filter"
	}
	emit(EngineEvent{Kind: EngineTurnFinish, Step: step, Finish: outFinish})
	var finalBlocks []ContentBlock
	for _, b := range streamed.asstBlocks {
		if b.Type == ContentToolUse {
			continue
		}
		finalBlocks = append(finalBlocks, b)
	}
	return finalBlocks, true
}
